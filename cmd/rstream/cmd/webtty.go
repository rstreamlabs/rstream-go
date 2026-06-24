// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

const (
	webTTYAuthTokenEnv                           = "RSTREAM_WEBTTY_AUTH_TOKEN"
	webTTYAuthorizedClientKeysEnv                = "RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS"
	webTTYAuthorizedClientsFileEnv               = "RSTREAM_WEBTTY_AUTHORIZED_CLIENTS_FILE"
	webTTYIdentityEnv                            = "RSTREAM_WEBTTY_IDENTITY"
	webTTYIdentityFileEnv                        = "RSTREAM_WEBTTY_IDENTITY_FILE"
	webTTYKnownServerKeyEnv                      = "RSTREAM_WEBTTY_KNOWN_SERVER_KEY"
	webTTYKnownServersFileEnv                    = "RSTREAM_WEBTTY_KNOWN_SERVERS_FILE"
	webTTYConfigEnv                              = "RSTREAM_WEBTTY_CONFIG"
	webTTYManagedSessionModeQuery                = "session_mode"
	webTTYManagedSessionModeInteractive          = "interactive"
	webTTYManagedSessionModeNonInteractive       = "non-interactive"
	defaultWebTTYFSMaxUploadSize           int64 = 64 * 1024 * 1024
)

type commandExitError struct {
	code int
}

type webTTYClientRunOptions struct {
	DefaultOutput      string
	ForceNoInteractive bool
	ForceNoTTY         bool
	Subcommand         string
}

type webTTYClientResult struct {
	URL        string   `json:"url"`
	Command    []string `json:"command,omitempty"`
	ExitCode   int      `json:"exit_code"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	DurationMS int64    `json:"duration_ms"`
}

type webTTYClientRuntimeE2EContext struct {
	controlClient *controlplane.Client
	project       controlplane.Project
	serverID      string
}

type webTTYServerPayloadCryptoConfig struct {
	Resolver         webtty.PayloadCryptoResolver
	EndpointIdentity *webtty.WebTTYEndpointIdentity
	HostKeyID        string
}

type webTTYKnownServerSource struct {
	Recipient        webtty.E2ERecipient
	EndpointIdentity *webtty.WebTTYEndpointIdentityPublic
	Name             string
	ClientIdentity   string
}

type webTTYClientCryptoConfig struct {
	PayloadCrypto          *webtty.PayloadCrypto
	EndpointIdentity       *webtty.WebTTYEndpointIdentity
	ExpectedServerIdentity *webtty.WebTTYEndpointIdentityPublic
	ClientCredential       []byte
	ClientIdentityName     string
	E2ERequired            bool
	ClientProofRequired    bool
}

type webTTYClientRstreamResolution struct {
	URL        string
	RuntimeE2E *webTTYClientRuntimeE2EContext
	Scope      webTTYClientSecurityScope
}

type webTTYClientSecurityScope struct {
	Target              string
	HostKeyID           string
	E2ERequired         bool
	ClientProofRequired bool
}

func (e *commandExitError) Error() string {
	return fmt.Sprintf("remote command exited with code %d", e.code)
}

func (e *commandExitError) ExitCode() int {
	if e.code <= 0 {
		return 1
	}
	if e.code > 255 {
		return 255
	}
	return e.code
}

var webttyCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "webtty",
	Short:        "Web Remote Terminal (WebTTY)",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var webttyServerCmd = &cobra.Command{
	Use:          "server",
	Short:        "Web Remote Terminal (Server)",
	GroupID:      "webtty-server",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	Example: strings.TrimSpace(`
# Start a WebTTY server through the current rstream project
rstream webtty server -v --rstream --name shell

# Start a registered WebTTY server after enrollment
rstream webtty server -v --server-id server_id

# Start from a service-manager friendly runtime config
rstream webtty server -v --webtty-config /etc/rstream/webtty/prod-shell.yaml

# Start a local validation server without creating a tunnel
rstream webtty server -v --listen 127.0.0.1:8080 --allow-unauthenticated`),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := slog.With("cmd", "webtty", "subcmd", "server")
		if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
			return err
		}
		if err := applyWebTTYServerDerivedDefaults(cmd); err != nil {
			return err
		}
		if err := validateWebTTYServerFlags(cmd); err != nil {
			return err
		}
		autoReconnect := webTTYServerAutoReconnect(cmd)
		retryInterval, err := webTTYServerRetryInterval(cmd)
		if err != nil {
			return err
		}
		shutdownTimeoutMs, _ := cmd.Flags().GetInt64("shutdown-timeout")
		shutdownTimeout := time.Duration(shutdownTimeoutMs) * time.Millisecond
		var stableHostname *string
		return runWebTTYServerRetryLoop(ctx, logger, autoReconnect, retryInterval, func() error {
			return runWebTTYServerOnce(ctx, cmd, logger, shutdownTimeout, &stableHostname)
		})
	},
}

func runWebTTYServerRetryLoop(ctx context.Context, logger *slog.Logger, autoReconnect bool, retryInterval time.Duration, runOnce func() error) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := runOnce()
		if err == nil {
			return nil
		}
		if !autoReconnect || !webTTYServerRetryableError(err) {
			return err
		}
		logger.Warn("webtty server disconnected; reconnecting", "error", err, "retry_in", retryInterval)
		select {
		case <-time.After(retryInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func webTTYServerAutoReconnect(cmd *cobra.Command) bool {
	autoReconnect := webttyServerUsesRstream(cmd)
	retryPtr := getBoolPtr(cmd, "retry")
	noRetryPtr := getBoolPtr(cmd, "no-retry")
	switch {
	case retryPtr != nil:
		autoReconnect = *retryPtr
	case noRetryPtr != nil && *noRetryPtr:
		autoReconnect = false
	}
	return autoReconnect
}

func webTTYServerRetryInterval(cmd *cobra.Command) (time.Duration, error) {
	retryInterval := 5 * time.Second
	intervalPtr := getInt64Ptr(cmd, "retry-interval")
	if intervalPtr == nil {
		return retryInterval, nil
	}
	if *intervalPtr <= 0 {
		return 0, fmt.Errorf("--retry-interval must be greater than 0")
	}
	return time.Duration(*intervalPtr) * time.Millisecond, nil
}

func webTTYServerRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"control channel closed",
		"failed to dial engine",
		"connection refused",
		"connection reset by peer",
		"connection aborted",
		"broken pipe",
		"i/o timeout",
		"network is unreachable",
		"no route to host",
		"unexpected eof",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func runWebTTYServerOnce(ctx context.Context, cmd *cobra.Command, logger *slog.Logger, shutdownTimeout time.Duration, stableHostname **string) error {
	useRstream := webttyServerUsesRstream(cmd)
	var runtime *resolvedRuntime
	var err error
	if useRstream {
		runtime, err = resolveRuntime(cmd, true, true)
		if err != nil {
			return fmt.Errorf("failed to resolve runtime: %w", err)
		}
	}
	transport, err := webTTYTransportFromFlag(cmd)
	if err != nil {
		return err
	}
	serverEnrollment, _, err := readWebTTYServerEnrollmentFromFlags(cmd)
	if err != nil {
		return err
	}
	if err := validateRegisteredWebTTYServerEnrollment(serverEnrollment); err != nil {
		return err
	}
	executionMode, err := webTTYExecutionModeFromFlag(cmd)
	if err != nil {
		return err
	}
	authToken, err := readWebTTYAuthToken(cmd)
	if err != nil {
		return err
	}
	allowUnauthenticated, _ := cmd.Flags().GetBool("allow-unauthenticated")
	if useRstream && authToken != nil {
		return fmt.Errorf("--auth-token-file and %s are only used by local WebTTY WebSocket/WebTransport servers", webTTYAuthTokenEnv)
	}
	if transport == webtty.WebTTYTransportPlain && authToken != nil {
		return fmt.Errorf("plain WebTTY transport does not support HTTP bearer tokens")
	}
	if transport == webtty.WebTTYTransportPlain && !useRstream && !allowUnauthenticated {
		return fmt.Errorf("local plain WebTTY transport has no HTTP auth layer; use --allow-unauthenticated only for isolated development")
	}
	if !useRstream && transport != webtty.WebTTYTransportPlain && authToken == nil && !allowUnauthenticated {
		return fmt.Errorf("local webtty server requires --auth-token-file or %s; use --allow-unauthenticated only for isolated development", webTTYAuthTokenEnv)
	}
	if useRstream && authToken == nil {
		allowUnauthenticated = true
	}
	allowedOrigins, err := webTTYServerAllowedOrigins(cmd, useRstream)
	if err != nil {
		return err
	}
	payloadCryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, serverEnrollment)
	if err != nil {
		return err
	}
	workspaceManagedServer := webTTYServerEnrollmentWorkspaceManaged(serverEnrollment)
	if workspaceManagedServer && webTTYExplicitAuthorizedClientSourceConfigured(cmd) {
		return fmt.Errorf("workspace-managed WebTTY servers use trusted workspace devices for client authorization; remove --authorized-client-key, --authorized-clients-file, and %s", webTTYAuthorizedClientKeysEnv)
	}
	authorizedClientSigningKeys := map[string][]byte{}
	var authorizedClientSigningKeyResolver webtty.AuthorizedClientSigningKeyResolver
	if !workspaceManagedServer {
		authorizedClientSigningKeys, err = webTTYAuthorizedClientSigningKeys(cmd)
		if err != nil {
			return err
		}
		authorizedClientSigningKeyResolver, err = webTTYAuthorizedClientSigningKeyResolver(cmd, serverEnrollment, payloadCryptoConfig.EndpointIdentity != nil)
		if err != nil {
			return err
		}
	}
	clientProofVerifier := webTTYWorkspaceClientProofVerifier(serverEnrollment)
	requireClientProof := false
	if payloadCryptoConfig.EndpointIdentity != nil {
		if clientProofVerifier == nil && len(authorizedClientSigningKeys) == 0 && authorizedClientSigningKeyResolver == nil {
			return fmt.Errorf("WebTTY E2E requires an authorized client source; use rstream webtty authorized-client, --authorized-client-key, or %s", webTTYAuthorizedClientKeysEnv)
		}
		requireClientProof = true
	}
	fsRoot, _ := cmd.Flags().GetString("fs-root")
	if strings.TrimSpace(fsRoot) != "" && payloadCryptoConfig.Resolver != nil {
		return fmt.Errorf("--fs-root cannot be used with WebTTY E2E payload encryption; the filesystem sidecar is a separate WebDAV surface")
	}
	hostKeyID := payloadCryptoConfig.HostKeyID
	if payloadCryptoConfig.EndpointIdentity != nil && hostKeyID == "" {
		hostKeyID, err = webTTYServerHostKeyIDFromEnrollment(serverEnrollment)
		if err != nil {
			return err
		}
	}
	requireSessionKeyGrant := payloadCryptoConfig.Resolver != nil
	loginUser := getStringPtr(cmd, "login-user")
	allowClientUser, _ := cmd.Flags().GetBool("allow-client-user")
	terminalHandler := webtty.NewWebTTYHandler(&webtty.ServerConfig{
		SessionCloseDeadline:        &shutdownTimeout,
		AuthToken:                   authToken,
		AllowUnauthenticated:        &allowUnauthenticated,
		AllowedOrigins:              allowedOrigins,
		PayloadCryptoResolver:       payloadCryptoConfig.Resolver,
		RequireSessionKeyGrant:      &requireSessionKeyGrant,
		EndpointIdentity:            payloadCryptoConfig.EndpointIdentity,
		RequireClientProof:          &requireClientProof,
		AuthorizedClientSigningKeys: authorizedClientSigningKeys,
		AuthorizedClientSigningKey:  authorizedClientSigningKeyResolver,
		ClientProofVerifier:         clientProofVerifier,
		WorkspaceID:                 webTTYServerEnrollmentWorkspaceID(serverEnrollment),
		ProjectID:                   webTTYServerEnrollmentProjectID(serverEnrollment),
		ServerID:                    webTTYServerEnrollmentServerID(serverEnrollment),
		ExecutionMode:               &executionMode,
		DefaultUsername:             loginUser,
		AllowClientUser:             &allowClientUser,
		Logger:                      logger,
	})
	handler, err := newWebTTYServerHTTPHandler(cmd, terminalHandler, authToken, allowUnauthenticated, logger)
	if err != nil {
		return err
	}
	if !useRstream && transport == webtty.WebTTYTransportWebTransport {
		addr, _ := cmd.Flags().GetString("listen")
		certFile, _ := cmd.Flags().GetString("tls-cert-file")
		keyFile, _ := cmd.Flags().GetString("tls-key-file")
		return serveWebTransportWebTTY(ctx, addr, terminalHandler, authToken, allowUnauthenticated, allowedOrigins, shutdownTimeout, certFile, keyFile, logger)
	}
	server := &http.Server{Handler: handler}
	var listener net.Listener
	address := ""
	if useRstream {
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return fmt.Errorf("failed to create rstream client: %w", err)
		}
		ctrl, err := client.Connect(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to connect to rstream engine server: %w", err)
		}
		defer ctrl.Close()
		props := newWebTTYServerTunnelProperties(cmd, serverEnrollment)
		applyWebTTYRuntimeSecurityLabels(props.Labels, payloadCryptoConfig, serverEnrollment, requireClientProof, hostKeyID)
		if props.Publish != nil && *props.Publish {
			if stableHostname != nil && *stableHostname != nil {
				props.Hostname = *stableHostname
			} else {
				if err := rstream.MaybeSetGeneratedStableDomain(&props, runtime.Resolved.Engine); err != nil {
					return fmt.Errorf("failed to generate stable domain: %w", err)
				}
				if stableHostname != nil {
					*stableHostname = props.Hostname
				}
			}
		}
		if err := applyWebTTYServerAdmissionLabel(&props, serverEnrollment); err != nil {
			return err
		}
		tunnel, err := ctrl.CreateTunnel(ctx, props)
		if err != nil {
			return fmt.Errorf("failed to create tunnel: %w", err)
		}
		defer tunnel.Close()
		address, err = tunnel.ForwardingAddress()
		if err != nil {
			return fmt.Errorf("failed to get forwarding address: %w", err)
		}
		if transport == webtty.WebTTYTransportWebTransport {
			packetListener, ok := tunnel.(rstream.PacketListener)
			if !ok {
				return fmt.Errorf("webtransport tunnel does not implement rstream.PacketListener")
			}
			tlsConfig, err := generateWebTTYInternalWebTransportTLSConfig()
			if err != nil {
				return err
			}
			logger.Info("webtty webtransport server started", "address", address)
			return serveWebTransportWebTTYOnPacketConn(ctx, rstream.PacketConnFromPacketListener(packetListener), terminalHandler, authToken, allowUnauthenticated, allowedOrigins, shutdownTimeout, tlsConfig, logger)
		}
		nl, ok := tunnel.(interface{ net.Listener })
		if !ok {
			return fmt.Errorf("tunnel does not implement net.Listener")
		}
		listener = nl
	} else {
		addr, _ := cmd.Flags().GetString("listen")
		netListener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", addr, err)
		}
		listener = netListener
		if transport == webtty.WebTTYTransportPlain {
			tlsConfig, err := webTTYPlainServerTLSConfig(cmd)
			if err != nil {
				_ = listener.Close()
				return err
			}
			if tlsConfig != nil {
				listener = tls.NewListener(listener, tlsConfig)
			}
		}
		address = listener.Addr().String()
	}
	logger.Info("webtty server started", "address", address)
	if transport == webtty.WebTTYTransportPlain {
		return servePlainWebTTY(ctx, listener, terminalHandler, shutdownTimeout, logger)
	}
	shutdownOnce := sync.Once{}
	shutdown := func(reason string) {
		shutdownOnce.Do(func() {
			logger.Info("stopping webtty server", "reason", reason)
			shutdownCtx, cancel := webTTYShutdownContext(ctx, shutdownTimeout)
			defer cancel()
			terminalHandler.BeginDrain()
			if err := terminalHandler.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logger.Warn("session shutdown failed", "error", err)
			}
			if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("http server shutdown failed", "error", err)
			}
		})
	}
	stopShutdownWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown("context canceled")
		case <-stopShutdownWatcher:
		}
	}()
	err = server.Serve(listener)
	close(stopShutdownWatcher)
	shutdown("serve loop ended")
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func webTTYShutdownContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func servePlainWebTTY(ctx context.Context, listener net.Listener, terminalHandler *webtty.Handler, shutdownTimeout time.Duration, logger *slog.Logger) error {
	shutdownOnce := sync.Once{}
	shutdown := func(reason string) {
		shutdownOnce.Do(func() {
			logger.Info("stopping plain webtty server", "reason", reason)
			_ = listener.Close()
			shutdownCtx, cancel := webTTYShutdownContext(ctx, shutdownTimeout)
			defer cancel()
			terminalHandler.BeginDrain()
			if err := terminalHandler.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logger.Warn("plain session shutdown failed", "error", err)
			}
		})
	}
	stopShutdownWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown("context canceled")
		case <-stopShutdownWatcher:
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			close(stopShutdownWatcher)
			shutdown("serve loop ended")
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go terminalHandler.ServeConn(conn)
	}
}

func serveWebTransportWebTTY(ctx context.Context, addr string, terminalHandler *webtty.Handler, authToken *string, allowUnauthenticated bool, allowedOrigins []string, shutdownTimeout time.Duration, certFile string, keyFile string, logger *slog.Logger) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to load WebTransport TLS certificate: %w", err)
	}
	packetConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	defer packetConn.Close()
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{http3.NextProtoH3},
	}
	logger.Info("webtty webtransport server started", "address", packetConn.LocalAddr().String())
	return serveWebTransportWebTTYOnPacketConn(ctx, packetConn, terminalHandler, authToken, allowUnauthenticated, allowedOrigins, shutdownTimeout, tlsConfig, logger)
}

func serveWebTransportWebTTYOnPacketConn(ctx context.Context, packetConn net.PacketConn, terminalHandler *webtty.Handler, authToken *string, allowUnauthenticated bool, allowedOrigins []string, shutdownTimeout time.Duration, tlsConfig *tls.Config, logger *slog.Logger) error {
	mux := http.NewServeMux()
	server := &webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       tlsConfig,
			EnableDatagrams: true,
			QUICConfig: &quic.Config{
				EnableDatagrams:                  true,
				EnableStreamResetPartialDelivery: true,
			},
		},
		CheckOrigin: func(r *http.Request) bool {
			return webTTYServerRequestOriginAllowed(r, allowedOrigins, !allowUnauthenticated)
		},
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.Handle("/", webtty.NewBearerAuthHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			logger.Warn("failed to upgrade webtransport session", "error", err)
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		go terminalHandler.ServeWebTransportSession(session.Context(), session)
	}), authToken, allowUnauthenticated))
	shutdownOnce := sync.Once{}
	shutdown := func(reason string) {
		shutdownOnce.Do(func() {
			logger.Info("stopping webtransport webtty server", "reason", reason)
			shutdownCtx, cancel := webTTYShutdownContext(ctx, shutdownTimeout)
			defer cancel()
			terminalHandler.BeginDrain()
			if err := terminalHandler.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logger.Warn("webtransport session shutdown failed", "error", err)
			}
			if err := server.Close(); err != nil {
				logger.Warn("webtransport server shutdown failed", "error", err)
			}
		})
	}
	stopShutdownWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown("context canceled")
		case <-stopShutdownWatcher:
		}
	}()
	err := server.Serve(packetConn)
	close(stopShutdownWatcher)
	shutdown("serve loop ended")
	if err == nil || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func generateWebTTYInternalWebTransportTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate WebTransport TLS key: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"rstream-webtty-internal", "localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate WebTransport TLS certificate: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load generated WebTransport TLS certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{http3.NextProtoH3},
	}, nil
}

func webTTYServerRequestOriginAllowed(r *http.Request, allowed []string, allowSameHost bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if len(allowed) == 1 && allowed[0] == "*" {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), origin) {
			return true
		}
	}
	if !allowSameHost {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

var webttyClientCmd = &cobra.Command{
	Use:          "client [--] [cmd...]",
	Short:        "Web Remote Terminal (Client)",
	GroupID:      "webtty-connect",
	SilenceUsage: true,
	Args:         cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYClient(cmd, "", args)
	},
}

var webttyExecCmd = &cobra.Command{
	Use:          "exec [--] cmd...",
	Short:        "Execute a command through a WebTTY server",
	GroupID:      "webtty-connect",
	SilenceUsage: true,
	Args:         cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYClientWithOptions(cmd, "", args, webTTYClientRunOptions{DefaultOutput: "json", ForceNoInteractive: true, ForceNoTTY: true, Subcommand: "exec"})
	},
}

func init() {
	webttyCmd.Flags().SortFlags = false
	webttyCmd.PersistentFlags().SortFlags = false
	webttyCmd.AddGroup(&cobra.Group{ID: "webtty-connect", Title: "Connection Commands:"})
	webttyCmd.AddGroup(&cobra.Group{ID: "webtty-server", Title: "Server Commands:"})
	webttyCmd.AddGroup(&cobra.Group{ID: "webtty-managed-session", Title: "Managed Session Commands:"})
	rootCmd.AddCommand(webttyCmd)
}

func init() {
	webttyServerCmd.Flags().SortFlags = false
	webttyServerCmd.PersistentFlags().SortFlags = false
	webttyServerCmd.Flags().String("listen", "127.0.0.1:8080", "listen address (e.g. 127.0.0.1:8080 or 0.0.0.0:8080)")
	webttyServerCmd.Flags().Bool("rstream", false, "serve over an rstream tunnel")
	webttyServerCmd.MarkFlagsMutuallyExclusive("listen", "rstream")
	webttyServerCmd.Flags().String("name", "", "tunnel name when using --rstream")
	webttyServerCmd.Flags().String("server-id", "", "registered WebTTY server ID; implies --rstream and loads ~/.rstream/webtty/enrollments/<server-id>.yaml")
	webttyServerCmd.Flags().String("server-enrollment", "", "registered WebTTY server enrollment file; implies --rstream")
	webttyServerCmd.Flags().String("webtty-config", "", "WebTTY server runtime config file; may contain serverId or serverEnrollment")
	webttyServerCmd.Flags().Bool("publish", false, "publish the tunnel when using --rstream")
	webttyServerCmd.Flags().Bool("no-publish", false, "do not publish the tunnel when using --rstream")
	webttyServerCmd.MarkFlagsMutuallyExclusive("publish", "no-publish")
	webttyServerCmd.Flags().Bool("retry", false, "enable automatic reconnection on disconnect")
	webttyServerCmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	webttyServerCmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	webttyServerCmd.Flags().Int64("retry-interval", 5000, "retry interval in ms")
	webttyServerCmd.Flags().Int64("shutdown-timeout", 5000, "graceful shutdown timeout in ms")
	webttyServerCmd.Flags().String("auth-token-file", "", "read local WebTTY bearer token from file")
	webttyServerCmd.Flags().Bool("allow-unauthenticated", false, "allow unauthenticated local WebTTY access")
	webttyServerCmd.Flags().StringArray("allowed-origin", nil, "allow a browser Origin for local WebTTY WebSocket/WebTransport access (may be specified multiple times)")
	webttyServerCmd.Flags().String("execution-mode", "", "server execution mode (spawn, login); defaults to login for registered servers and spawn otherwise")
	webttyServerCmd.Flags().String("login-user", "", "default OS user for login execution mode")
	webttyServerCmd.Flags().Bool("allow-client-user", false, "allow clients to request an OS user in login execution mode")
	webttyServerCmd.Flags().String("transport", string(webtty.WebTTYTransportWebSocket), "WebTTY transport (plain, websocket, webtransport)")
	webttyServerCmd.Flags().String("tls-cert-file", "", "TLS certificate file for local plain TLS or WebTransport")
	webttyServerCmd.Flags().String("tls-key-file", "", "TLS private key file for local plain TLS or WebTransport")
	webttyServerCmd.Flags().Bool("e2e", false, "require end-to-end encrypted WebTTY terminal content")
	webttyServerCmd.Flags().String("identity", "", "named local WebTTY server identity")
	webttyServerCmd.Flags().String("identity-file", "", "local WebTTY server identity file")
	webttyServerCmd.Flags().StringArray("authorized-client-key", nil, "authorized WebTTY client signing key, as signing_key_id:signing_public_key")
	webttyServerCmd.Flags().String("authorized-clients-file", "", "authorized WebTTY client keys file")
	webttyServerCmd.Flags().StringArray("label", nil, "set WebTTY inventory labels (key=value, may be specified multiple times)")
	webttyServerCmd.Flags().String("fs-root", "", "serve a WebDAV filesystem sidecar rooted at this directory")
	webttyServerCmd.Flags().Bool("fs-read-only", false, "serve the WebDAV filesystem sidecar in read-only mode")
	webttyServerCmd.Flags().Int64("fs-max-upload-size", defaultWebTTYFSMaxUploadSize, "maximum WebDAV upload size in bytes")
	webttyCmd.AddCommand(webttyServerCmd)
}

func init() {
	webttyClientCmd.Flags().SortFlags = false
	webttyClientCmd.PersistentFlags().SortFlags = false
	addWebTTYClientFlags(webttyClientCmd, "text")
	webttyCmd.AddCommand(webttyClientCmd)
}

func init() {
	webttyExecCmd.Flags().SortFlags = false
	webttyExecCmd.PersistentFlags().SortFlags = false
	addWebTTYClientFlags(webttyExecCmd, "json")
	webttyCmd.AddCommand(webttyExecCmd)
}

func addWebTTYClientFlags(cmd *cobra.Command, outputDefault string) {
	cmd.Flags().String("url", "ws://127.0.0.1:8080", "WebTTY endpoint URL")
	cmd.Flags().String("transport", "", "WebTTY transport override (plain, websocket, webtransport)")
	cmd.Flags().BoolP("interactive", "i", false, "enable interactive mode")
	cmd.Flags().BoolP("no-interactive", "I", false, "disable interactive mode")
	cmd.MarkFlagsMutuallyExclusive("interactive", "no-interactive")
	cmd.Flags().BoolP("tty", "t", false, "enable TTY allocation")
	cmd.Flags().BoolP("no-tty", "T", false, "disable TTY allocation")
	cmd.MarkFlagsMutuallyExclusive("tty", "no-tty")
	cmd.Flags().StringArrayP("env", "e", nil, "pass environment variable (KEY or KEY=VALUE)")
	cmd.Flags().StringP("workdir", "w", "", "set the working directory")
	cmd.Flags().StringP("user", "u", "", "username or UID")
	cmd.Flags().StringP("output", "o", outputDefault, "output mode (text, json)")
	cmd.Flags().String("auth-token-file", "", "read local WebTTY bearer token from file")
	cmd.Flags().String("tls-ca-file", "", "PEM CA bundle used to verify WebTTY TLS")
	cmd.Flags().String("tls-server-name", "", "TLS server name used when verifying WebTTY TLS")
	cmd.Flags().Bool("tls-insecure-skip-verify", false, "skip WebTTY TLS certificate verification")
	cmd.Flags().String("exec-path", "", "advertised WebTTY exec path to append to the endpoint URL")
	cmd.Flags().Bool("e2e", false, "require end-to-end encrypted WebTTY terminal content")
	cmd.Flags().String("identity", "", "named local WebTTY client identity")
	cmd.Flags().String("identity-file", "", "local WebTTY client identity file")
	cmd.Flags().String("known-server", "", "local known WebTTY server name")
	cmd.Flags().StringArray("known-server-key", nil, "known WebTTY server key or endpoint identity")
	cmd.Flags().String("known-servers-file", "", "JSON file containing known WebTTY server endpoint identities")
}

func webttyServerUsesRstream(cmd *cobra.Command) bool {
	if registeredWebTTYServerRequested(cmd) {
		return true
	}
	useRstream, _ := cmd.Flags().GetBool("rstream")
	return useRstream
}

func applyWebTTYServerDerivedDefaults(cmd *cobra.Command) error {
	if !registeredWebTTYServerRequested(cmd) || flagChanged(cmd, "execution-mode") {
		return nil
	}
	return cmd.Flags().Set("execution-mode", string(webtty.WebTTYExecutionModeLogin))
}

func validateWebTTYServerFlags(cmd *cobra.Command) error {
	useRstream := webttyServerUsesRstream(cmd)
	transport, err := webTTYTransportFromFlag(cmd)
	if err != nil {
		return err
	}
	if _, err := webTTYServerEnrollmentPathFromFlags(cmd); err != nil {
		return err
	}
	if useRstream && cmd.Flags().Changed("listen") {
		return fmt.Errorf("--listen cannot be used with --rstream, --server-id, or --server-enrollment")
	}
	if useRstream && cmd.Flags().Changed("auth-token-file") {
		return fmt.Errorf("--auth-token-file is only used by local WebTTY WebSocket/WebTransport servers")
	}
	identityName, _ := cmd.Flags().GetString("identity")
	if strings.TrimSpace(identityName) != "" {
		if err := validateWebTTYServerID(identityName); err != nil {
			return fmt.Errorf("--identity contains unsupported characters")
		}
		if cmd.Flags().Changed("identity-file") {
			return fmt.Errorf("--identity cannot be combined with --identity-file")
		}
	}
	if !useRstream && (cmd.Flags().Changed("name") || cmd.Flags().Changed("publish") || cmd.Flags().Changed("no-publish")) {
		return fmt.Errorf("--name, --publish and --no-publish require --rstream")
	}
	fsRoot, _ := cmd.Flags().GetString("fs-root")
	if strings.TrimSpace(fsRoot) == "" && (cmd.Flags().Changed("fs-read-only") || cmd.Flags().Changed("fs-max-upload-size")) {
		return fmt.Errorf("--fs-read-only and --fs-max-upload-size require --fs-root")
	}
	fsMaxUploadSize, _ := cmd.Flags().GetInt64("fs-max-upload-size")
	if strings.TrimSpace(fsRoot) != "" && fsMaxUploadSize <= 0 {
		return fmt.Errorf("--fs-max-upload-size must be greater than zero")
	}
	if transport != webtty.WebTTYTransportWebSocket && strings.TrimSpace(fsRoot) != "" {
		return fmt.Errorf("--fs-root is only available with websocket WebTTY transport")
	}
	e2eActive, err := webTTYServerE2EActiveFromFlags(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(fsRoot) != "" && e2eActive {
		return fmt.Errorf("--fs-root cannot be used with WebTTY E2E payload encryption; the filesystem sidecar is a separate WebDAV surface")
	}
	if transport == webtty.WebTTYTransportWebTransport {
		certFile, _ := cmd.Flags().GetString("tls-cert-file")
		keyFile, _ := cmd.Flags().GetString("tls-key-file")
		if useRstream {
			if registeredWebTTYServerRequested(cmd) {
				if noPublishPtr := getBoolPtr(cmd, "no-publish"); noPublishPtr != nil && *noPublishPtr {
					return fmt.Errorf("registered WebTTY servers cannot use --transport=webtransport with --no-publish; use a published WebTTY endpoint so the engine can terminate WebTransport and record the managed session")
				}
			}
			if strings.TrimSpace(certFile) != "" || strings.TrimSpace(keyFile) != "" {
				return fmt.Errorf("--tls-cert-file and --tls-key-file are only used by local WebTransport servers")
			}
		} else if strings.TrimSpace(certFile) == "" || strings.TrimSpace(keyFile) == "" {
			return fmt.Errorf("--transport=webtransport requires --tls-cert-file and --tls-key-file")
		}
	} else if transport == webtty.WebTTYTransportPlain {
		certFile, _ := cmd.Flags().GetString("tls-cert-file")
		keyFile, _ := cmd.Flags().GetString("tls-key-file")
		if strings.TrimSpace(certFile) != "" || strings.TrimSpace(keyFile) != "" {
			if useRstream {
				return fmt.Errorf("plain WebTTY TLS server certificates are only supported for local servers")
			}
			if strings.TrimSpace(certFile) == "" || strings.TrimSpace(keyFile) == "" {
				return fmt.Errorf("--transport=plain requires both --tls-cert-file and --tls-key-file when TLS is enabled")
			}
		}
	} else if cmd.Flags().Changed("tls-cert-file") || cmd.Flags().Changed("tls-key-file") {
		return fmt.Errorf("--tls-cert-file and --tls-key-file require --transport=plain or --transport=webtransport")
	}
	if _, err := webTTYExecutionModeFromFlag(cmd); err != nil {
		return err
	}
	mode, _ := webTTYExecutionModeFromFlag(cmd)
	if mode != webtty.WebTTYExecutionModeLogin {
		if cmd.Flags().Changed("login-user") || cmd.Flags().Changed("allow-client-user") {
			return fmt.Errorf("--login-user and --allow-client-user require --execution-mode=login")
		}
	} else if registeredWebTTYServerRequested(cmd) {
		loginUser, _ := cmd.Flags().GetString("login-user")
		allowClientUser, _ := cmd.Flags().GetBool("allow-client-user")
		if strings.TrimSpace(loginUser) == "" && !allowClientUser {
			return fmt.Errorf("registered WebTTY servers default to login execution mode; set --login-user, --allow-client-user, or --execution-mode=spawn")
		}
	}
	return nil
}

func webTTYPlainServerTLSConfig(cmd *cobra.Command) (*tls.Config, error) {
	certFile, _ := cmd.Flags().GetString("tls-cert-file")
	keyFile, _ := cmd.Flags().GetString("tls-key-file")
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load plain WebTTY TLS certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
}

func webTTYExecutionModeFromFlag(cmd *cobra.Command) (webtty.ExecutionMode, error) {
	raw, _ := cmd.Flags().GetString("execution-mode")
	return webtty.ParseExecutionMode(raw)
}

func webTTYTransportFromFlag(cmd *cobra.Command) (webtty.WebTTYTransport, error) {
	raw, _ := cmd.Flags().GetString("transport")
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	switch raw {
	case string(webtty.WebTTYTransportPlain):
		return webtty.WebTTYTransportPlain, nil
	case string(webtty.WebTTYTransportWebSocket):
		return webtty.WebTTYTransportWebSocket, nil
	case string(webtty.WebTTYTransportWebTransport):
		return webtty.WebTTYTransportWebTransport, nil
	default:
		return "", fmt.Errorf("invalid --transport %q (valid: plain, websocket, webtransport)", raw)
	}
}

func webTTYServerE2EActiveFromFlags(cmd *cobra.Command) (bool, error) {
	e2eRequested, _ := cmd.Flags().GetBool("e2e")
	_, _, identityPathExplicit, err := webTTYE2EIdentityFilePath(cmd, nil)
	if err != nil {
		return false, err
	}
	identityName, _ := cmd.Flags().GetString("identity")
	return e2eRequested || identityPathExplicit || strings.TrimSpace(identityName) != "" || strings.TrimSpace(os.Getenv(webTTYIdentityEnv)) != "", nil
}

func readWebTTYAuthToken(cmd *cobra.Command) (*string, error) {
	if cmd.Flags().Changed("auth-token-file") {
		path, _ := cmd.Flags().GetString("auth-token-file")
		if strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("--auth-token-file is empty")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read --auth-token-file: %w", err)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return nil, fmt.Errorf("--auth-token-file is empty")
		}
		return &token, nil
	}
	if token := strings.TrimSpace(os.Getenv(webTTYAuthTokenEnv)); token != "" {
		return &token, nil
	}
	return nil, nil
}

func webTTYServerPayloadCryptoResolver(cmd *cobra.Command, enrollment *webTTYServerEnrollmentFile) (webTTYServerPayloadCryptoConfig, error) {
	e2eRequested, _ := cmd.Flags().GetBool("e2e")
	identityPath, _, identityPathExplicit, err := webTTYE2EIdentityFilePath(cmd, enrollment)
	if err != nil {
		return webTTYServerPayloadCryptoConfig{}, err
	}
	inlineIdentity, inlineIdentityConfigured, err := webTTYEndpointIdentityFromEnvironment()
	if err != nil {
		return webTTYServerPayloadCryptoConfig{}, err
	}
	if inlineIdentityConfigured && identityPathExplicit {
		return webTTYServerPayloadCryptoConfig{}, fmt.Errorf("%s cannot be combined with --identity, --identity-file, or %s", webTTYIdentityEnv, webTTYIdentityFileEnv)
	}
	keysConfigured := identityPathExplicit || inlineIdentityConfigured
	enrollmentRequiresE2E := webTTYServerEnrollmentRequiresE2E(enrollment)
	if !e2eRequested && !keysConfigured && !enrollmentRequiresE2E {
		return webTTYServerPayloadCryptoConfig{}, nil
	}
	var endpointIdentity *webtty.WebTTYEndpointIdentity
	if inlineIdentityConfigured {
		endpointIdentity = inlineIdentity
	} else {
		if strings.TrimSpace(identityPath) == "" {
			identityPath, err = webtty.DefaultE2EIdentityPath()
			if err != nil {
				return webTTYServerPayloadCryptoConfig{}, err
			}
		}
		endpointIdentity, err = webtty.LoadOrCreateWebTTYEndpointIdentityFile(identityPath)
		if err != nil {
			return webTTYServerPayloadCryptoConfig{}, fmt.Errorf("failed to load WebTTY endpoint identity: %w", err)
		}
	}
	if endpointIdentity == nil {
		return webTTYServerPayloadCryptoConfig{}, fmt.Errorf("WebTTY endpoint identity is not configured")
	}
	if err := validateWebTTYEndpointIdentityMatchesEnrollment(enrollment, endpointIdentity); err != nil {
		return webTTYServerPayloadCryptoConfig{}, err
	}
	hostKeyID := webtty.EncodeE2EKeyMaterial(endpointIdentity.Encryption.KeyID)
	return webTTYServerPayloadCryptoConfig{
		Resolver:         webtty.NewE2EServerPayloadCryptoResolver(endpointIdentity.Encryption),
		EndpointIdentity: endpointIdentity,
		HostKeyID:        hostKeyID,
	}, nil
}

func webTTYEndpointIdentityFromEnvironment() (*webtty.WebTTYEndpointIdentity, bool, error) {
	value := strings.TrimSpace(os.Getenv(webTTYIdentityEnv))
	if value == "" {
		return nil, false, nil
	}
	identity, err := webtty.DecodeWebTTYEndpointIdentityJSON([]byte(value))
	if err != nil {
		return nil, true, fmt.Errorf("%s must contain a WebTTY endpoint identity JSON document: %w", webTTYIdentityEnv, err)
	}
	return identity, true, nil
}

func webTTYClientEndpointIdentity(cmd *cobra.Command) (*webtty.WebTTYEndpointIdentity, error) {
	identity, configured, err := webTTYClientEndpointIdentityFromExplicitSources(cmd)
	if err != nil {
		return nil, err
	}
	if configured {
		return identity, nil
	}
	path, err := webtty.DefaultE2EIdentityPath()
	if err != nil {
		return nil, err
	}
	identity, err = webtty.LoadOrCreateWebTTYEndpointIdentityFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load WebTTY client endpoint identity: %w", err)
	}
	return identity, nil
}

func webTTYClientEndpointIdentityFromExplicitSources(cmd *cobra.Command) (*webtty.WebTTYEndpointIdentity, bool, error) {
	identityPath := ""
	identityPathExplicit := false
	var err error
	if cmd != nil {
		identityPath, _, identityPathExplicit, err = webTTYE2EIdentityFilePath(cmd, nil)
		if err != nil {
			return nil, false, err
		}
	} else if value := strings.TrimSpace(os.Getenv(webTTYIdentityFileEnv)); value != "" {
		identityPath, err = expandWebTTYPath(value)
		if err != nil {
			return nil, false, err
		}
		identityPathExplicit = true
	}
	inlineIdentity, inlineIdentityConfigured, err := webTTYEndpointIdentityFromEnvironment()
	if err != nil {
		return nil, false, err
	}
	if inlineIdentityConfigured && identityPathExplicit {
		return nil, false, fmt.Errorf("%s cannot be combined with --identity, --identity-file, or %s", webTTYIdentityEnv, webTTYIdentityFileEnv)
	}
	if inlineIdentityConfigured {
		return inlineIdentity, true, nil
	}
	if !identityPathExplicit {
		return nil, false, nil
	}
	identity, err := webtty.LoadOrCreateWebTTYEndpointIdentityFile(identityPath)
	if err != nil {
		return nil, true, fmt.Errorf("failed to load WebTTY client endpoint identity: %w", err)
	}
	return identity, true, nil
}

func webTTYClientEndpointIdentityForNameOrExplicitSources(cmd *cobra.Command, name string) (*webtty.WebTTYEndpointIdentity, error) {
	identity, configured, err := webTTYClientEndpointIdentityFromExplicitSources(cmd)
	if err != nil || configured {
		return identity, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	identity, err = webTTYClientEndpointIdentityByName(name)
	if err != nil {
		return nil, err
	}
	return identity, nil
}

func webTTYClientEndpointIdentityByName(name string) (*webtty.WebTTYEndpointIdentity, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		path, err := webtty.DefaultE2EIdentityPath()
		if err != nil {
			return nil, err
		}
		identity, err := webtty.LoadOrCreateWebTTYEndpointIdentityFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to load WebTTY client endpoint identity: %w", err)
		}
		return identity, nil
	}
	path, err := defaultNamedWebTTYIdentityPath(name)
	if err != nil {
		return nil, err
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load associated WebTTY client identity %q: %w", name, err)
	}
	return identity, nil
}

func webTTYAuthorizedClientSigningKeys(cmd *cobra.Command) (map[string][]byte, error) {
	values, _ := cmd.Flags().GetStringArray("authorized-client-key")
	if envValue := strings.TrimSpace(os.Getenv(webTTYAuthorizedClientKeysEnv)); envValue != "" {
		values = append(values, strings.Split(envValue, ",")...)
	}
	keys := map[string][]byte{}
	for _, raw := range values {
		keyID, publicKey, err := parseWebTTYAuthorizedClientSigningKey(raw)
		if err != nil {
			return nil, err
		}
		keys[string(keyID)] = publicKey
	}
	return keys, nil
}

func webTTYExplicitAuthorizedClientSourceConfigured(cmd *cobra.Command) bool {
	values, _ := cmd.Flags().GetStringArray("authorized-client-key")
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	if strings.TrimSpace(os.Getenv(webTTYAuthorizedClientKeysEnv)) != "" {
		return true
	}
	if value, changed := stringFlagValue(cmd, "authorized-clients-file"); changed && strings.TrimSpace(value) != "" {
		return true
	}
	return false
}

func webTTYAuthorizedClientSigningKeyResolver(cmd *cobra.Command, enrollment *webTTYServerEnrollmentFile, e2eActive bool) (webtty.AuthorizedClientSigningKeyResolver, error) {
	if !e2eActive {
		return nil, nil
	}
	if webTTYServerEnrollmentWorkspaceManaged(enrollment) {
		return nil, nil
	}
	path, err := webTTYAuthorizedClientsPathFromFlags(cmd, enrollment)
	if err != nil {
		return nil, err
	}
	return webtty.NewAuthorizedClientSigningKeyFileResolver(path), nil
}

func chainWebTTYAuthorizedClientSigningKeyResolvers(resolvers ...webtty.AuthorizedClientSigningKeyResolver) webtty.AuthorizedClientSigningKeyResolver {
	active := make([]webtty.AuthorizedClientSigningKeyResolver, 0, len(resolvers))
	for _, resolver := range resolvers {
		if resolver != nil {
			active = append(active, resolver)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(ctx context.Context, signingKeyID []byte) ([]byte, error) {
		for _, resolver := range active {
			publicKey, err := resolver(ctx, signingKeyID)
			if err != nil {
				return nil, err
			}
			if len(publicKey) != 0 {
				return publicKey, nil
			}
		}
		return nil, nil
	}
}

func webTTYAuthorizedClientsPathFromFlags(cmd *cobra.Command, enrollment *webTTYServerEnrollmentFile) (string, error) {
	if value, changed := stringFlagValue(cmd, "authorized-clients-file"); changed {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("--authorized-clients-file is empty")
		}
		return expandWebTTYPath(value)
	}
	if value := strings.TrimSpace(os.Getenv(webTTYAuthorizedClientsFileEnv)); value != "" {
		return expandWebTTYPath(value)
	}
	name, err := webTTYAuthorizedClientsStoreName(cmd, enrollment)
	if err != nil {
		return "", err
	}
	return webtty.DefaultAuthorizedClientKeysPath(name)
}

func webTTYAuthorizedClientsStoreName(cmd *cobra.Command, enrollment *webTTYServerEnrollmentFile) (string, error) {
	identityName, _ := cmd.Flags().GetString("identity")
	identityName = strings.TrimSpace(identityName)
	if identityName != "" {
		return identityName, nil
	}
	if flag := cmd.Flags().Lookup("server-id"); flag != nil {
		serverID, _ := cmd.Flags().GetString("server-id")
		if strings.TrimSpace(serverID) != "" {
			return strings.TrimSpace(serverID), nil
		}
	}
	if enrollment != nil && strings.TrimSpace(enrollment.ServerID) != "" {
		return strings.TrimSpace(enrollment.ServerID), nil
	}
	identityFile, _ := cmd.Flags().GetString("identity-file")
	if strings.TrimSpace(identityFile) != "" {
		return webTTYIdentityNameFromPath(identityFile), nil
	}
	if value := strings.TrimSpace(os.Getenv(webTTYIdentityFileEnv)); value != "" {
		return webTTYIdentityNameFromPath(value), nil
	}
	if value, ok := webTTYServerEnrollmentIdentityPath(enrollment); ok {
		return webTTYIdentityNameFromPath(value), nil
	}
	return "default", nil
}

func webTTYIdentityNameFromPath(pathValue string) string {
	base := filepath.Base(strings.TrimSpace(pathValue))
	base = strings.TrimSuffix(base, ".identity.json")
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if strings.TrimSpace(base) == "" {
		return "default"
	}
	return base
}

func parseWebTTYAuthorizedClientSigningKey(raw string) ([]byte, []byte, error) {
	return webtty.ParseAuthorizedClientSigningKey(raw)
}

func webTTYClientPayloadCrypto(cmd *cobra.Command) (*webtty.PayloadCrypto, error) {
	return webTTYClientPayloadCryptoWithRuntime(cmd.Context(), cmd, nil)
}

func webTTYClientPayloadCryptoWithRuntime(ctx context.Context, cmd *cobra.Command, runtimeE2E *webTTYClientRuntimeE2EContext) (*webtty.PayloadCrypto, error) {
	cryptoConfig, err := webTTYClientCryptoWithRuntimeAndScope(ctx, cmd, runtimeE2E, webTTYClientSecurityScope{})
	if err != nil {
		return nil, err
	}
	return cryptoConfig.PayloadCrypto, nil
}

func webTTYClientCryptoWithRuntime(ctx context.Context, cmd *cobra.Command, runtimeE2E *webTTYClientRuntimeE2EContext) (webTTYClientCryptoConfig, error) {
	return webTTYClientCryptoWithRuntimeAndScope(ctx, cmd, runtimeE2E, webTTYClientSecurityScope{})
}

func webTTYClientCryptoWithRuntimeAndScope(ctx context.Context, cmd *cobra.Command, runtimeE2E *webTTYClientRuntimeE2EContext, scope webTTYClientSecurityScope) (webTTYClientCryptoConfig, error) {
	e2eRequested, _ := cmd.Flags().GetBool("e2e")
	sources, serverKeysConfigured, err := webTTYKnownServerSourcesFromFlags(cmd)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	return webTTYClientCryptoFromSources(ctx, e2eRequested, sources, serverKeysConfigured, runtimeE2E, scope)
}

func webTTYClientPayloadCryptoForRuntime(ctx context.Context, runtimeE2E *webTTYClientRuntimeE2EContext) (*webtty.PayloadCrypto, error) {
	cryptoConfig, err := webTTYClientCryptoForRuntimeAndScope(ctx, runtimeE2E, webTTYClientSecurityScope{})
	if err != nil {
		return nil, err
	}
	return cryptoConfig.PayloadCrypto, nil
}

func webTTYClientCryptoForRuntime(ctx context.Context, runtimeE2E *webTTYClientRuntimeE2EContext) (webTTYClientCryptoConfig, error) {
	return webTTYClientCryptoForRuntimeAndScope(ctx, runtimeE2E, webTTYClientSecurityScope{})
}

func webTTYClientCryptoForRuntimeAndScope(ctx context.Context, runtimeE2E *webTTYClientRuntimeE2EContext, scope webTTYClientSecurityScope) (webTTYClientCryptoConfig, error) {
	sources, serverKeysConfigured, err := webTTYKnownServerSourcesFromEnvironment()
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	cryptoConfig, err := webTTYClientCryptoFromSources(ctx, false, sources, serverKeysConfigured, runtimeE2E, scope)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	return cryptoConfig, nil
}

func webTTYClientPayloadCryptoFromSources(ctx context.Context, e2eRequested bool, serverKeys []webtty.E2ERecipient, serverKeysConfigured bool, runtimeE2E *webTTYClientRuntimeE2EContext) (*webtty.PayloadCrypto, error) {
	sources := make([]webTTYKnownServerSource, 0, len(serverKeys))
	for _, serverKey := range serverKeys {
		sources = append(sources, webTTYKnownServerSource{Recipient: serverKey})
	}
	cryptoConfig, err := webTTYClientCryptoFromSources(ctx, e2eRequested, sources, serverKeysConfigured, runtimeE2E, webTTYClientSecurityScope{})
	if err != nil {
		return nil, err
	}
	return cryptoConfig.PayloadCrypto, nil
}

func webTTYClientCryptoFromSources(ctx context.Context, e2eRequested bool, sources []webTTYKnownServerSource, serverKeysConfigured bool, runtimeE2E *webTTYClientRuntimeE2EContext, scope webTTYClientSecurityScope) (webTTYClientCryptoConfig, error) {
	effectiveSources := append([]webTTYKnownServerSource(nil), sources...)
	var endpointIdentity *webtty.WebTTYEndpointIdentity
	var clientCredential []byte
	if runtimeE2E != nil {
		runtimeSources, runtimeEndpointIdentity, runtimeClientCredential, runtimeE2ERequired, err := webTTYClientRuntimeE2EServerSources(ctx, runtimeE2E)
		if err != nil {
			return webTTYClientCryptoConfig{}, err
		}
		if runtimeE2ERequired {
			scope.E2ERequired = true
		}
		if len(runtimeSources) > 0 {
			if runtimeSources[0].EndpointIdentity != nil {
				clientIdentity, err := webTTYRuntimeKnownServerClientIdentity(scope, *runtimeSources[0].EndpointIdentity)
				if err != nil {
					return webTTYClientCryptoConfig{}, err
				}
				runtimeSources[0].ClientIdentity = clientIdentity
			}
			endpointIdentity = runtimeEndpointIdentity
			clientCredential = runtimeClientCredential
			serverKeysConfigured = true
			effectiveSources = runtimeSources
		}
		if runtimeE2ERequired && len(runtimeSources) == 0 {
			return webTTYClientCryptoConfig{}, fmt.Errorf("persistent WebTTY server %s requires E2E, but no authenticated server identity was resolved", runtimeE2E.serverID)
		}
	}
	if !serverKeysConfigured && (scope.HostKeyID != "" || scope.E2ERequired) {
		defaultSources, err := webTTYDefaultKnownServerSourcesForScope(scope)
		if err != nil {
			return webTTYClientCryptoConfig{}, err
		}
		if len(defaultSources) > 0 {
			effectiveSources = defaultSources
			serverKeysConfigured = true
		}
	}
	if !serverKeysConfigured && e2eRequested {
		defaultSources, err := webTTYDefaultKnownServerSources()
		if err != nil {
			return webTTYClientCryptoConfig{}, err
		}
		if len(defaultSources) > 0 {
			effectiveSources = defaultSources
			serverKeysConfigured = true
		}
	}
	if !serverKeysConfigured && scope.E2ERequired {
		if strings.TrimSpace(scope.Target) != "" {
			return webTTYClientCryptoConfig{}, fmt.Errorf("WebTTY server %s requires E2E, but no matching known server identity was found; add it with %s", scope.Target, webTTYKnownServerAddCommand(scope.Target, scope.ClientProofRequired))
		}
		return webTTYClientCryptoConfig{}, fmt.Errorf("WebTTY server requires E2E, but no known server identity was found; add it with %s", webTTYKnownServerAddCommand("<server>", scope.ClientProofRequired))
	}
	if !e2eRequested && !serverKeysConfigured {
		return webTTYClientCryptoConfig{}, nil
	}
	serverKeys := make([]webtty.E2ERecipient, 0, len(effectiveSources))
	for _, source := range effectiveSources {
		var appendErr error
		serverKeys, appendErr = appendWebTTYE2EServerKey(serverKeys, source.Recipient)
		if appendErr != nil {
			return webTTYClientCryptoConfig{}, appendErr
		}
	}
	if len(serverKeys) == 0 {
		if scope.E2ERequired && scope.Target != "" {
			return webTTYClientCryptoConfig{}, fmt.Errorf("WebTTY server %s requires E2E, but no matching known server identity was found; add it with %s", scope.Target, webTTYKnownServerAddCommand(scope.Target, scope.ClientProofRequired))
		}
		return webTTYClientCryptoConfig{}, fmt.Errorf("E2E client mode requires --known-server-key, --known-servers-file, %s, %s, or %s", webTTYKnownServerKeyEnv, webTTYKnownServersFileEnv, mustDefaultKnownServerKeysPath())
	}
	expectedServerIdentity, err := webTTYExpectedServerIdentityFromKnownSources(effectiveSources)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	if scope.ClientProofRequired && expectedServerIdentity == nil {
		if strings.TrimSpace(scope.Target) != "" {
			return webTTYClientCryptoConfig{}, fmt.Errorf("WebTTY server %s requires authenticated E2E, but the known server key does not include a signing identity; use a WebTTY endpoint identity from rstream webtty identity show --name <server> --endpoint-identity", scope.Target)
		}
		return webTTYClientCryptoConfig{}, fmt.Errorf("WebTTY server requires authenticated E2E, but the known server key does not include a signing identity; use a WebTTY endpoint identity from rstream webtty identity show --name <server> --endpoint-identity")
	}
	clientIdentityName, err := webTTYClientIdentityNameFromKnownSources(effectiveSources)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	if endpointIdentity == nil && clientIdentityName != "" {
		endpointIdentity, err = webTTYClientEndpointIdentityByName(clientIdentityName)
		if err != nil {
			return webTTYClientCryptoConfig{}, fmt.Errorf("%w: %v", webTTYMissingClientEndpointIdentityError(scope, webTTYClientCryptoConfig{ClientIdentityName: clientIdentityName}), err)
		}
	}
	if endpointIdentity != nil && clientIdentityName != "" {
		serverKeys, err = appendWebTTYE2EClientEndpointRecipient(serverKeys, *endpointIdentity)
		if err != nil {
			return webTTYClientCryptoConfig{}, err
		}
	}
	payloadConfig := webtty.E2EPayloadCryptoConfig{
		Recipients: serverKeys,
	}
	if runtimeE2E != nil {
		payloadConfig.WorkspaceID = runtimeE2E.project.WorkspaceID
		payloadConfig.ProjectID = runtimeE2E.project.ID
		payloadConfig.ServerID = runtimeE2E.serverID
	}
	payloadCrypto, err := webtty.NewE2EClientPayloadCrypto(payloadConfig)
	if err != nil {
		return webTTYClientCryptoConfig{}, fmt.Errorf("failed to configure WebTTY E2E payload crypto: %w", err)
	}
	return webTTYClientCryptoConfig{
		PayloadCrypto:          payloadCrypto,
		EndpointIdentity:       endpointIdentity,
		ExpectedServerIdentity: expectedServerIdentity,
		ClientCredential:       clientCredential,
		ClientIdentityName:     clientIdentityName,
		E2ERequired:            scope.E2ERequired,
		ClientProofRequired:    scope.ClientProofRequired,
	}, nil
}

func webTTYClientRuntimeE2EServerSources(ctx context.Context, runtimeE2E *webTTYClientRuntimeE2EContext) ([]webTTYKnownServerSource, *webtty.WebTTYEndpointIdentity, []byte, bool, error) {
	workspaceID := strings.TrimSpace(runtimeE2E.project.WorkspaceID)
	proofs := []controlplane.WorkspaceDeviceAccessProof{}
	localDevices := []workspaceDeviceFile{}
	if workspaceID != "" {
		var err error
		proofItems, err := workspaceDeviceAccessProofsWithDevices(workspaceID, 8)
		if err != nil {
			return nil, nil, nil, false, err
		}
		proofs = make([]controlplane.WorkspaceDeviceAccessProof, 0, len(proofItems))
		localDevices = make([]workspaceDeviceFile, 0, len(proofItems))
		for _, item := range proofItems {
			proofs = append(proofs, item.proof)
			localDevices = append(localDevices, item.device)
		}
	}
	resolved, err := runtimeE2E.controlClient.ResolveWebTTYServerClient(ctx, runtimeE2E.project.ID, runtimeE2E.serverID, controlplane.ResolveWebTTYServerClientRequest{
		DeviceProofs: proofs,
	})
	if err != nil {
		if errors.Is(err, controlplane.ErrForbidden) && workspaceID != "" {
			return nil, nil, nil, false, fmt.Errorf("workspace-managed WebTTY E2E requires this machine to be a trusted workspace device; %s", workspaceDeviceEnrollmentHint(workspaceID))
		}
		return nil, nil, nil, false, mapControlPlaneError(err)
	}
	if !resolved.E2ERequired {
		return nil, nil, nil, false, nil
	}
	if resolved.ServerEndpointIdentity == nil || strings.TrimSpace(*resolved.ServerEndpointIdentity) == "" {
		return nil, nil, nil, true, fmt.Errorf("persistent WebTTY server %s requires E2E but has no authenticated server identity", runtimeE2E.serverID)
	}
	serverIdentity, err := webtty.ParseKnownServerEndpointIdentity(*resolved.ServerEndpointIdentity)
	if err != nil {
		return nil, nil, nil, true, fmt.Errorf("invalid persistent WebTTY server endpoint identity: %w", err)
	}
	serverKey := webtty.E2ERecipient{
		ID:        runtimeE2E.serverID,
		Kind:      webtty.E2ERecipientKindServer,
		KeyID:     append([]byte(nil), serverIdentity.EncryptionKeyID...),
		PublicKey: append([]byte(nil), serverIdentity.EncryptionPublicKey...),
	}
	sources := []webTTYKnownServerSource{{
		Recipient:        serverKey,
		EndpointIdentity: &serverIdentity,
	}}
	if resolved.EncryptionPolicy == webTTYServerEncryptionPolicyWorkspaceManaged && (resolved.CurrentDevice == nil || strings.TrimSpace(resolved.CurrentDevice.DeviceKeyID) == "") {
		return nil, nil, nil, true, fmt.Errorf("workspace-managed WebTTY E2E requires this machine to be a trusted workspace device; %s", workspaceDeviceEnrollmentHint(workspaceID))
	}
	var endpointIdentity *webtty.WebTTYEndpointIdentity
	var clientCredential []byte
	if resolved.CurrentDevice != nil && strings.TrimSpace(resolved.CurrentDevice.DeviceKeyID) != "" {
		deviceRecipient, deviceEndpointIdentity, deviceCredential, err := webTTYCurrentWorkspaceDeviceRecipient(resolved, localDevices)
		if err != nil {
			return nil, nil, nil, true, err
		}
		endpointIdentity = deviceEndpointIdentity
		clientCredential = deviceCredential
		sources = append(sources, webTTYKnownServerSource{Recipient: deviceRecipient})
	}
	return sources, endpointIdentity, clientCredential, true, nil
}

func webTTYCurrentWorkspaceDeviceRecipient(resolved controlplane.ResolveWebTTYServerClientResponse, devices []workspaceDeviceFile) (webtty.E2ERecipient, *webtty.WebTTYEndpointIdentity, []byte, error) {
	deviceKeyID := resolved.CurrentDevice.DeviceKeyID
	for _, device := range devices {
		if device.DeviceKeyID != deviceKeyID {
			continue
		}
		identity, err := loadWorkspaceDeviceWebTTYIdentity(device)
		if err != nil {
			return webtty.E2ERecipient{}, nil, nil, err
		}
		endpointIdentity, err := workspaceDeviceWebTTYEndpointIdentity(device)
		if err != nil {
			return webtty.E2ERecipient{}, nil, nil, err
		}
		credential, err := webTTYWorkspaceClientCredential(device, resolved)
		if err != nil {
			return webtty.E2ERecipient{}, nil, nil, err
		}
		return webtty.E2ERecipient{
			ID:        device.DeviceKeyID,
			Kind:      webtty.E2ERecipientKindWorkspaceDevice,
			KeyID:     append([]byte(nil), identity.KeyID...),
			PublicKey: append([]byte(nil), identity.PublicKey...),
		}, endpointIdentity, credential, nil
	}
	return webtty.E2ERecipient{}, nil, nil, fmt.Errorf("trusted workspace device %s was not found locally", deviceKeyID)
}

func webTTYClientRuntimeE2EContextFromServerInfo(ctx context.Context, runtime *resolvedRuntime, server webtty.ServerInfo) (*webTTYClientRuntimeE2EContext, error) {
	if !webTTYClientRstreamNeedsControlPlane(&server) {
		return nil, nil
	}
	serverID := trimOptionalString(server.ServerID)
	if serverID == "" {
		return nil, nil
	}
	if runtime == nil || runtime.Resolved.Context == nil || strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint) == "" {
		return nil, nil
	}
	controlClient := controlplane.NewClient(runtime.Resolved.APIURL, runtime.Resolved.Token)
	project, ok := webTTYProjectFromRuntimeServerInfo(runtime, &server)
	if !ok {
		var err error
		project, err = controlClient.ResolveProjectByEndpoint(ctx, runtime.Resolved.Context.ProjectEndpoint)
		if err != nil {
			return nil, mapControlPlaneError(err)
		}
	}
	return &webTTYClientRuntimeE2EContext{
		controlClient: controlClient,
		project:       project,
		serverID:      serverID,
	}, nil
}

func webTTYDefaultE2EServerKeys() ([]webtty.E2ERecipient, error) {
	sources, err := webTTYDefaultKnownServerSources()
	if err != nil {
		return nil, err
	}
	keys := make([]webtty.E2ERecipient, 0, len(sources))
	for _, source := range sources {
		var appendErr error
		keys, appendErr = appendWebTTYE2EServerKey(keys, source.Recipient)
		if appendErr != nil {
			return nil, appendErr
		}
	}
	return keys, nil
}

func webTTYDefaultKnownServerSources() ([]webTTYKnownServerSource, error) {
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		return nil, err
	}
	doc, err := webtty.ReadKnownServerKeysFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sources := make([]webTTYKnownServerSource, 0, len(doc.KnownServers))
	for i, entry := range doc.KnownServers {
		source, err := webTTYKnownServerSourceFromEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("decode known WebTTY server %d: %w", i, err)
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func webTTYDefaultKnownServerSourcesForScope(scope webTTYClientSecurityScope) ([]webTTYKnownServerSource, error) {
	sources, err := webTTYDefaultKnownServerSources()
	if err != nil {
		return nil, err
	}
	return webTTYFilterKnownServerSourcesForScope(sources, scope)
}

func webTTYRuntimeKnownServerClientIdentity(scope webTTYClientSecurityScope, serverIdentity webtty.WebTTYEndpointIdentityPublic) (string, error) {
	matches, err := webTTYKnownServerSourcesForScope(scope, &serverIdentity)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	return webTTYClientIdentityNameFromKnownSources(matches)
}

func webTTYKnownServerNameForScope(scope webTTYClientSecurityScope, expected *webtty.WebTTYEndpointIdentityPublic) (string, error) {
	matches, err := webTTYKnownServerSourcesForScope(scope, expected)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return strings.TrimSpace(scope.Target), nil
	}
	name := ""
	for _, match := range matches {
		next := strings.TrimSpace(match.Name)
		if next == "" {
			continue
		}
		if name != "" && name != next {
			return "", fmt.Errorf("multiple known WebTTY server entries match this target; pass --known-server <name> or update the target-specific entry with rstream webtty known-server set-identity <name> --identity <identity>")
		}
		name = next
	}
	if name != "" {
		return name, nil
	}
	return strings.TrimSpace(scope.Target), nil
}

func webTTYKnownServerSourcesForScope(scope webTTYClientSecurityScope, expected *webtty.WebTTYEndpointIdentityPublic) ([]webTTYKnownServerSource, error) {
	sources, err := webTTYDefaultKnownServerSources()
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, nil
	}
	matches, err := webTTYFilterKnownServerSourcesForScope(sources, scope)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 || expected == nil {
		return matches, nil
	}
	for _, source := range sources {
		if source.EndpointIdentity == nil {
			continue
		}
		if webTTYEndpointIdentitiesEqual(*source.EndpointIdentity, *expected) {
			matches = append(matches, source)
		}
	}
	return matches, nil
}

func webTTYFilterKnownServerSourcesForScope(sources []webTTYKnownServerSource, scope webTTYClientSecurityScope) ([]webTTYKnownServerSource, error) {
	target := strings.TrimSpace(scope.Target)
	hostKeyID := strings.TrimSpace(scope.HostKeyID)
	if target == "" && hostKeyID == "" {
		return sources, nil
	}
	targetMatches := make([]webTTYKnownServerSource, 0, len(sources))
	for _, source := range sources {
		if target != "" && strings.EqualFold(strings.TrimSpace(source.Name), target) {
			targetMatches = append(targetMatches, source)
		}
	}
	if hostKeyID != "" {
		hostMatches := make([]webTTYKnownServerSource, 0, len(sources))
		for _, source := range sources {
			if webtty.EncodeE2EKeyMaterial(source.Recipient.KeyID) == hostKeyID {
				hostMatches = append(hostMatches, source)
			}
		}
		if len(targetMatches) > 0 {
			filtered := make([]webTTYKnownServerSource, 0, len(targetMatches))
			for _, source := range targetMatches {
				if webtty.EncodeE2EKeyMaterial(source.Recipient.KeyID) == hostKeyID {
					filtered = append(filtered, source)
				}
			}
			if len(filtered) == 0 {
				return nil, fmt.Errorf("known WebTTY server %s does not match advertised host key %s", target, hostKeyID)
			}
			return filtered, nil
		}
		return hostMatches, nil
	}
	return targetMatches, nil
}

func webTTYEndpointIdentitiesEqual(first webtty.WebTTYEndpointIdentityPublic, second webtty.WebTTYEndpointIdentityPublic) bool {
	return bytes.Equal(first.EncryptionKeyID, second.EncryptionKeyID) &&
		bytes.Equal(first.EncryptionPublicKey, second.EncryptionPublicKey) &&
		bytes.Equal(first.SigningKeyID, second.SigningKeyID) &&
		bytes.Equal(first.SigningPublicKey, second.SigningPublicKey)
}

func webTTYClientIdentityNameFromKnownSources(sources []webTTYKnownServerSource) (string, error) {
	value := ""
	for _, source := range sources {
		clientIdentity := strings.TrimSpace(source.ClientIdentity)
		if clientIdentity == "" {
			continue
		}
		if value != "" && value != clientIdentity {
			return "", fmt.Errorf("multiple WebTTY client identities are associated with this known server")
		}
		value = clientIdentity
	}
	return value, nil
}

func webTTYMissingClientEndpointIdentityError(scope webTTYClientSecurityScope, cryptoConfig webTTYClientCryptoConfig) error {
	target := strings.TrimSpace(scope.Target)
	if target == "" {
		target = "this server"
	}
	if strings.TrimSpace(cryptoConfig.ClientIdentityName) != "" {
		return fmt.Errorf(
			"WebTTY server %s requires client endpoint identity %q, but it could not be loaded; create it with rstream webtty identity create --name %s or update the association with rstream webtty known-server set-identity %s --identity <identity>",
			target,
			cryptoConfig.ClientIdentityName,
			cryptoConfig.ClientIdentityName,
			target,
		)
	}
	if strings.TrimSpace(scope.Target) != "" {
		return fmt.Errorf("WebTTY server %s requires a client endpoint identity; create one with rstream webtty identity create --name <identity>, then trust the server and associate the identity with rstream webtty known-server add %s --key <server-endpoint-identity> --client-identity <identity>", target, target)
	}
	return fmt.Errorf("WebTTY server requires a client endpoint identity; pass --identity, --identity-file, %s, %s, or associate an identity with the known server", webTTYIdentityEnv, webTTYIdentityFileEnv)
}

func webTTYE2EServerKeysFromEnvironment() ([]webtty.E2ERecipient, bool, error) {
	sources, configured, err := webTTYKnownServerSourcesFromEnvironment()
	if err != nil {
		return nil, configured, err
	}
	keys := make([]webtty.E2ERecipient, 0, len(sources))
	for _, source := range sources {
		var appendErr error
		keys, appendErr = appendWebTTYE2EServerKey(keys, source.Recipient)
		if appendErr != nil {
			return nil, configured, appendErr
		}
	}
	return keys, configured, nil
}

func webTTYKnownServerSourcesFromEnvironment() ([]webTTYKnownServerSource, bool, error) {
	configured := false
	sources := []webTTYKnownServerSource{}
	if value := strings.TrimSpace(os.Getenv(webTTYKnownServerKeyEnv)); value != "" {
		configured = true
		source, err := parseWebTTYKnownServerSource(value)
		if err != nil {
			return nil, configured, fmt.Errorf("invalid %s: %w", webTTYKnownServerKeyEnv, err)
		}
		sources = append(sources, source)
	}
	if value := strings.TrimSpace(os.Getenv(webTTYKnownServersFileEnv)); value != "" {
		configured = true
		value, err := expandWebTTYPath(value)
		if err != nil {
			return nil, configured, err
		}
		doc, err := webtty.ReadKnownServerKeysFile(value)
		if err != nil {
			return nil, configured, err
		}
		for i, entry := range doc.KnownServers {
			source, err := webTTYKnownServerSourceFromEntry(entry)
			if err != nil {
				return nil, configured, fmt.Errorf("decode known WebTTY server %d: %w", i, err)
			}
			sources = append(sources, source)
		}
	}
	return sources, configured, nil
}

func mustDefaultKnownServerKeysPath() string {
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		return "~/.rstream/webtty/known_servers.json"
	}
	return path
}

func webTTYE2EIdentityFilePath(cmd *cobra.Command, enrollment *webTTYServerEnrollmentFile) (string, bool, bool, error) {
	identityName, _ := cmd.Flags().GetString("identity")
	identityName = strings.TrimSpace(identityName)
	pathValue, _ := cmd.Flags().GetString("identity-file")
	if strings.TrimSpace(pathValue) != "" {
		path, err := expandWebTTYPath(pathValue)
		return path, true, true, err
	}
	if identityName != "" {
		path, err := defaultNamedWebTTYIdentityPath(identityName)
		return path, true, true, err
	}
	if value := strings.TrimSpace(os.Getenv(webTTYIdentityFileEnv)); value != "" {
		path, err := expandWebTTYPath(value)
		return path, true, true, err
	}
	if value, ok := webTTYServerEnrollmentIdentityPath(enrollment); ok {
		path, err := expandWebTTYPath(value)
		return path, true, false, err
	}
	return "", false, false, nil
}

func defaultNamedWebTTYIdentityPath(name string) (string, error) {
	if err := validateWebTTYServerID(name); err != nil {
		return "", err
	}
	root, err := defaultRstreamHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "webtty", "identities", name+".identity.json"), nil
}

func webTTYE2EServerKeysFromFlags(cmd *cobra.Command) ([]webtty.E2ERecipient, bool, error) {
	sources, configured, err := webTTYKnownServerSourcesFromFlags(cmd)
	if err != nil {
		return nil, configured, err
	}
	serverKeys := make([]webtty.E2ERecipient, 0, len(sources))
	for _, source := range sources {
		var appendErr error
		serverKeys, appendErr = appendWebTTYE2EServerKey(serverKeys, source.Recipient)
		if appendErr != nil {
			return nil, configured, appendErr
		}
	}
	return serverKeys, configured, nil
}

func webTTYKnownServerSourcesFromFlags(cmd *cobra.Command) ([]webTTYKnownServerSource, bool, error) {
	configured := false
	sources := []webTTYKnownServerSource{}
	knownServerName, _ := cmd.Flags().GetString("known-server")
	knownServerName = strings.TrimSpace(knownServerName)
	if knownServerName != "" {
		if err := validateWebTTYServerID(knownServerName); err != nil {
			return nil, configured, fmt.Errorf("--known-server contains unsupported characters")
		}
	}
	rawKeys, _ := cmd.Flags().GetStringArray("known-server-key")
	if value := strings.TrimSpace(os.Getenv(webTTYKnownServerKeyEnv)); value != "" {
		rawKeys = append(rawKeys, value)
	}
	if len(rawKeys) > 0 {
		configured = true
	}
	if knownServerName != "" && len(rawKeys) > 0 {
		return nil, configured, fmt.Errorf("--known-server cannot be combined with --known-server-key or %s", webTTYKnownServerKeyEnv)
	}
	for _, raw := range rawKeys {
		source, err := parseWebTTYKnownServerSource(raw)
		if err != nil {
			return nil, configured, fmt.Errorf("invalid --known-server-key: %w", err)
		}
		sources = append(sources, source)
	}
	serverKeysFile, serverKeysFileSet, err := webTTYE2EServerKeysFilePath(cmd)
	if err != nil {
		return nil, configured, err
	}
	if knownServerName != "" {
		source, err := webTTYKnownServerSourceFromLocalStore(knownServerName, serverKeysFile, serverKeysFileSet)
		if err != nil {
			return nil, true, err
		}
		return []webTTYKnownServerSource{source}, true, nil
	}
	if serverKeysFileSet {
		configured = true
		doc, err := webtty.ReadKnownServerKeysFile(serverKeysFile)
		if err != nil {
			return nil, configured, err
		}
		for i, entry := range doc.KnownServers {
			source, err := webTTYKnownServerSourceFromEntry(entry)
			if err != nil {
				return nil, configured, fmt.Errorf("decode known WebTTY server %d: %w", i, err)
			}
			sources = append(sources, source)
		}
	}
	return sources, configured, nil
}

func webTTYKnownServerSourceFromLocalStore(name string, path string, pathSet bool) (webTTYKnownServerSource, error) {
	name = strings.TrimSpace(name)
	if err := validateWebTTYServerID(name); err != nil {
		return webTTYKnownServerSource{}, fmt.Errorf("known server name contains unsupported characters")
	}
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = webtty.DefaultKnownServerKeysPath()
		if err != nil {
			return webTTYKnownServerSource{}, err
		}
	}
	doc, err := readKnownServersDocument(path)
	if err != nil {
		return webTTYKnownServerSource{}, err
	}
	for _, entry := range doc.KnownServers {
		if strings.TrimSpace(entry.Name) != name {
			continue
		}
		source, err := webTTYKnownServerSourceFromEntry(entry)
		if err != nil {
			return webTTYKnownServerSource{}, fmt.Errorf("decode known WebTTY server %q: %w", name, err)
		}
		return source, nil
	}
	if pathSet {
		return webTTYKnownServerSource{}, fmt.Errorf("known WebTTY server %q was not found in %s; add it with rstream webtty known-server add %s --key <server-endpoint-identity> --known-servers-file %s", name, path, name, path)
	}
	return webTTYKnownServerSource{}, fmt.Errorf("known WebTTY server %q was not found; add it with rstream webtty known-server add %s --key <server-endpoint-identity>", name, name)
}

func webTTYKnownServerAddCommand(target string, clientProofRequired bool) string {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "<server>"
	}
	command := "rstream webtty known-server add " + target + " --key <server-endpoint-identity>"
	if clientProofRequired {
		command += " --client-identity <identity>"
	}
	return command
}

func parseWebTTYKnownServerSource(raw string) (webTTYKnownServerSource, error) {
	value := strings.TrimSpace(raw)
	if strings.Count(value, ":") == 3 {
		identity, err := webtty.ParseKnownServerEndpointIdentity(value)
		if err != nil {
			return webTTYKnownServerSource{}, err
		}
		return webTTYKnownServerSource{
			Recipient: webtty.E2ERecipient{
				KeyID:     append([]byte(nil), identity.EncryptionKeyID...),
				PublicKey: append([]byte(nil), identity.EncryptionPublicKey...),
			},
			EndpointIdentity: &identity,
		}, nil
	}
	recipient, err := webtty.ParseKnownServerKey(value)
	if err != nil {
		return webTTYKnownServerSource{}, err
	}
	return webTTYKnownServerSource{Recipient: recipient}, nil
}

func webTTYKnownServerSourceFromEntry(entry webtty.KnownServerKeyEntry) (webTTYKnownServerSource, error) {
	sourceName := strings.TrimSpace(entry.Name)
	clientIdentity := strings.TrimSpace(entry.ClientIdentity)
	if strings.TrimSpace(entry.SigningKeyID) != "" || strings.TrimSpace(entry.SigningPublicKey) != "" {
		identity, err := webtty.ParseKnownServerEndpointIdentity(strings.Join([]string{
			entry.KeyID,
			entry.PublicKey,
			entry.SigningKeyID,
			entry.SigningPublicKey,
		}, ":"))
		if err != nil {
			return webTTYKnownServerSource{}, err
		}
		return webTTYKnownServerSource{
			Recipient: webtty.E2ERecipient{
				ID:        sourceName,
				Kind:      webtty.E2ERecipientKindServer,
				KeyID:     append([]byte(nil), identity.EncryptionKeyID...),
				PublicKey: append([]byte(nil), identity.EncryptionPublicKey...),
			},
			EndpointIdentity: &identity,
			Name:             sourceName,
			ClientIdentity:   clientIdentity,
		}, nil
	}
	recipient, err := webtty.ParseKnownServerKey(entry.KeyID + ":" + entry.PublicKey)
	if err != nil {
		return webTTYKnownServerSource{}, err
	}
	recipient.ID = sourceName
	recipient.Kind = webtty.E2ERecipientKindServer
	return webTTYKnownServerSource{Recipient: recipient, Name: sourceName, ClientIdentity: clientIdentity}, nil
}

func webTTYExpectedServerIdentityFromKnownSources(sources []webTTYKnownServerSource) (*webtty.WebTTYEndpointIdentityPublic, error) {
	var expected *webtty.WebTTYEndpointIdentityPublic
	for _, source := range sources {
		if source.EndpointIdentity == nil {
			continue
		}
		if expected != nil {
			return nil, fmt.Errorf("multiple known WebTTY server endpoint identities are configured; pass exactly one endpoint identity for authenticated E2E")
		}
		identity := *source.EndpointIdentity
		expected = &identity
	}
	return expected, nil
}

func webTTYE2EServerKeysFilePath(cmd *cobra.Command) (string, bool, error) {
	pathValue, _ := cmd.Flags().GetString("known-servers-file")
	if strings.TrimSpace(pathValue) != "" {
		path, err := expandWebTTYPath(pathValue)
		return path, true, err
	}
	if value := strings.TrimSpace(os.Getenv(webTTYKnownServersFileEnv)); value != "" {
		path, err := expandWebTTYPath(value)
		return path, true, err
	}
	return "", false, nil
}

func appendWebTTYE2EServerKey(serverKeys []webtty.E2ERecipient, next webtty.E2ERecipient) ([]webtty.E2ERecipient, error) {
	return appendWebTTYE2ERecipient(serverKeys, next, "known WebTTY server")
}

func appendWebTTYE2EClientEndpointRecipient(recipients []webtty.E2ERecipient, identity webtty.WebTTYEndpointIdentity) ([]webtty.E2ERecipient, error) {
	return appendWebTTYE2ERecipient(recipients, webtty.E2ERecipient{
		ID:        webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		Kind:      webtty.E2ERecipientKindPublicKey,
		KeyID:     append([]byte(nil), identity.Encryption.KeyID...),
		PublicKey: append([]byte(nil), identity.Encryption.PublicKey...),
	}, "WebTTY client identity")
}

func appendWebTTYE2ERecipient(serverKeys []webtty.E2ERecipient, next webtty.E2ERecipient, label string) ([]webtty.E2ERecipient, error) {
	for _, existing := range serverKeys {
		if !bytes.Equal(existing.KeyID, next.KeyID) {
			continue
		}
		if !bytes.Equal(existing.PublicKey, next.PublicKey) {
			return nil, fmt.Errorf("conflicting %s public keys for key id %s", label, webtty.EncodeE2EKeyMaterial(next.KeyID))
		}
		return serverKeys, nil
	}
	return append(serverKeys, next), nil
}

func webTTYClientTLSConfig(cmd *cobra.Command) (*tls.Config, error) {
	caFile, _ := cmd.Flags().GetString("tls-ca-file")
	serverName, _ := cmd.Flags().GetString("tls-server-name")
	insecure, _ := cmd.Flags().GetBool("tls-insecure-skip-verify")
	caFile = strings.TrimSpace(caFile)
	serverName = strings.TrimSpace(serverName)
	if caFile == "" && serverName == "" && !insecure {
		return nil, nil
	}
	if caFile != "" && insecure {
		return nil, fmt.Errorf("--tls-ca-file cannot be combined with --tls-insecure-skip-verify")
	}
	cfg := &tls.Config{
		InsecureSkipVerify: insecure,
		MinVersion:         tls.VersionTLS13,
		ServerName:         serverName,
	}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read WebTTY TLS CA file: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("WebTTY TLS CA file %s does not contain any PEM certificates", caFile)
		}
		cfg.RootCAs = roots
	}
	return cfg, nil
}

func webTTYServerAllowedOrigins(cmd *cobra.Command, useRstream bool) ([]string, error) {
	origins, err := cmd.Flags().GetStringArray("allowed-origin")
	if err != nil {
		return nil, err
	}
	cleaned := make([]string, 0, len(origins)+1)
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		cleaned = append(cleaned, origin)
	}
	if useRstream {
		cleaned = append(cleaned, "*")
	}
	return cleaned, nil
}

func newWebTTYServerHTTPHandler(cmd *cobra.Command, terminalHandler *webtty.Handler, authToken *string, allowUnauthenticated bool, logger *slog.Logger) (http.Handler, error) {
	fsRoot, _ := cmd.Flags().GetString("fs-root")
	if strings.TrimSpace(fsRoot) == "" {
		return terminalHandler, nil
	}
	fsReadOnly, _ := cmd.Flags().GetBool("fs-read-only")
	fsMaxUploadSize, _ := cmd.Flags().GetInt64("fs-max-upload-size")
	fsHandler, err := webtty.NewFileSystemHandler(&webtty.FileSystemConfig{Root: fsRoot, ReadOnly: fsReadOnly, MaxUploadSize: &fsMaxUploadSize, Logger: logger})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(webtty.WebTTYDefaultFSPath, webtty.NewBearerAuthHandler(fsHandler, authToken, allowUnauthenticated))
	mux.Handle(webtty.WebTTYDefaultFSPath+"/", webtty.NewBearerAuthHandler(fsHandler, authToken, allowUnauthenticated))
	mux.Handle("/", terminalHandler)
	return mux, nil
}

func newWebTTYServerTunnelProperties(cmd *cobra.Command, enrollment *webTTYServerEnrollmentFile) rstream.TunnelProperties {
	publish := true
	if noPublishPtr := getBoolPtr(cmd, "no-publish"); noPublishPtr != nil && *noPublishPtr {
		publish = false
	}
	transport, _ := webTTYTransportFromFlag(cmd)
	name := getStringPtr(cmd, "name")
	if name == nil && enrollment != nil && strings.TrimSpace(enrollment.ServerID) != "" {
		name = rstream.StringPtr(enrollment.ServerID)
	}
	props := rstream.TunnelProperties{
		Name:    name,
		Publish: rstream.BoolPtr(publish),
		Labels:  webtty.DefaultLabels(),
	}
	applyWebTTYServerLabels(cmd, props.Labels)
	webTTYServerRegisteredLabels(enrollment, props.Labels)
	if enrollment != nil {
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolWebTTY)
		if transport == webtty.WebTTYTransportWebTransport {
			props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
		} else {
			props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
		}
	} else if transport == webtty.WebTTYTransportWebTransport {
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
	}
	if publish {
		if enrollment == nil {
			props.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
			if transport == webtty.WebTTYTransportWebTransport {
				props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
			} else {
				props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP1_1)
			}
		}
		props.TokenAuth = rstream.BoolPtr(true)
	}
	return props
}

func applyWebTTYServerLabels(cmd *cobra.Command, labels map[string]string) {
	if mode, err := webTTYExecutionModeFromFlag(cmd); err == nil {
		labels[webtty.WebTTYExecutionModeLabelKey] = string(mode)
	}
	for key, value := range getStringArrayMap(cmd, "label") {
		labels[webtty.WebTTYCustomLabelPrefix+key] = value
	}
	fsRoot, _ := cmd.Flags().GetString("fs-root")
	if strings.TrimSpace(fsRoot) == "" {
		return
	}
	fsReadOnly, _ := cmd.Flags().GetBool("fs-read-only")
	fsMode := webtty.WebTTYFSModeReadWrite
	if fsReadOnly {
		fsMode = webtty.WebTTYFSModeReadOnly
	}
	labels[webtty.WebTTYCapabilitiesLabelKey] = webtty.WebTTYCapabilityExec + "," + webtty.WebTTYCapabilityFS
	labels[webtty.WebTTYFSModeLabelKey] = fsMode
	labels[webtty.WebTTYFSPathLabelKey] = webtty.WebTTYDefaultFSPath
}

func applyWebTTYRuntimeSecurityLabels(labels map[string]string, cryptoConfig webTTYServerPayloadCryptoConfig, enrollment *webTTYServerEnrollmentFile, requireClientProof bool, hostKeyID string) {
	if labels == nil {
		return
	}
	if cryptoConfig.EndpointIdentity != nil && strings.TrimSpace(hostKeyID) != "" {
		labels[webtty.WebTTYHostKeyIDLabelKey] = hostKeyID
	}
	if cryptoConfig.EndpointIdentity != nil {
		labels[webtty.WebTTYE2ELabelKey] = webtty.WebTTYE2ERequired
	} else {
		labels[webtty.WebTTYE2ELabelKey] = webtty.WebTTYE2EDisabled
	}
	if requireClientProof {
		labels[webtty.WebTTYClientProofLabelKey] = webtty.WebTTYClientProofRequired
	} else {
		labels[webtty.WebTTYClientProofLabelKey] = webtty.WebTTYClientProofNone
	}
	if strings.TrimSpace(labels[webtty.WebTTYEncryptionPolicyLabelKey]) != "" {
		return
	}
	if enrollment != nil && strings.TrimSpace(enrollment.EncryptionPolicy) != "" {
		labels[webtty.WebTTYEncryptionPolicyLabelKey] = strings.TrimSpace(enrollment.EncryptionPolicy)
		return
	}
	if cryptoConfig.EndpointIdentity != nil {
		labels[webtty.WebTTYEncryptionPolicyLabelKey] = webTTYServerEncryptionPolicyExplicitKey
		return
	}
	labels[webtty.WebTTYEncryptionPolicyLabelKey] = webTTYServerEncryptionPolicyDisabled
}

func applyWebTTYServerAdmissionLabel(props *rstream.TunnelProperties, enrollment *webTTYServerEnrollmentFile) error {
	if props == nil || enrollment == nil {
		return nil
	}
	if props.Protocol == nil || string(*props.Protocol) != string(rstream.ProtocolWebTTY) {
		return nil
	}
	if props.Labels == nil {
		props.Labels = map[string]string{}
	}
	identityPath := strings.TrimSpace(enrollment.IdentityFile)
	if identityPath == "" {
		return fmt.Errorf("registered WebTTY server enrollment is missing its identity file")
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityPath)
	if err != nil {
		return fmt.Errorf("failed to load registered WebTTY server identity: %w", err)
	}
	if err := validateWebTTYEndpointIdentityMatchesEnrollment(enrollment, identity); err != nil {
		return err
	}
	tunnelType := string(rstream.TunnelTypeBytestream)
	if props.Type != nil {
		tunnelType = string(*props.Type)
	}
	label, err := webtty.NewWebTTYServerAdmissionProofLabel(identity, webtty.WebTTYServerAdmissionProofParams{
		WorkspaceID:    enrollment.WorkspaceID,
		ProjectID:      enrollment.ProjectID,
		ServerID:       enrollment.ServerID,
		TunnelProtocol: string(rstream.ProtocolWebTTY),
		TunnelType:     tunnelType,
		Labels:         props.Labels,
	})
	if err != nil {
		return fmt.Errorf("failed to sign registered WebTTY server admission: %w", err)
	}
	props.Labels[webtty.WebTTYServerAdmissionLabelKey] = label
	return nil
}

func webttyClientUsesRstream(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(strings.ToLower(raw), "rstrm://")
}

func resolveWebTTYExecURL(raw string, execPath string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.TrimSpace(execPath) == "" {
		return raw, nil
	}
	endpoint, err := normalizeWebTTYEndpointPath(execPath)
	if err != nil {
		return "", err
	}
	if !strings.Contains(raw, "://") {
		raw = "ws://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid WebTTY exec URL: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "ws", "wss", "rstrm":
	default:
		return "", fmt.Errorf("unsupported WebTTY exec URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("WebTTY exec URL is missing host")
	}
	u.Path = webTTYEndpointPath(u.Path, endpoint)
	u.Fragment = ""
	return u.String(), nil
}
func normalizeWebTTYEndpointPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return "/", nil
	}
	if strings.ContainsAny(value, "?#") {
		return "", fmt.Errorf("WebTTY endpoint path must not include query or fragment")
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return path.Clean(value), nil
}
func webTTYEndpointPath(current string, endpoint string) string {
	base := strings.TrimRight(current, "/")
	if endpoint == "/" {
		if base == "" {
			return "/"
		}
		return base
	}
	if base == "" || base == "/" {
		return endpoint
	}
	if base == endpoint || strings.HasPrefix(base, endpoint+"/") {
		return base
	}
	return path.Join(base, endpoint)
}

func newWebTTYClientDialContext(client *rstream.Client) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		target, err := extractWebTTYTunnelTarget(addr)
		if err != nil {
			return nil, err
		}
		return client.Dial(ctx, rstream.Addr{IdOrName: target})
	}
}

func newWebTTYClientPacketDialContext(client *rstream.Client) func(context.Context, string) (net.PacketConn, net.Addr, error) {
	return func(ctx context.Context, addr string) (net.PacketConn, net.Addr, error) {
		target, err := extractWebTTYTunnelTarget(addr)
		if err != nil {
			return nil, nil, err
		}
		remote := rstream.Addr{IdOrName: target}
		pc, err := client.PacketDial(ctx, remote)
		if err != nil {
			return nil, nil, err
		}
		return pc, &remote, nil
	}
}

func extractWebTTYTunnelTarget(addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.Contains(addr, ":") {
			return "", fmt.Errorf("failed to extract tunnel id or name from address %q: %w", addr, err)
		}
		host = addr
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("websocket target is missing tunnel id or name")
	}
	return host, nil
}

func webTTYClientRstreamTarget(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "rstrm" {
		return "", fmt.Errorf("WebTTY URL is not an rstream URL")
	}
	target := strings.TrimSpace(u.Host)
	if target == "" {
		return "", fmt.Errorf("rstream WebTTY URL is missing a tunnel target")
	}
	return target, nil
}

func resolveWebTTYClientRstream(ctx context.Context, runtime *resolvedRuntime, rstreamClient *rstream.Client, urlValue string) (webTTYClientRstreamResolution, error) {
	target, err := webTTYClientRstreamTarget(urlValue)
	if err != nil {
		return webTTYClientRstreamResolution{}, err
	}
	serverInfo, err := resolveWebTTYRuntimeServerInfo(ctx, rstreamClient, target)
	if err != nil {
		return webTTYClientRstreamResolution{}, err
	}
	scope := webTTYClientSecurityScopeFromServerInfo(target, serverInfo)
	serverID := webTTYRuntimeServerID(serverInfo)
	if serverInfo != nil {
		urlValue, err = webTTYURLWithRstreamTarget(urlValue, webTTYRuntimeDialTarget(serverInfo))
		if err != nil {
			return webTTYClientRstreamResolution{}, err
		}
	}
	if serverInfo != nil && !webTTYClientRstreamNeedsControlPlane(serverInfo) {
		return webTTYClientRstreamResolution{URL: urlValue, Scope: scope}, nil
	}
	if runtime == nil || runtime.Resolved.Context == nil || strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint) == "" {
		return webTTYClientRstreamResolution{URL: urlValue, Scope: scope}, nil
	}
	controlClient := controlplane.NewClient(runtime.Resolved.APIURL, runtime.Resolved.Token)
	project, ok := webTTYProjectFromRuntimeServerInfo(runtime, serverInfo)
	if !ok {
		var err error
		project, err = controlClient.ResolveProjectByEndpoint(ctx, runtime.Resolved.Context.ProjectEndpoint)
		if err != nil {
			return webTTYClientRstreamResolution{}, mapControlPlaneError(err)
		}
	}
	if serverID == "" {
		server, err := resolveControlPlaneWebTTYServerTarget(ctx, controlClient, project.ID, target)
		if err != nil {
			return webTTYClientRstreamResolution{}, mapControlPlaneError(err)
		}
		if server == nil {
			return webTTYClientRstreamResolution{URL: urlValue, Scope: scope}, nil
		}
		serverID = strings.TrimSpace(server.ID)
		urlValue, err = webTTYURLWithRstreamTarget(urlValue, serverID)
		if err != nil {
			return webTTYClientRstreamResolution{}, err
		}
		serverInfo, err = resolveWebTTYRuntimeServerInfo(ctx, rstreamClient, serverID)
		if err != nil {
			return webTTYClientRstreamResolution{}, err
		}
		if serverInfo == nil {
			return webTTYClientRstreamResolution{}, fmt.Errorf("persistent WebTTY server %q is registered but is not online", target)
		}
		scope = webTTYClientSecurityScopeFromServerInfo(serverID, serverInfo)
		if strings.TrimSpace(target) != "" {
			scope.Target = strings.TrimSpace(target)
		}
		if !webTTYClientRstreamNeedsControlPlane(serverInfo) {
			return webTTYClientRstreamResolution{URL: urlValue, Scope: scope}, nil
		}
	}
	if serverID == "" {
		return webTTYClientRstreamResolution{URL: urlValue, Scope: scope}, nil
	}
	return webTTYClientRstreamResolution{
		URL: urlValue,
		RuntimeE2E: &webTTYClientRuntimeE2EContext{
			controlClient: controlClient,
			project:       project,
			serverID:      serverID,
		},
		Scope: scope,
	}, nil
}

func webTTYProjectFromRuntimeServerInfo(runtime *resolvedRuntime, serverInfo *webtty.ServerInfo) (controlplane.Project, bool) {
	if serverInfo == nil {
		return controlplane.Project{}, false
	}
	projectID := strings.TrimSpace(trimOptionalString(serverInfo.ProjectID))
	workspaceID := strings.TrimSpace(trimOptionalString(serverInfo.WorkspaceID))
	if projectID == "" || workspaceID == "" {
		return controlplane.Project{}, false
	}
	project := controlplane.Project{
		ID:          projectID,
		WorkspaceID: workspaceID,
	}
	if runtime != nil && runtime.Resolved.Context != nil {
		project.Endpoint = strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint)
	}
	return project, true
}

func webTTYRuntimeServerID(serverInfo *webtty.ServerInfo) string {
	if serverInfo == nil || serverInfo.ServerID == nil {
		return ""
	}
	return strings.TrimSpace(*serverInfo.ServerID)
}

func webTTYRuntimeDialTarget(serverInfo *webtty.ServerInfo) string {
	if serverID := webTTYRuntimeServerID(serverInfo); serverID != "" {
		return serverID
	}
	if serverInfo == nil {
		return ""
	}
	return strings.TrimSpace(serverInfo.Target)
}

func webTTYClientRstreamNeedsControlPlane(serverInfo *webtty.ServerInfo) bool {
	if serverInfo == nil {
		return true
	}
	serverID := webTTYRuntimeServerID(serverInfo)
	if serverID == "" {
		return false
	}
	policy := strings.TrimSpace(trimOptionalString(serverInfo.EncryptionPolicy))
	switch policy {
	case webTTYServerEncryptionPolicyDisabled, webTTYServerEncryptionPolicyExplicitKey:
		return false
	case webTTYServerEncryptionPolicyWorkspaceManaged:
		return true
	default:
		return true
	}
}

func webTTYClientSecurityScopeFromServerInfo(target string, serverInfo *webtty.ServerInfo) webTTYClientSecurityScope {
	scope := webTTYClientSecurityScope{Target: strings.TrimSpace(target)}
	if serverInfo == nil {
		return scope
	}
	if strings.TrimSpace(serverInfo.Target) != "" {
		scope.Target = strings.TrimSpace(serverInfo.Target)
	}
	if serverInfo.HostKeyID != nil {
		scope.HostKeyID = strings.TrimSpace(*serverInfo.HostKeyID)
	}
	if serverInfo.E2E != nil && strings.EqualFold(strings.TrimSpace(*serverInfo.E2E), webtty.WebTTYE2ERequired) {
		scope.E2ERequired = true
	}
	if scope.HostKeyID != "" {
		scope.E2ERequired = true
	}
	if serverInfo.ClientProof != nil && strings.EqualFold(strings.TrimSpace(*serverInfo.ClientProof), webtty.WebTTYClientProofRequired) {
		scope.ClientProofRequired = true
		scope.E2ERequired = true
	}
	return scope
}

func webTTYClientRuntimeE2EContextFromRstream(ctx context.Context, runtime *resolvedRuntime, rstreamClient *rstream.Client, urlValue string) (*webTTYClientRuntimeE2EContext, error) {
	resolved, err := resolveWebTTYClientRstream(ctx, runtime, rstreamClient, urlValue)
	if err != nil {
		return nil, err
	}
	return resolved.RuntimeE2E, nil
}

func resolveControlPlaneWebTTYServerTarget(ctx context.Context, client *controlplane.Client, projectID string, target string) (*controlplane.WebTTYServer, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil
	}
	pageSize := 100
	servers, err := client.ListWebTTYServers(ctx, projectID, controlplane.ListWebTTYServersParams{
		Query:    target,
		PageSize: &pageSize,
	})
	if err != nil {
		return nil, err
	}
	var match *controlplane.WebTTYServer
	for i := range servers.Servers {
		server := &servers.Servers[i]
		if server.ID != target && server.Name != target {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple persistent WebTTY servers match %q; use the server id", target)
		}
		match = server
	}
	return match, nil
}

func webTTYURLWithRstreamTarget(raw string, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("rstream WebTTY URL target is empty")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "rstrm" {
		return "", fmt.Errorf("WebTTY URL is not an rstream URL")
	}
	u.Host = target
	return u.String(), nil
}

func webTTYURLWithManagedSessionMode(raw string, interactive bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "rstrm" {
		return raw, nil
	}
	query := u.Query()
	if strings.TrimSpace(query.Get(webTTYManagedSessionModeQuery)) != "" {
		return u.String(), nil
	}
	mode := webTTYManagedSessionModeNonInteractive
	if interactive {
		mode = webTTYManagedSessionModeInteractive
	}
	query.Set(webTTYManagedSessionModeQuery, mode)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func resolveWebTTYRuntimeServerInfo(ctx context.Context, client *rstream.Client, target string) (*webtty.ServerInfo, error) {
	servers, err := listWebTTYServers(ctx, client, "")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve WebTTY server metadata: %w", err)
	}
	var match *webtty.ServerInfo
	for i := range servers {
		server := &servers[i]
		if !webTTYRuntimeServerMatchesTarget(*server, target) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple WebTTY servers match %q; use the tunnel id or a unique name", target)
		}
		match = server
	}
	return match, nil
}

func webTTYRuntimeServerMatchesTarget(server webtty.ServerInfo, target string) bool {
	target = strings.TrimSpace(target)
	return target != "" &&
		(server.Target == target ||
			server.TunnelID == target ||
			trimOptionalString(server.TunnelName) == target ||
			trimOptionalString(server.ServerID) == target ||
			trimOptionalString(server.ServerName) == target)
}

func runWebTTYClient(cmd *cobra.Command, urlOverride string, args []string) error {
	return runWebTTYClientWithOptions(cmd, urlOverride, args, webTTYClientRunOptions{DefaultOutput: "text", Subcommand: "client"})
}

func runWebTTYClientWithOptions(cmd *cobra.Command, urlOverride string, args []string, options webTTYClientRunOptions) error {
	ctx := cmd.Context()
	logger := slog.With("cmd", "webtty", "subcmd", options.Subcommand)
	urlValue := strings.TrimSpace(urlOverride)
	if urlValue == "" {
		urlValue, _ = cmd.Flags().GetString("url")
	}
	if urlValue == "" {
		urlValue = "ws://127.0.0.1:8080"
	}
	transport, err := webTTYTransportFromFlag(cmd)
	if err != nil {
		return err
	}
	execPath, _ := cmd.Flags().GetString("exec-path")
	if transport == webtty.WebTTYTransportPlain {
		if strings.TrimSpace(execPath) != "" {
			return fmt.Errorf("--exec-path requires websocket WebTTY transport")
		}
	} else {
		urlValue, err = resolveWebTTYExecURL(urlValue, execPath)
		if err != nil {
			return err
		}
	}
	interactivePtr := getBoolPtr(cmd, "interactive")
	noInteractivePtr := getBoolPtr(cmd, "no-interactive")
	interactive := false
	switch {
	case options.ForceNoInteractive:
		interactive = false
	case interactivePtr != nil && *interactivePtr:
		interactive = true
	case noInteractivePtr != nil && *noInteractivePtr:
		interactive = false
	default:
		interactive = len(args) == 0
	}
	ttyPtr := getBoolPtr(cmd, "tty")
	noTTYPtr := getBoolPtr(cmd, "no-tty")
	allocateTTY := false
	switch {
	case options.ForceNoTTY:
		allocateTTY = false
	case ttyPtr != nil && *ttyPtr:
		allocateTTY = true
	case noTTYPtr != nil && *noTTYPtr:
		allocateTTY = false
	default:
		allocateTTY = len(args) == 0
	}
	envVars, _ := cmd.Flags().GetStringArray("env")
	workdir := getStringPtr(cmd, "workdir")
	username := getStringPtr(cmd, "user")
	clientCfg := &webtty.ClientConfig{
		URL:           urlValue,
		Transport:     transport,
		Interactive:   interactive,
		AllocateTTY:   allocateTTY,
		SendHeartbeat: true,
		EnvVars:       envVars,
		Workdir:       workdir,
		Username:      username,
		CmdArgs:       args,
		Logger:        logger,
	}
	tlsConfig, err := webTTYClientTLSConfig(cmd)
	if err != nil {
		return err
	}
	clientCfg.TLSConfig = tlsConfig
	outputMode := options.DefaultOutput
	if cmd.Flags().Changed("output") {
		outputMode, _ = cmd.Flags().GetString("output")
	}
	outputMode = strings.ToLower(strings.TrimSpace(outputMode))
	authToken, err := readWebTTYAuthToken(cmd)
	if err != nil {
		return err
	}
	clientCfg.AuthToken = authToken
	var runtimeE2E *webTTYClientRuntimeE2EContext
	var securityScope webTTYClientSecurityScope
	var rstreamClient *rstream.Client
	if webttyClientUsesRstream(urlValue) {
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return fmt.Errorf("failed to resolve runtime: %w", err)
		}
		rstreamClient, err = newClientFromResolved(runtime.Resolved)
		if err != nil {
			return fmt.Errorf("failed to create rstream client: %w", err)
		}
		rstreamResolution, err := resolveWebTTYClientRstream(ctx, runtime, rstreamClient, urlValue)
		if err != nil {
			return err
		}
		urlValue = rstreamResolution.URL
		securityScope = rstreamResolution.Scope
		urlValue, err = webTTYURLWithManagedSessionMode(urlValue, interactive)
		if err != nil {
			return err
		}
		clientCfg.URL = urlValue
		runtimeE2E = rstreamResolution.RuntimeE2E
		clientCfg.DialContext = newWebTTYClientDialContext(rstreamClient)
		if transport == webtty.WebTTYTransportWebTransport {
			clientCfg.DialPacketContext = newWebTTYClientPacketDialContext(rstreamClient)
			if clientCfg.TLSConfig == nil {
				clientCfg.TLSConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}
			}
		}
	}
	cryptoConfig, err := webTTYClientCryptoWithRuntimeAndScope(ctx, cmd, runtimeE2E, securityScope)
	if err != nil {
		return err
	}
	clientCfg.PayloadCrypto = cryptoConfig.PayloadCrypto
	clientCfg.ExpectedServerIdentity = cryptoConfig.ExpectedServerIdentity
	clientCfg.ClientCredential = append([]byte(nil), cryptoConfig.ClientCredential...)
	if cryptoConfig.ExpectedServerIdentity != nil {
		identity := cryptoConfig.EndpointIdentity
		if identity == nil {
			identity, err = webTTYClientEndpointIdentityForNameOrExplicitSources(cmd, cryptoConfig.ClientIdentityName)
			if err != nil {
				return err
			}
		}
		if identity == nil {
			return webTTYMissingClientEndpointIdentityError(securityScope, cryptoConfig)
		}
		clientCfg.EndpointIdentity = identity
	}
	if outputMode == "json" {
		result, err := runWebTTYClientCapture(ctx, clientCfg)
		if result != nil {
			if encodeErr := writeStructuredOutput("json", result); encodeErr != nil && err == nil {
				err = encodeErr
			}
		}
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return &commandExitError{code: result.ExitCode}
		}
		return nil
	}
	if outputMode != "text" {
		return fmt.Errorf("invalid --output %q (valid: text, json)", outputMode)
	}
	exitCode, err := webtty.RunClient(ctx, clientCfg)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return &commandExitError{code: exitCode}
	}
	return nil
}

func runWebTTYClientCapture(ctx context.Context, cfg *webtty.ClientConfig) (*webTTYClientResult, error) {
	start := time.Now()
	session, err := webtty.OpenClientSession(ctx, webTTYSessionConfigFromClientConfig(cfg))
	if err != nil {
		return nil, err
	}
	defer session.Close()
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}
	waitCh := make(chan struct {
		exitCode int
		err      error
	}, 1)
	go func() {
		exitCode, err := session.Wait()
		waitCh <- struct {
			exitCode int
			err      error
		}{exitCode: exitCode, err: err}
	}()
	for {
		select {
		case event, ok := <-session.Events():
			if ok {
				if event.Stream == webtty.ClientSessionStderr {
					stderr.Write(event.Data)
				} else {
					stdout.Write(event.Data)
				}
			}
		case result := <-waitCh:
			return &webTTYClientResult{URL: cfg.URL, Command: append([]string(nil), cfg.CmdArgs...), ExitCode: result.exitCode, Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: time.Since(start).Milliseconds()}, result.err
		case <-ctx.Done():
			_ = session.CloseWithError(ctx.Err())
			return &webTTYClientResult{URL: cfg.URL, Command: append([]string(nil), cfg.CmdArgs...), ExitCode: -1, Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: time.Since(start).Milliseconds()}, ctx.Err()
		}
	}
}

func webTTYSessionConfigFromClientConfig(cfg *webtty.ClientConfig) *webtty.SessionConfig {
	return &webtty.SessionConfig{
		URL:                    cfg.URL,
		Transport:              cfg.Transport,
		DialContext:            cfg.DialContext,
		DialTLSContext:         cfg.DialTLSContext,
		DialPacketContext:      cfg.DialPacketContext,
		Interactive:            cfg.Interactive,
		AllocateTTY:            cfg.AllocateTTY,
		SendHeartbeat:          cfg.SendHeartbeat,
		Attach:                 cfg.Attach,
		PayloadCrypto:          cfg.PayloadCrypto,
		EndpointIdentity:       cfg.EndpointIdentity,
		ExpectedServerIdentity: cfg.ExpectedServerIdentity,
		ClientCredential:       append([]byte(nil), cfg.ClientCredential...),
		ClientPrincipalID:      cfg.ClientPrincipalID,
		ClientDeviceID:         cfg.ClientDeviceID,
		ClientBrowserID:        cfg.ClientBrowserID,
		TLSConfig:              cfg.TLSConfig,
		EnvVars:                append([]string(nil), cfg.EnvVars...),
		Workdir:                cfg.Workdir,
		Username:               cfg.Username,
		CmdArgs:                append([]string(nil), cfg.CmdArgs...),
		AuthToken:              cfg.AuthToken,
		MaxMessageSize:         cfg.MaxMessageSize,
		ReadBufferSize:         cfg.ReadBufferSize,
		WriteBufferSize:        cfg.WriteBufferSize,
		OpenDeadline:           cfg.OpenDeadline,
		CloseDeadline:          cfg.CloseDeadline,
		HeartbeatInterval:      cfg.HeartbeatInterval,
		Logger:                 cfg.Logger,
	}
}

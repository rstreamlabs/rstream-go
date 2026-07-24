// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

const tunneledWebTransportInitialPacketSize = 1200

type SessionConfig struct {
	URL                    string
	Transport              WebTTYTransport
	DialContext            func(context.Context, string, string) (net.Conn, error)
	DialTLSContext         func(context.Context, string, string) (net.Conn, error)
	DialPacketContext      func(context.Context, string) (net.PacketConn, net.Addr, error)
	Interactive            bool
	AllocateTTY            bool
	SendHeartbeat          bool
	Attach                 *AttachConfig
	PayloadCrypto          *PayloadCrypto
	EndpointIdentity       *WebTTYEndpointIdentity
	ExpectedServerIdentity *WebTTYEndpointIdentityPublic
	ClientCredential       []byte
	ClientPrincipalID      string
	ClientDeviceID         string
	ClientBrowserID        string
	EnvVars                []string
	Workdir                *string
	Username               *string
	CmdArgs                []string
	AuthToken              *string
	TLSConfig              *tls.Config
	MaxMessageSize         *int64
	ReadBufferSize         *int
	WriteBufferSize        *int
	OpenDeadline           *time.Duration
	CloseDeadline          *time.Duration
	HeartbeatInterval      *time.Duration
	Logger                 *slog.Logger
}

type ClientSessionStream string

const (
	ClientSessionStdout ClientSessionStream = "stdout"
	ClientSessionStderr ClientSessionStream = "stderr"
)

type ClientSessionEvent struct {
	Stream ClientSessionStream
	Data   []byte
}

type clientSessionResult struct {
	exitCode int
	err      error
}

type ClientSession struct {
	runtime            *clientRuntime
	loopCancel         context.CancelFunc
	doneRead           chan struct{}
	done               chan struct{}
	events             chan ClientSessionEvent
	resultCh           chan clientSessionResult
	closeTransportOnce sync.Once
	finalizeOnce       sync.Once
	resultMu           sync.Mutex
	closeResult        *clientSessionResult
}

func (cfg *ClientConfig) sessionConfig() *SessionConfig {
	if cfg == nil {
		return nil
	}
	return &SessionConfig{
		URL:                    cfg.URL,
		Transport:              cfg.Transport,
		DialContext:            cfg.DialContext,
		DialTLSContext:         cfg.DialTLSContext,
		DialPacketContext:      cfg.DialPacketContext,
		Interactive:            cfg.Interactive,
		AllocateTTY:            cfg.AllocateTTY,
		SendHeartbeat:          cfg.SendHeartbeat,
		Attach:                 cloneAttachConfig(cfg.Attach),
		PayloadCrypto:          cfg.PayloadCrypto,
		EndpointIdentity:       cfg.EndpointIdentity,
		ExpectedServerIdentity: cfg.ExpectedServerIdentity,
		ClientCredential:       append([]byte(nil), cfg.ClientCredential...),
		ClientPrincipalID:      cfg.ClientPrincipalID,
		ClientDeviceID:         cfg.ClientDeviceID,
		ClientBrowserID:        cfg.ClientBrowserID,
		EnvVars:                append([]string(nil), cfg.EnvVars...),
		Workdir:                cfg.Workdir,
		Username:               cfg.Username,
		CmdArgs:                append([]string(nil), cfg.CmdArgs...),
		AuthToken:              cfg.AuthToken,
		TLSConfig:              cloneTLSConfig(cfg.TLSConfig),
		MaxMessageSize:         cfg.MaxMessageSize,
		ReadBufferSize:         cfg.ReadBufferSize,
		WriteBufferSize:        cfg.WriteBufferSize,
		OpenDeadline:           cfg.OpenDeadline,
		CloseDeadline:          cfg.CloseDeadline,
		HeartbeatInterval:      cfg.HeartbeatInterval,
		Logger:                 cfg.Logger,
	}
}

func (cfg *SessionConfig) clientConfig() *ClientConfig {
	if cfg == nil {
		return nil
	}
	return &ClientConfig{
		URL:                    cfg.URL,
		Transport:              cfg.Transport,
		DialContext:            cfg.DialContext,
		DialTLSContext:         cfg.DialTLSContext,
		DialPacketContext:      cfg.DialPacketContext,
		Interactive:            cfg.Interactive,
		AllocateTTY:            cfg.AllocateTTY,
		SendHeartbeat:          cfg.SendHeartbeat,
		Attach:                 cloneAttachConfig(cfg.Attach),
		PayloadCrypto:          cfg.PayloadCrypto,
		EndpointIdentity:       cfg.EndpointIdentity,
		ExpectedServerIdentity: cfg.ExpectedServerIdentity,
		ClientCredential:       append([]byte(nil), cfg.ClientCredential...),
		ClientPrincipalID:      cfg.ClientPrincipalID,
		ClientDeviceID:         cfg.ClientDeviceID,
		ClientBrowserID:        cfg.ClientBrowserID,
		EnvVars:                append([]string(nil), cfg.EnvVars...),
		Workdir:                cfg.Workdir,
		Username:               cfg.Username,
		CmdArgs:                append([]string(nil), cfg.CmdArgs...),
		AuthToken:              cfg.AuthToken,
		TLSConfig:              cloneTLSConfig(cfg.TLSConfig),
		MaxMessageSize:         cfg.MaxMessageSize,
		ReadBufferSize:         cfg.ReadBufferSize,
		WriteBufferSize:        cfg.WriteBufferSize,
		OpenDeadline:           cfg.OpenDeadline,
		CloseDeadline:          cfg.CloseDeadline,
		HeartbeatInterval:      cfg.HeartbeatInterval,
		Logger:                 cfg.Logger,
	}
}

func resolveSessionConfig(cfg *SessionConfig) (*SessionConfig, error) {
	if cfg == nil {
		cfg = &SessionConfig{}
	}
	if cfg.PayloadCrypto != nil && cfg.PayloadCrypto.SessionKeyGrant != nil && cfg.Attach == nil {
		if cfg.ExpectedServerIdentity == nil {
			return nil, fmt.Errorf("WebTTY E2E requires a known server endpoint identity")
		}
		if cfg.EndpointIdentity == nil {
			return nil, fmt.Errorf("WebTTY E2E requires a client endpoint identity")
		}
	}
	if cfg.URL == "" {
		cfg.URL = "ws://127.0.0.1:8080"
	}
	if cfg.MaxMessageSize == nil {
		value := defaultMaxMessageSize
		cfg.MaxMessageSize = &value
	}
	if cfg.ReadBufferSize == nil {
		value := defaultReadBufferSize
		cfg.ReadBufferSize = &value
	}
	if cfg.WriteBufferSize == nil {
		value := defaultWriteBufferSize
		cfg.WriteBufferSize = &value
	}
	if cfg.OpenDeadline == nil {
		value := defaultClientOpenDeadline
		cfg.OpenDeadline = &value
	}
	if cfg.CloseDeadline == nil {
		value := defaultClientCloseDeadline
		cfg.CloseDeadline = &value
	}
	if cfg.HeartbeatInterval == nil {
		value := defaultHeartbeatInterval
		cfg.HeartbeatInterval = &value
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg, nil
}

func OpenClientSession(ctx context.Context, cfg *SessionConfig) (*ClientSession, error) {
	resolved, err := resolveSessionConfig(cfg)
	if err != nil {
		return nil, err
	}
	endpoint, err := resolveWebTTYEndpointWithTransport(resolved.URL, resolved.Transport)
	if err != nil {
		return nil, err
	}
	conn, err := dialWebTTYMessageConn(ctx, resolved, endpoint)
	if err != nil {
		return nil, err
	}
	runtimeCfg := resolved.clientConfig()
	if *resolved.MaxMessageSize > 0 {
		conn.SetReadLimit(*resolved.MaxMessageSize)
	}
	runtime := &clientRuntime{
		conn:     conn,
		cfg:      runtimeCfg,
		logger:   runtimeCfg.Logger.With("component", "webtty.client"),
		logProto: stringsEqualFoldTrimmed(rstream.Channel, "dev"),
	}
	if endpoint.Transport == WebTTYTransportWebTransport && runtime.cfg.ExpectedServerIdentity != nil {
		if err := runtime.writeMessage(&pb.Message{Payload: &pb.Message_ClientHello{ClientHello: &pb.ClientHello{ProtocolVersion: pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1}}}); err != nil {
			runtime.closeConn()
			return nil, fmt.Errorf("failed to send WebTTY client hello: %w", err)
		}
	}
	doneRead := make(chan struct{})
	readEvents := make(chan clientEvent, 1)
	go runtime.readLoop(doneRead, readEvents)
	var serverHello *pb.ServerHello
	if runtime.cfg.ExpectedServerIdentity != nil {
		event, err := waitForClientEvent(ctx, runtime.cfg.OpenDeadline, readEvents)
		if err != nil {
			close(doneRead)
			runtime.closeConn()
			return nil, err
		}
		msg := event.msg
		if protocolError := msg.GetProtocolError(); protocolError != nil {
			close(doneRead)
			runtime.closeConn()
			return nil, webTTYProtocolError(protocolError)
		}
		if msg == nil || msg.GetServerHello() == nil {
			close(doneRead)
			runtime.closeConn()
			return nil, fmt.Errorf("%w: expected WebTTY server hello", errClientUnexpected)
		}
		serverHello = msg.GetServerHello()
		if err := runtime.verifyServerHello(serverHello, endpoint.Transport); err != nil {
			close(doneRead)
			runtime.closeConn()
			return nil, err
		}
	}
	handshakeMessage, err := runtime.buildHandshakeMessage(endpoint.Transport, serverHello)
	if err != nil {
		close(doneRead)
		runtime.closeConn()
		return nil, err
	}
	if err := runtime.writeMessage(handshakeMessage); err != nil {
		close(doneRead)
		runtime.closeConn()
		return nil, fmt.Errorf("failed to send WebTTY handshake message: %w", err)
	}
	if err := runtime.waitForOpen(ctx, readEvents); err != nil {
		close(doneRead)
		runtime.closeConn()
		return nil, err
	}
	loopCtx, loopCancel := context.WithCancel(ctx)
	session := &ClientSession{
		runtime:    runtime,
		loopCancel: loopCancel,
		doneRead:   doneRead,
		done:       make(chan struct{}),
		events:     make(chan ClientSessionEvent, 128),
		resultCh:   make(chan clientSessionResult, 1),
	}
	loopErrCh := make(chan error, 1)
	if resolved.SendHeartbeat {
		go runtime.heartbeatLoop(loopCtx, loopErrCh)
	}
	go session.run(loopCtx, readEvents, loopErrCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.CloseWithError(ctx.Err())
		case <-session.done:
		}
	}()
	return session, nil
}

func dialWebTTYMessageConn(ctx context.Context, cfg *SessionConfig, endpoint *clientEndpoint) (messageConn, error) {
	if endpoint.Transport == WebTTYTransportPlain {
		return dialPlainWebTTYMessageConn(ctx, cfg, endpoint)
	}
	if endpoint.Transport == WebTTYTransportWebTransport {
		return dialWebTransportWebTTYMessageConn(ctx, cfg, endpoint)
	}
	dialer := &websocket.Dialer{
		ReadBufferSize:    *cfg.ReadBufferSize,
		WriteBufferSize:   *cfg.WriteBufferSize,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
		Proxy:             http.ProxyFromEnvironment,
		TLSClientConfig:   cloneTLSConfig(cfg.TLSConfig),
	}
	if endpoint.RequiresCustomDial {
		if cfg.DialContext == nil {
			return nil, fmt.Errorf("websocket url scheme %q requires a custom dialer", "rstrm")
		}
		dialer.NetDialContext = cfg.DialContext
	}
	if cfg.DialTLSContext != nil {
		dialer.NetDialTLSContext = cfg.DialTLSContext
	}
	header := http.Header{}
	if cfg.AuthToken != nil && strings.TrimSpace(*cfg.AuthToken) != "" {
		header.Set("Authorization", "Bearer "+strings.TrimSpace(*cfg.AuthToken))
	}
	conn, resp, err := dialer.DialContext(ctx, endpoint.URL, header)
	if err != nil {
		if resp != nil {
			statusCode := resp.StatusCode
			_ = resp.Body.Close()
			return nil, fmt.Errorf("websocket dial failed with status %d", statusCode)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	return conn, nil
}

func dialPlainWebTTYMessageConn(ctx context.Context, cfg *SessionConfig, endpoint *clientEndpoint) (messageConn, error) {
	if cfg.AuthToken != nil && strings.TrimSpace(*cfg.AuthToken) != "" {
		return nil, fmt.Errorf("plain WebTTY transport does not support HTTP bearer tokens")
	}
	dialContext := cfg.DialContext
	if endpoint.RequiresCustomDial {
		if dialContext == nil {
			return nil, fmt.Errorf("plain rstrm WebTTY transport requires a custom dialer")
		}
	} else if dialContext == nil {
		dialer := &net.Dialer{}
		dialContext = dialer.DialContext
	}
	conn, err := dialContext(ctx, "tcp", endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("plain WebTTY dial failed: %w", err)
	}
	if endpoint.TLS {
		tlsConfig := cloneTLSConfigWithWebTTYDefaults(cfg.TLSConfig)
		if strings.TrimSpace(tlsConfig.ServerName) == "" {
			tlsConfig.ServerName = endpointTLSServerName(endpoint.Address)
		}
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("plain WebTTY TLS handshake failed: %w", err)
		}
		conn = tlsConn
	}
	return newPlainMessageConn(conn), nil
}

func dialWebTransportWebTTYMessageConn(ctx context.Context, cfg *SessionConfig, endpoint *clientEndpoint) (messageConn, error) {
	tlsConfig := cloneTLSConfigWithWebTTYDefaults(cfg.TLSConfig)
	tlsConfig.NextProtos = ensureTLSNextProto(tlsConfig.NextProtos, http3.NextProtoH3)
	if strings.TrimSpace(tlsConfig.ServerName) == "" {
		tlsConfig.ServerName = webTransportTLSServerName(endpoint.URL)
	}
	header := http.Header{}
	if cfg.AuthToken != nil && strings.TrimSpace(*cfg.AuthToken) != "" {
		header.Set("Authorization", "Bearer "+strings.TrimSpace(*cfg.AuthToken))
	}
	dialer := &webtransport.Dialer{
		TLSClientConfig: tlsConfig,
		QUICConfig:      webTransportClientQUICConfig(endpoint.RequiresCustomDial),
	}
	if endpoint.RequiresCustomDial {
		if cfg.DialPacketContext == nil {
			return nil, fmt.Errorf("WebTransport rstrm WebTTY transport requires a custom packet dialer")
		}
		dialer.DialAddr = func(ctx context.Context, addr string, tlsCfg *tls.Config, quicCfg *quic.Config) (*quic.Conn, error) {
			pc, remoteAddr, err := cfg.DialPacketContext(ctx, addr)
			if err != nil {
				return nil, err
			}
			qconn, err := quic.DialEarly(ctx, pc, remoteAddr, tlsCfg, quicCfg)
			if err != nil {
				_ = pc.Close()
				return nil, err
			}
			go func() {
				<-qconn.Context().Done()
				_ = pc.Close()
			}()
			return qconn, nil
		}
	}
	resp, session, err := dialer.Dial(ctx, endpoint.URL, header)
	if err != nil {
		if resp != nil {
			statusCode := resp.StatusCode
			_ = resp.Body.Close()
			return nil, fmt.Errorf("WebTransport dial failed with status %d", statusCode)
		}
		return nil, fmt.Errorf("WebTransport dial failed: %w", err)
	}
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		_ = session.CloseWithError(0, "stream open failed")
		return nil, fmt.Errorf("WebTransport stream open failed: %w", err)
	}
	return newWebTransportMessageConn(session, stream), nil
}

func webTransportClientQUICConfig(tunneled bool) *quic.Config {
	cfg := &quic.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
	}
	if tunneled {
		cfg.InitialPacketSize = tunneledWebTransportInitialPacketSize
		cfg.DisablePathMTUDiscovery = true
	}
	return cfg
}

func endpointTLSServerName(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

func webTransportTLSServerName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(u.Hostname()) == "" {
		return ""
	}
	return u.Hostname()
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	return cfg.Clone()
}

func cloneAttachConfig(cfg *AttachConfig) *AttachConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.AttachGrant = cloneBytes(cfg.AttachGrant)
	cloned.Capabilities = append([]AttachCapability(nil), cfg.Capabilities...)
	return &cloned
}

func cloneTLSConfigWithWebTTYDefaults(cfg *tls.Config) *tls.Config {
	tlsConfig := cloneTLSConfig(cfg)
	if tlsConfig == nil {
		return &tls.Config{MinVersion: tls.VersionTLS13}
	}
	if tlsConfig.MinVersion == 0 || tlsConfig.MinVersion < tls.VersionTLS13 {
		tlsConfig.MinVersion = tls.VersionTLS13
	}
	return tlsConfig
}

func ensureTLSNextProto(values []string, proto string) []string {
	for _, value := range values {
		if value == proto {
			return values
		}
	}
	return append(append([]string(nil), values...), proto)
}

func (s *ClientSession) Events() <-chan ClientSessionEvent { return s.events }

func (s *ClientSession) Wait() (int, error) {
	result, ok := <-s.resultCh
	if !ok {
		return -1, io.EOF
	}
	return result.exitCode, result.err
}

func (s *ClientSession) SendInput(data []byte) error {
	return s.SendInputContext(context.Background(), data)
}

func (s *ClientSession) SendInputContext(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	msg, err := s.runtime.stdinDataMessage(ctx, data)
	if err != nil {
		return err
	}
	return s.runtime.writeMessage(msg)
}

func (s *ClientSession) SendText(text string) error {
	if text == "" {
		return nil
	}
	return s.SendInput([]byte(text))
}

func (s *ClientSession) SendEOF() error {
	return s.runtime.writeMessage(&pb.Message{
		Payload: &pb.Message_Data{
			Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}},
		},
	})
}

func (s *ClientSession) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return s.runtime.writeMessage(&pb.Message{
		Payload: &pb.Message_Parameter{
			Parameter: &pb.Parameter{
				Parameter: &pb.Parameter_TerminalSize{
					TerminalSize: &pb.TerminalSize{Row: uint32(rows), Col: uint32(cols)},
				},
			},
		},
	})
}

func (s *ClientSession) Close() error {
	s.requestClose(-1, nil, false)
	return nil
}

func (s *ClientSession) CloseWithError(err error) error {
	s.requestClose(-1, err, true)
	return nil
}

func (s *ClientSession) run(ctx context.Context, readEvents <-chan clientEvent, loopErrCh <-chan error) {
	for {
		select {
		case <-ctx.Done():
			s.finalize(-1, ctx.Err())
			return
		case event, ok := <-readEvents:
			if !ok {
				s.finalize(-1, io.EOF)
				return
			}
			if event.err != nil {
				s.finalize(-1, event.err)
				return
			}
			if event.msg == nil {
				s.finalize(-1, errClientProtocol)
				return
			}
			switch payload := event.msg.Payload.(type) {
			case *pb.Message_Data:
				sessionEvent, ok, err := s.runtime.decodeClientSessionEvent(ctx, payload.Data)
				if err != nil {
					s.finalize(-1, err)
					return
				}
				if !ok {
					continue
				}
				select {
				case s.events <- sessionEvent:
				case <-ctx.Done():
					s.finalize(-1, ctx.Err())
					return
				}
			case *pb.Message_Close:
				if payload.Close == nil {
					s.finalize(-1, errClientProtocol)
					return
				}
				s.finalize(int(payload.Close.ReturnCode), nil)
				return
			case *pb.Message_Heartbeat:
			case *pb.Message_Error:
				if payload.Error != nil && stringsTrimSpace(payload.Error.Msg) != "" {
					msg := stringsTrimSpace(payload.Error.Msg)
					s.finalize(-1, fmt.Errorf("%w: %s", errClientServer, msg))
				} else {
					s.finalize(-1, errClientServer)
				}
				return
			case *pb.Message_ProtocolError:
				s.finalize(-1, webTTYProtocolError(payload.ProtocolError))
				return
			default:
				s.finalize(-1, fmt.Errorf("%w: %T", errClientUnexpected, payload))
				return
			}
		case err := <-loopErrCh:
			if err != nil {
				s.finalize(-1, err)
				return
			}
		}
	}
}

func (s *ClientSession) requestClose(exitCode int, err error, sendError bool) {
	s.closeTransportOnce.Do(func() {
		s.resultMu.Lock()
		s.closeResult = &clientSessionResult{exitCode: exitCode, err: err}
		s.resultMu.Unlock()
		s.loopCancel()
		if sendError && err != nil {
			_ = s.runtime.sendClientError(err)
		}
		s.runtime.closeConn()
	})
}

func (s *ClientSession) finalize(exitCode int, err error) {
	s.finalizeOnce.Do(func() {
		s.loopCancel()
		close(s.doneRead)
		result := clientSessionResult{exitCode: exitCode, err: err}
		s.resultMu.Lock()
		if s.closeResult != nil {
			result = *s.closeResult
		}
		s.resultMu.Unlock()
		s.resultCh <- result
		close(s.resultCh)
		close(s.events)
		close(s.done)
	})
}

func decodeClientSessionEvent(data *pb.Data) (ClientSessionEvent, bool, error) {
	return (&clientRuntime{cfg: &ClientConfig{}}).decodeClientSessionEvent(context.Background(), data)
}

func stringsEqualFoldTrimmed(value, target string) bool {
	return stringsTrimSpaceFold(value) == stringsTrimSpaceFold(target)
}

func stringsTrimSpaceFold(value string) string {
	return strings.ToLower(stringsTrimSpace(value))
}

func stringsTrimSpace(value string) string {
	return strings.TrimSpace(value)
}

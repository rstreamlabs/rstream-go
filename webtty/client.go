// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
	"google.golang.org/protobuf/proto"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type ClientConfig struct {
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
	TLSConfig              *tls.Config
	EnvVars                []string
	Workdir                *string
	Username               *string
	CmdArgs                []string
	AuthToken              *string
	MaxMessageSize         *int64
	ReadBufferSize         *int
	WriteBufferSize        *int
	OpenDeadline           *time.Duration
	CloseDeadline          *time.Duration
	HeartbeatInterval      *time.Duration
	Stdin                  io.Reader
	StdinReadContext       func(context.Context, []byte) (int, error)
	Stdout                 io.Writer
	Stderr                 io.Writer
	Logger                 *slog.Logger
}

type AttachRole string

const (
	AttachRoleSpectator  AttachRole = "spectator"
	AttachRoleController AttachRole = "controller"
)

type AttachCapability string

const (
	AttachCapabilityReadStream     AttachCapability = "read_stream"
	AttachCapabilityRequestControl AttachCapability = "request_control"
	AttachCapabilityReceiveControl AttachCapability = "receive_control"
)

type AttachConfig struct {
	SessionID     string
	WorkspaceID   string
	ProjectID     string
	ServerID      string
	ParticipantID string
	AttachGrant   []byte
	RequestedRole AttachRole
	Transport     WebTTYTransport
	Capabilities  []AttachCapability
	DeviceID      string
	BrowserID     string
}

type clientRuntime struct {
	conn        messageConn
	cfg         *ClientConfig
	logger      *slog.Logger
	logProto    bool
	writeMu     sync.Mutex
	closeOnce   sync.Once
	closing     atomic.Bool
	stdinFD     int
	hasStdinFD  bool
	hasTerminal bool
}

type clientEndpoint struct {
	URL                string
	Address            string
	RequiresCustomDial bool
	TLS                bool
	Transport          WebTTYTransport
}

type clientEvent struct {
	msg *pb.Message
	err error
}

const (
	defaultClientCloseDeadline      time.Duration = 5 * time.Second
	defaultClientOpenDeadline       time.Duration = 5 * time.Second
	defaultStdinReadPollPeriod      time.Duration = 100 * time.Millisecond
	defaultTerminalResizePollPeriod time.Duration = 300 * time.Millisecond
)

var (
	errClientOperationTimeout = errors.New("operation timeout")
	errClientProtocol         = errors.New("protocol error")
	errClientServer           = errors.New("server error")
	errClientUnexpected       = errors.New("unexpected message")
	errClientWriteBusy        = errors.New("another WebTTY write is in progress")
)

func RunClient(ctx context.Context, cfg *ClientConfig) (int, error) {
	resolved, err := resolveClientConfig(cfg)
	if err != nil {
		return -1, err
	}
	runtime := &clientRuntime{cfg: resolved}
	if fd, ok := stdinFileDescriptor(resolved.Stdin); ok {
		runtime.stdinFD = fd
		runtime.hasStdinFD = true
	}
	if resolved.AllocateTTY {
		if !runtime.hasStdinFD || !term.IsTerminal(runtime.stdinFD) {
			return -1, fmt.Errorf("tty allocation requires stdin to be a terminal")
		}
		runtime.hasTerminal = true
		if resolved.Interactive {
			state, err := term.MakeRaw(runtime.stdinFD)
			if err != nil {
				return -1, fmt.Errorf("failed to switch terminal to raw mode: %w", err)
			}
			defer term.Restore(runtime.stdinFD, state)
		}
	}
	forwardStdin := resolved.Interactive || !runtime.hasStdinFD || !term.IsTerminal(runtime.stdinFD)
	var readStdin func(context.Context, []byte) (int, error)
	if forwardStdin {
		readStdin, err = resolveClientStdinRead(resolved)
		if err != nil {
			return -1, err
		}
	}
	session, err := OpenClientSession(ctx, resolved.sessionConfig())
	if err != nil {
		return -1, err
	}
	runtime.logger = resolved.Logger.With("component", "webtty.client")
	runtime.logger.Debug("webtty session acknowledged")
	loopCtx, stopLoops := context.WithCancel(ctx)
	var loopWG sync.WaitGroup
	defer func() {
		stopLoops()
		_ = session.Close()
		loopWG.Wait()
	}()
	loopErrCh := make(chan error, 1)
	if forwardStdin {
		loopWG.Add(1)
		go func() {
			defer loopWG.Done()
			runtime.stdinSessionLoop(loopCtx, session, loopErrCh, readStdin)
		}()
	}
	if resolved.AllocateTTY {
		loopWG.Add(1)
		go func() {
			defer loopWG.Done()
			runtime.resizeSessionLoop(loopCtx, session, loopErrCh)
		}()
	}
	waitCh := make(chan clientSessionResult, 1)
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		exitCode, err := session.Wait()
		waitCh <- clientSessionResult{exitCode: exitCode, err: err}
	}()
	var pendingErr error
	var closeTimer *time.Timer
	var closeTimeout <-chan time.Time
	defer func() {
		if closeTimer == nil {
			return
		}
		if !closeTimer.Stop() {
			select {
			case <-closeTimer.C:
			default:
			}
		}
	}()
	ctxDone := ctx.Done()
	events := session.Events()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := runtime.writeSessionEvent(event); err != nil {
				return -1, err
			}
		case result := <-waitCh:
			if result.err != nil {
				if pendingErr != nil {
					return -1, pendingErr
				}
				return -1, result.err
			}
			if pendingErr != nil {
				return -1, pendingErr
			}
			return result.exitCode, nil
		case err := <-loopErrCh:
			if err == nil || pendingErr != nil {
				continue
			}
			pendingErr = err
			stopLoops()
			session.requestClose(-1, err, true)
			if resolved.CloseDeadline != nil && *resolved.CloseDeadline > 0 {
				closeTimer = time.NewTimer(*resolved.CloseDeadline)
				closeTimeout = closeTimer.C
			}
		case <-ctxDone:
			ctxDone = nil
			if pendingErr != nil {
				continue
			}
			pendingErr = ctx.Err()
			stopLoops()
			session.requestClose(-1, pendingErr, true)
			if resolved.CloseDeadline != nil && *resolved.CloseDeadline > 0 {
				closeTimer = time.NewTimer(*resolved.CloseDeadline)
				closeTimeout = closeTimer.C
			}
		case <-closeTimeout:
			if pendingErr != nil {
				return -1, pendingErr
			}
			return -1, errClientOperationTimeout
		}
	}
}

func (c *clientRuntime) writeSessionEvent(event ClientSessionEvent) error {
	var writer io.Writer
	switch event.Stream {
	case ClientSessionStderr:
		writer = c.cfg.Stderr
	default:
		writer = c.cfg.Stdout
	}
	return writeAll(writer, event.Data)
}

func (c *clientRuntime) stdinSessionLoop(ctx context.Context, session *ClientSession, errCh chan<- error, readStdin func(context.Context, []byte) (int, error)) {
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := readStdin(ctx, buffer)
		if n > 0 {
			if werr := session.SendInputContext(ctx, buffer[:n]); werr != nil {
				select {
				case errCh <- fmt.Errorf("failed to send stdin payload: %w", werr):
				default:
				}
				return
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, io.EOF) {
				if werr := session.SendEOF(); werr != nil {
					if session.runtime.closing.Load() {
						return
					}
					select {
					case errCh <- fmt.Errorf("failed to send stdin eos: %w", werr):
					default:
					}
				}
				return
			}
			select {
			case errCh <- fmt.Errorf("failed to read stdin: %w", err):
			default:
			}
			return
		}
	}
}

type clientStdinContextReader interface {
	ReadContext(context.Context, []byte) (int, error)
}

type clientStdinDeadlineReader interface {
	Read([]byte) (int, error)
	SetReadDeadline(time.Time) error
}

func resolveClientStdinRead(cfg *ClientConfig) (func(context.Context, []byte) (int, error), error) {
	if cfg.StdinReadContext != nil {
		return cfg.StdinReadContext, nil
	}
	if reader, ok := cfg.Stdin.(clientStdinContextReader); ok {
		return reader.ReadContext, nil
	}
	if file, ok := cfg.Stdin.(*os.File); ok {
		if readStdin := clientFileStdinRead(file); readStdin != nil {
			return readStdin, nil
		}
	}
	if reader, ok := cfg.Stdin.(clientStdinDeadlineReader); ok {
		if err := reader.SetReadDeadline(time.Time{}); err == nil {
			return func(ctx context.Context, buffer []byte) (int, error) {
				return readClientStdinWithDeadline(ctx, reader, buffer)
			}, nil
		}
	}
	switch cfg.Stdin.(type) {
	case *bytes.Buffer, *bytes.Reader, *strings.Reader:
		return func(ctx context.Context, buffer []byte) (int, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return cfg.Stdin.Read(buffer)
		}, nil
	}
	return nil, fmt.Errorf("stdin reader must support cancellation through StdinReadContext, ReadContext, or SetReadDeadline")
}

func readClientStdinWithDeadline(ctx context.Context, reader clientStdinDeadlineReader, buffer []byte) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := reader.SetReadDeadline(time.Now().Add(defaultStdinReadPollPeriod)); err != nil {
			return 0, fmt.Errorf("failed to set stdin read deadline: %w", err)
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			_ = reader.SetReadDeadline(time.Time{})
			return n, nil
		}
		if err == nil {
			continue
		}
		if !isClientStdinReadTimeout(err) {
			_ = reader.SetReadDeadline(time.Time{})
			return 0, err
		}
	}
}

func isClientStdinReadTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

type fileDescriptorReader interface {
	Fd() uintptr
}

func stdinFileDescriptor(reader io.Reader) (int, bool) {
	fdReader, ok := reader.(fileDescriptorReader)
	if !ok || fdReader == nil {
		return 0, false
	}
	return int(fdReader.Fd()), true
}

func (c *clientRuntime) resizeSessionLoop(ctx context.Context, session *ClientSession, errCh chan<- error) {
	if !c.hasTerminal {
		return
	}
	lastRows := -1
	lastCols := -1
	sendSize := func() error {
		cols, rows, err := term.GetSize(c.stdinFD)
		if err != nil {
			return fmt.Errorf("failed to read terminal size: %w", err)
		}
		if rows == lastRows && cols == lastCols {
			return nil
		}
		lastRows = rows
		lastCols = cols
		if err := session.Resize(rows, cols); err != nil {
			return fmt.Errorf("failed to send terminal size: %w", err)
		}
		return nil
	}
	if err := sendSize(); err != nil {
		select {
		case errCh <- err:
		default:
		}
		return
	}
	notifier := newTerminalResizeNotifier(defaultTerminalResizePollPeriod)
	defer notifier.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notifier.C():
			if err := sendSize(); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
	}
}

func resolveClientConfig(cfg *ClientConfig) (*ClientConfig, error) {
	cfg = cloneClientConfig(cfg)
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
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg, nil
}

func resolveWebTTYEndpoint(raw string) (*clientEndpoint, error) {
	return resolveWebTTYEndpointWithTransport(raw, "")
}

func resolveWebTTYEndpointWithTransport(raw string, transport WebTTYTransport) (*clientEndpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("WebTTY url is required")
	}
	if !strings.Contains(raw, "://") {
		switch transport {
		case WebTTYTransportPlain:
			raw = "tcp://" + raw
		case WebTTYTransportWebTransport:
			raw = "https://" + raw
		default:
			raw = "ws://" + raw
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid WebTTY url: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	transport, err = resolveEndpointTransport(scheme, transport)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("WebTTY host is required")
	}
	if transport == WebTTYTransportPlain {
		return resolvePlainWebTTYEndpoint(u, scheme)
	}
	if transport == WebTTYTransportWebTransport {
		return resolveWebTransportWebTTYEndpoint(u, scheme)
	}
	switch scheme {
	case "ws", "wss":
		if u.Path == "" {
			u.Path = "/"
		}
		return &clientEndpoint{URL: u.String(), Transport: WebTTYTransportWebSocket}, nil
	case "rstrm":
		if u.Path == "" {
			u.Path = "/"
		}
		u.Scheme = "ws"
		return &clientEndpoint{URL: u.String(), RequiresCustomDial: true, Transport: WebTTYTransportWebSocket}, nil
	default:
		return nil, fmt.Errorf("invalid websocket scheme %q (expected ws, wss, or rstrm)", u.Scheme)
	}
}

func resolveEndpointTransport(scheme string, configured WebTTYTransport) (WebTTYTransport, error) {
	switch configured {
	case "":
		switch scheme {
		case "tcp", "tls":
			return WebTTYTransportPlain, nil
		default:
			return WebTTYTransportWebSocket, nil
		}
	case WebTTYTransportWebSocket, WebTTYTransportPlain, WebTTYTransportWebTransport:
		return configured, nil
	default:
		return "", fmt.Errorf("invalid WebTTY transport %q", configured)
	}
}

func resolveWebTransportWebTTYEndpoint(u *url.URL, scheme string) (*clientEndpoint, error) {
	requiresCustomDial := false
	switch scheme {
	case "https":
	case "wss":
		u.Scheme = "https"
	case "webtransport", "wt", "wts":
		u.Scheme = "https"
	case "rstrm":
		u.Scheme = "https"
		requiresCustomDial = true
	default:
		return nil, fmt.Errorf("invalid WebTransport WebTTY scheme %q (expected https, wss, webtransport, wt, wts, or rstrm)", u.Scheme)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return &clientEndpoint{URL: u.String(), Address: u.Host, RequiresCustomDial: requiresCustomDial, Transport: WebTTYTransportWebTransport}, nil
}

func resolvePlainWebTTYEndpoint(u *url.URL, scheme string) (*clientEndpoint, error) {
	switch scheme {
	case "tcp", "tls", "rstrm":
	default:
		return nil, fmt.Errorf("invalid plain WebTTY scheme %q (expected tcp, tls, or rstrm)", u.Scheme)
	}
	address := u.Host
	if scheme == "tcp" {
		address = defaultPortAddress(address, "80")
	}
	if scheme == "tls" {
		address = defaultPortAddress(address, "443")
	}
	return &clientEndpoint{
		Address:            address,
		RequiresCustomDial: scheme == "rstrm",
		TLS:                scheme == "tls",
		Transport:          WebTTYTransportPlain,
		URL:                u.String(),
	}, nil
}

func defaultPortAddress(address string, defaultPort string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = strings.TrimPrefix(strings.TrimSuffix(address, "]"), "[")
	}
	if strings.Count(address, ":") > 1 {
		return net.JoinHostPort(address, defaultPort)
	}
	if strings.Contains(address, ":") {
		return address
	}
	return net.JoinHostPort(address, defaultPort)
}

func normalizeWebTTYURL(raw string) (string, error) {
	endpoint, err := resolveWebTTYEndpoint(raw)
	if err != nil {
		return "", err
	}
	return endpoint.URL, nil
}

func parseClientEnvVars(specs []string) ([]*pb.Environment, error) {
	out := make([]*pb.Environment, 0, len(specs))
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		key := spec
		value := ""
		hasValue := false
		if idx := strings.Index(spec, "="); idx >= 0 {
			key = spec[:idx]
			value = spec[idx+1:]
			hasValue = true
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid environment variable %q", spec)
		}
		if !hasValue {
			resolved, ok := os.LookupEnv(key)
			if !ok {
				continue
			}
			value = resolved
		}
		out = append(out, &pb.Environment{Key: key, Value: value})
	}
	return out, nil
}

func parseClientUsername(raw *string) (*pb.Username, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	isNumeric := true
	for _, r := range value {
		if r < '0' || r > '9' {
			isNumeric = false
			break
		}
	}
	if isNumeric {
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid user id %q: %w", value, err)
		}
		return &pb.Username{Payload: &pb.Username_Id{Id: uint32(parsed)}}, nil
	}
	return &pb.Username{Payload: &pb.Username_Name{Name: value}}, nil
}

func (c *clientRuntime) buildHandshakeMessage(transport WebTTYTransport, serverHello *pb.ServerHello) (*pb.Message, error) {
	if c.cfg != nil && c.cfg.Attach != nil {
		return c.buildAttachMessage(transport)
	}
	return c.buildOpenMessage(transport, serverHello)
}

func (c *clientRuntime) buildOpenMessage(transport WebTTYTransport, serverHello *pb.ServerHello) (*pb.Message, error) {
	env, err := parseClientEnvVars(c.cfg.EnvVars)
	if err != nil {
		return nil, err
	}
	if c.cfg.AllocateTTY {
		if termValue, ok := os.LookupEnv("TERM"); ok && termValue != "" {
			hasTERM := false
			for _, e := range env {
				if strings.EqualFold(strings.TrimSpace(e.Key), "TERM") {
					hasTERM = true
					break
				}
			}
			if !hasTERM {
				env = append(env, &pb.Environment{Key: "TERM", Value: termValue})
			}
		}
	}
	username, err := parseClientUsername(c.cfg.Username)
	if err != nil {
		return nil, err
	}
	config := &pb.Config{
		Options: &pb.Options{
			Interactive:   c.cfg.Interactive,
			AllocateTty:   c.cfg.AllocateTTY,
			SendHeartbeat: c.cfg.SendHeartbeat,
		},
		CmdArgs: append([]string(nil), c.cfg.CmdArgs...),
		EnvVars: env,
	}
	if c.cfg.Workdir != nil && strings.TrimSpace(*c.cfg.Workdir) != "" {
		config.Workdir = &pb.Workdir{Value: strings.TrimSpace(*c.cfg.Workdir)}
	}
	if username != nil {
		config.Username = username
	}
	sessionKeyGrant := payloadCryptoSessionKeyGrant(c.cfg.PayloadCrypto)
	open := &pb.Open{
		Config:          config,
		Capabilities:    payloadCryptoCapabilities(c.cfg.PayloadCrypto),
		SessionKeyGrant: sessionKeyGrant,
	}
	clientProof, err := c.clientProofForOpen(open, serverHello, transport)
	if err != nil {
		return nil, err
	}
	open.ClientProof = clientProof
	return &pb.Message{Payload: &pb.Message_Open{Open: open}}, nil
}

func (c *clientRuntime) buildAttachMessage(transport WebTTYTransport) (*pb.Message, error) {
	if c.cfg == nil || c.cfg.Attach == nil {
		return nil, fmt.Errorf("WebTTY attach config is required")
	}
	cfg := c.cfg.Attach
	sessionID := strings.TrimSpace(cfg.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("WebTTY attach session ID is required")
	}
	participantID := strings.TrimSpace(cfg.ParticipantID)
	if participantID == "" {
		return nil, fmt.Errorf("WebTTY attach participant ID is required")
	}
	if len(cfg.AttachGrant) == 0 {
		return nil, fmt.Errorf("WebTTY attach grant is required")
	}
	role, err := attachRoleToProto(cfg.RequestedRole)
	if err != nil {
		return nil, err
	}
	attachTransport := cfg.Transport
	if attachTransport == "" {
		attachTransport = transport
	}
	transportProto, err := attachTransportToProto(attachTransport)
	if err != nil {
		return nil, err
	}
	capabilities, err := attachCapabilitiesToProto(cfg.Capabilities)
	if err != nil {
		return nil, err
	}
	attach := &pb.Attach{
		SessionId:     sessionID,
		ParticipantId: participantID,
		AttachGrant:   cloneBytes(cfg.AttachGrant),
		RequestedRole: role,
		Transport:     transportProto,
		Capabilities:  capabilities,
		DeviceId:      webTTYStringValue(cfg.DeviceID),
		BrowserId:     webTTYStringValue(cfg.BrowserID),
	}
	clientProof, err := c.clientProofForAttach(attach, attachTransport)
	if err != nil {
		return nil, err
	}
	attach.ClientProof = clientProof
	return &pb.Message{Payload: &pb.Message_Attach{Attach: attach}}, nil
}

func attachRoleToProto(role AttachRole) (pb.AttachRole, error) {
	switch role {
	case "", AttachRoleSpectator:
		return pb.AttachRole_ATTACH_ROLE_SPECTATOR, nil
	case AttachRoleController:
		return pb.AttachRole_ATTACH_ROLE_CONTROLLER, nil
	default:
		return pb.AttachRole_ATTACH_ROLE_UNSPECIFIED, fmt.Errorf("invalid WebTTY attach role %q", role)
	}
}

func attachTransportToProto(transport WebTTYTransport) (pb.AttachTransport, error) {
	switch transport {
	case WebTTYTransportPlain:
		return pb.AttachTransport_ATTACH_TRANSPORT_PLAIN, nil
	case "", WebTTYTransportWebSocket:
		return pb.AttachTransport_ATTACH_TRANSPORT_WEBSOCKET, nil
	case WebTTYTransportWebTransport:
		return pb.AttachTransport_ATTACH_TRANSPORT_WEBTRANSPORT, nil
	default:
		return pb.AttachTransport_ATTACH_TRANSPORT_UNSPECIFIED, fmt.Errorf("invalid WebTTY attach transport %q", transport)
	}
}

func attachCapabilitiesToProto(capabilities []AttachCapability) ([]pb.AttachCapability, error) {
	if len(capabilities) == 0 {
		return []pb.AttachCapability{pb.AttachCapability_ATTACH_CAPABILITY_READ_STREAM}, nil
	}
	out := make([]pb.AttachCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		switch capability {
		case AttachCapabilityReadStream:
			out = append(out, pb.AttachCapability_ATTACH_CAPABILITY_READ_STREAM)
		case AttachCapabilityRequestControl:
			out = append(out, pb.AttachCapability_ATTACH_CAPABILITY_REQUEST_CONTROL)
		case AttachCapabilityReceiveControl:
			out = append(out, pb.AttachCapability_ATTACH_CAPABILITY_RECEIVE_CONTROL)
		default:
			return nil, fmt.Errorf("invalid WebTTY attach capability %q", capability)
		}
	}
	return out, nil
}

func (c *clientRuntime) waitForOpen(ctx context.Context, eventCh <-chan clientEvent) error {
	var timer *time.Timer
	var timeout <-chan time.Time
	if c.cfg.OpenDeadline != nil && *c.cfg.OpenDeadline > 0 {
		timer = time.NewTimer(*c.cfg.OpenDeadline)
		timeout = timer.C
		defer func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}()
	}
	for {
		select {
		case event := <-eventCh:
			if event.err != nil {
				return event.err
			}
			return c.handleOpenMessage(event.msg)
		case <-timeout:
			return errClientOperationTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *clientRuntime) handleOpenMessage(msg *pb.Message) error {
	if msg == nil {
		return errClientProtocol
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_Ack:
		return nil
	case *pb.Message_Error:
		if payload.Error != nil && strings.TrimSpace(payload.Error.Msg) != "" {
			return fmt.Errorf("%w: %s", errClientServer, payload.Error.Msg)
		}
		return errClientServer
	case *pb.Message_ServerHello:
		return fmt.Errorf("WebTTY server requires authenticated E2E; configure a known server endpoint identity before opening the session")
	case *pb.Message_ProtocolError:
		return webTTYProtocolError(payload.ProtocolError)
	default:
		return fmt.Errorf("%w: %T", errClientUnexpected, payload)
	}
}

func (c *clientRuntime) handleSessionMessage(ctx context.Context, msg *pb.Message) (int, bool, error) {
	if msg == nil {
		return -1, true, errClientProtocol
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_Data:
		if err := c.handleData(ctx, payload.Data); err != nil {
			return -1, true, err
		}
		return -1, false, nil
	case *pb.Message_Close:
		if payload.Close == nil {
			return -1, true, errClientProtocol
		}
		return int(payload.Close.ReturnCode), true, nil
	case *pb.Message_Heartbeat:
		return -1, false, nil
	case *pb.Message_Error:
		if payload.Error != nil && strings.TrimSpace(payload.Error.Msg) != "" {
			return -1, true, fmt.Errorf("%w: %s", errClientServer, payload.Error.Msg)
		}
		return -1, true, errClientServer
	case *pb.Message_ProtocolError:
		return -1, true, webTTYProtocolError(payload.ProtocolError)
	default:
		return -1, true, fmt.Errorf("%w: %T", errClientUnexpected, payload)
	}
}

func webTTYProtocolError(protocolError *pb.ProtocolError) error {
	msg := webTTYProtocolErrorMessage(protocolError)
	if msg == "" {
		return errClientServer
	}
	return fmt.Errorf("%w: %s", errClientServer, msg)
}

func webTTYProtocolErrorMessage(protocolError *pb.ProtocolError) string {
	if protocolError == nil {
		return ""
	}
	if msg := strings.TrimSpace(protocolError.Msg); msg != "" {
		return msg
	}
	switch protocolError.Code {
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_UNKNOWN_SERVER:
		return "known WebTTY server endpoint identity is required"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_SERVER_KEY_CHANGED:
		return "WebTTY server endpoint identity does not match the configured known server"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_SERVER_PROOF_INVALID:
		return "WebTTY server proof is invalid"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED:
		return "WebTTY client proof is required"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_INVALID:
		return "WebTTY client proof is invalid"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED:
		return "WebTTY client signing key is not authorized"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_WORKSPACE_TRUST_REQUIRED:
		return "workspace trust is required"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_WORKSPACE_DEVICE_UNTRUSTED:
		return "workspace device is not trusted"
	case pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_REGISTERED_SERVER_MISMATCH:
		return "registered WebTTY server identity does not match"
	default:
		return "WebTTY protocol error"
	}
}

func (c *clientRuntime) readLoop(done <-chan struct{}, eventCh chan<- clientEvent) {
	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				select {
				case eventCh <- clientEvent{err: io.EOF}:
				case <-done:
				}
			} else {
				select {
				case eventCh <- clientEvent{err: fmt.Errorf("failed to read websocket message: %w", err)}:
				case <-done:
				}
			}
			return
		}
		if messageType != websocket.BinaryMessage {
			select {
			case eventCh <- clientEvent{err: fmt.Errorf("%w: websocket message type %d", errClientUnexpected, messageType)}:
			case <-done:
			}
			return
		}
		msg := &pb.Message{}
		if err := proto.Unmarshal(payload, msg); err != nil {
			select {
			case eventCh <- clientEvent{err: fmt.Errorf("%w: failed to decode protobuf message: %v", errClientProtocol, err)}:
			case <-done:
			}
			return
		}
		switch msg.Payload.(type) {
		case *pb.Message_Close, *pb.Message_Error, *pb.Message_ProtocolError:
			c.closing.Store(true)
		}
		c.logProtoMessage("received", msg)
		select {
		case eventCh <- clientEvent{msg: msg}:
		case <-done:
			return
		}
	}
}

func (c *clientRuntime) handleData(ctx context.Context, data *pb.Data) error {
	if data == nil {
		return fmt.Errorf("received empty data message")
	}
	var writer io.Writer
	var decrypt PayloadDecryptFunc
	switch data.Type {
	case pb.Data_TYPE_STDOUT:
		writer = c.cfg.Stdout
		if c.cfg.PayloadCrypto != nil {
			decrypt = c.cfg.PayloadCrypto.DecryptStdout
		}
	case pb.Data_TYPE_STDERR:
		writer = c.cfg.Stderr
		if c.cfg.PayloadCrypto != nil {
			decrypt = c.cfg.PayloadCrypto.DecryptStderr
		}
	default:
		return fmt.Errorf("unexpected data stream type: %v", data.Type)
	}
	payload, ok, err := c.decodeStreamPayload(ctx, data.Payload, decrypt, data.Type.String())
	if err != nil || !ok {
		return err
	}
	if err := writeAll(writer, payload); err != nil {
		return fmt.Errorf("failed to write stream payload: %w", err)
	}
	return nil
}

func (c *clientRuntime) stdinDataMessage(ctx context.Context, data []byte) (*pb.Message, error) {
	if c.cfg.PayloadCrypto == nil || c.cfg.PayloadCrypto.EncryptStdin == nil {
		return &pb.Message{
			Payload: &pb.Message_Data{
				Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: cloneBytes(data)}},
			},
		}, nil
	}
	encrypted, err := c.cfg.PayloadCrypto.EncryptStdin(ctx, cloneBytes(data))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt stdin payload: %w", err)
	}
	return &pb.Message{
		Payload: &pb.Message_Data{
			Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_EncryptedData{EncryptedData: encryptedPayloadToProto(encrypted)}},
		},
	}, nil
}

func (c *clientRuntime) decodeClientSessionEvent(ctx context.Context, data *pb.Data) (ClientSessionEvent, bool, error) {
	if data == nil {
		return ClientSessionEvent{}, false, fmt.Errorf("received empty data message")
	}
	var stream ClientSessionStream
	var decrypt PayloadDecryptFunc
	switch data.Type {
	case pb.Data_TYPE_STDOUT:
		stream = ClientSessionStdout
		if c.cfg.PayloadCrypto != nil {
			decrypt = c.cfg.PayloadCrypto.DecryptStdout
		}
	case pb.Data_TYPE_STDERR:
		stream = ClientSessionStderr
		if c.cfg.PayloadCrypto != nil {
			decrypt = c.cfg.PayloadCrypto.DecryptStderr
		}
	default:
		return ClientSessionEvent{}, false, fmt.Errorf("unexpected data stream type: %v", data.Type)
	}
	payload, ok, err := c.decodeStreamPayload(ctx, data.Payload, decrypt, data.Type.String())
	if err != nil || !ok {
		return ClientSessionEvent{}, false, err
	}
	return ClientSessionEvent{Stream: stream, Data: payload}, true, nil
}

func (c *clientRuntime) decodeStreamPayload(ctx context.Context, dataPayload any, decrypt PayloadDecryptFunc, stream string) ([]byte, bool, error) {
	switch payload := dataPayload.(type) {
	case *pb.Data_Data:
		return cloneBytes(payload.Data), true, nil
	case *pb.Data_Eos:
		return nil, false, nil
	case *pb.Data_EncryptedData:
		if decrypt == nil {
			return nil, false, fmt.Errorf("encrypted WebTTY %s payload requires a decrypt hook", stream)
		}
		decrypted, err := decrypt(ctx, encryptedPayloadFromProto(payload.EncryptedData))
		if err != nil {
			return nil, false, fmt.Errorf("failed to decrypt WebTTY %s payload: %w", stream, err)
		}
		return cloneBytes(decrypted), true, nil
	default:
		return nil, false, fmt.Errorf("unexpected data payload type: %T", payload)
	}
}

func (c *clientRuntime) heartbeatLoop(ctx context.Context, errCh chan<- error) {
	interval := *c.cfg.HeartbeatInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msg := &pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}}
			if err := c.writeMessage(msg); err != nil {
				select {
				case errCh <- fmt.Errorf("failed to send heartbeat: %w", err):
				default:
				}
				return
			}
		}
	}
}

func (c *clientRuntime) sendClientError(err error) error {
	if err == nil {
		return nil
	}
	msg := &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: err.Error()}}}
	c.logProtoMessage("sending", msg)
	payload, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}
	if !c.writeMu.TryLock() {
		return errClientWriteBusy
	}
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(c.closeWriteDeadline()); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (c *clientRuntime) writeMessage(msg *pb.Message) error {
	c.logProtoMessage("sending", msg)
	payload, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (c *clientRuntime) closeConn() {
	c.closing.Store(true)
	c.closeOnce.Do(func() {
		deadline := c.closeWriteDeadline()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), deadline)
		_ = c.conn.Close()
	})
}

func (c *clientRuntime) closeWriteDeadline() time.Time {
	timeout := defaultClientCloseDeadline
	if c.cfg != nil && c.cfg.CloseDeadline != nil {
		timeout = *c.cfg.CloseDeadline
	}
	if timeout < 0 {
		timeout = 0
	}
	return time.Now().Add(timeout)
}

func (c *clientRuntime) logProtoMessage(direction string, msg *pb.Message) {
	if !c.logProto {
		return
	}
	c.logger.Debug("protobuf message", "direction", direction, "payload_type", webTTYMessageType(msg))
}

func writeAll(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return fmt.Errorf("writer returned %d bytes", n)
		}
		payload = payload[n:]
	}
	return nil
}

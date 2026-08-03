// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type session struct {
	conn            messageConn
	cfg             *ServerConfig
	logger          *slog.Logger
	sessionID       string
	transport       WebTTYTransport
	ctx             context.Context
	cancel          context.CancelFunc
	logProto        bool
	mu              sync.Mutex
	writeMu         sync.Mutex
	closed          bool
	opening         bool
	shutdownReq     bool
	cmd             *exec.Cmd
	ptyFile         *os.File
	stdinPipe       io.WriteCloser
	stdinClosed     bool
	doneCh          chan struct{}
	openTimer       *time.Timer
	closeTimer      *time.Timer
	heartbeatTicker *time.Ticker
	streamsActive   int
	childDone       bool
	childExitCode   int
	opened          bool
	disconnecting   bool
	closeErr        error
	payloadCrypto   *PayloadCrypto
	serverNonce     []byte
	acceptedAt      time.Time
	authenticatedAt time.Time
	acceptedLogged  bool
	clientPrincipal string
	clientDeviceID  string
	clientBrowserID string
	clientKeyID     string
	serverKeyID     string
}

type sessionCleanup struct {
	logger     *slog.Logger
	conn       messageConn
	cmd        *exec.Cmd
	ptyFile    *os.File
	stdinPipe  io.WriteCloser
	doneCh     chan struct{}
	attrs      []any
	childDone  bool
	closeFrame []byte
}

type sessionOpenResources struct {
	cmd           *exec.Cmd
	ptyFile       *os.File
	stdinPipe     io.WriteCloser
	stdout        io.Reader
	stderr        io.Reader
	payloadCrypto *PayloadCrypto
	streamsActive int
	allocateTTY   bool
}

func (r *sessionOpenResources) close() {
	if r == nil {
		return
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
	}
	if r.ptyFile != nil {
		_ = r.ptyFile.Close()
	}
	if r.stdinPipe != nil {
		_ = r.stdinPipe.Close()
	}
	if closer, ok := r.stdout.(io.Closer); ok {
		_ = closer.Close()
	}
	if closer, ok := r.stderr.(io.Closer); ok {
		_ = closer.Close()
	}
}

const managedAttachUnsupportedMessage = "managed WebTTY attach is handled by the rstream engine; direct WebTTY servers accept only new Open sessions"

var errSessionOperationTimeout = errors.New("operation timeout")

func newSession(conn messageConn, cfg *ServerConfig, logger *slog.Logger, sessionID string, transport WebTTYTransport) *session {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.MaxMessageSize != nil && *cfg.MaxMessageSize > 0 {
		conn.SetReadLimit(*cfg.MaxMessageSize)
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	s := &session{
		conn:          conn,
		cfg:           cfg,
		logger:        logger,
		sessionID:     sessionID,
		transport:     transport,
		ctx:           sessionCtx,
		cancel:        cancel,
		logProto:      strings.EqualFold(strings.TrimSpace(rstream.Channel), "dev"),
		doneCh:        make(chan struct{}),
		payloadCrypto: cfg.PayloadCrypto,
		acceptedAt:    time.Now(),
	}
	if cfg.HeartbeatInterval != nil && *cfg.HeartbeatInterval > 0 {
		s.heartbeatTicker = time.NewTicker(*cfg.HeartbeatInterval)
	}
	if cfg.SessionOpenDeadline != nil && *cfg.SessionOpenDeadline > 0 {
		timer := time.AfterFunc(*cfg.SessionOpenDeadline, func() {
			s.onOpenTimeout()
		})
		s.mu.Lock()
		if s.closed {
			timer.Stop()
		} else {
			s.openTimer = timer
		}
		s.mu.Unlock()
	}
	return s
}

func (s *session) run() {
	if err := s.validateE2EConfig(); err != nil {
		s.error(err)
		return
	}
	initial, err := s.readWebTransportInitialMessageIfNeeded()
	if err != nil {
		s.error(err)
		return
	}
	if err := s.sendServerHelloIfConfigured(); err != nil {
		s.error(err)
		return
	}
	go s.readLoop(initial)
	<-s.doneCh
}

func (s *session) validateE2EConfig() error {
	if s.cfg == nil || s.cfg.PayloadCryptoResolver == nil {
		return nil
	}
	if s.cfg.EndpointIdentity == nil {
		return fmt.Errorf("WebTTY E2E server requires an endpoint identity")
	}
	if s.cfg.RequireClientProof == nil || !*s.cfg.RequireClientProof {
		return fmt.Errorf("WebTTY E2E server requires client proof")
	}
	return nil
}

func (s *session) done() <-chan struct{} {
	return s.doneCh
}

func (s *session) shutdown(ctx context.Context) {
	s.mu.Lock()
	done := s.done()
	if s.closed {
		s.mu.Unlock()
		s.waitForCleanup(ctx, done)
		return
	}
	s.shutdownReq = true
	cmd := s.cmd
	childDone := s.childDone
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		s.close()
		return
	}
	if !childDone {
		if err := signalChildInterrupt(cmd); err != nil {
			s.logger.Debug("failed to send child interrupt", "error", err)
		} else {
			s.logger.Debug("requested child process shutdown")
		}
	}
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	s.mu.Lock()
	cmd = s.cmd
	childDone = s.childDone
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil && !childDone {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			s.logger.Warn("failed to force kill child process", "error", err)
		}
	}
	s.close()
	s.waitForCleanup(ctx, done)
}

func (s *session) waitForCleanup(ctx context.Context, done <-chan struct{}) {
	select {
	case <-done:
		return
	case <-ctx.Done():
		_ = s.conn.Close()
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func signalChildInterrupt(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := interruptChildProcess(cmd); err != nil {
		return err
	}
	return nil
}

func (s *session) readLoop(initial *pb.Message) {
	if initial != nil {
		err := s.handleMessage(initial)
		if err != nil {
			s.error(err)
		}
		if err != nil || s.isClosed() {
			return
		}
	}
	for {
		mt, data, err := s.conn.ReadMessage()
		if !s.onIncomingMessage(mt, data, err) {
			return
		}
	}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) readWebTransportInitialMessageIfNeeded() (*pb.Message, error) {
	if s.transport != WebTTYTransportWebTransport || s.cfg == nil || s.cfg.EndpointIdentity == nil {
		return nil, nil
	}
	mt, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.BinaryMessage {
		return nil, fmt.Errorf("unexpected message type: %d", mt)
	}
	var msg pb.Message
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal protobuf: %w", err)
	}
	s.logProtoMessage("received", &msg)
	if _, ok := msg.Payload.(*pb.Message_ClientHello); ok {
		return nil, nil
	}
	return &msg, nil
}

func (s *session) heartbeatLoop() {
	if s.heartbeatTicker == nil {
		return
	}
	defer s.heartbeatTicker.Stop()
	for {
		select {
		case <-s.doneCh:
			return
		case <-s.heartbeatTicker.C:
			if !s.doSendHeartbeat() {
				return
			}
		}
	}
}

func (s *session) copyStreamLoop(r io.Reader, t pb.Data_Type) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 && !s.onReadStream(t, buf[:n], nil) {
			return
		}
		if err != nil && !s.onReadStream(t, nil, err) {
			return
		}
	}
}

func (s *session) copyStdoutLoop(r io.Reader) {
	s.copyStreamLoop(r, pb.Data_TYPE_STDOUT)
}

func (s *session) copyStderrLoop(r io.Reader) {
	s.copyStreamLoop(r, pb.Data_TYPE_STDERR)
}

func (s *session) waitProcessLoop(cmd *exec.Cmd) {
	err := cmd.Wait()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
			err = nil
		}
	}
	_ = s.onChildExit(exitCode, err)
}

func (s *session) onIncomingMessage(messageType int, p []byte, err error) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	expectedClose := err != nil && (isExpectedWebTTYPeerCloseError(err) || s.disconnecting || s.shutdownReq)
	s.mu.Unlock()
	if expectedClose {
		s.close()
		return false
	}
	if err == nil && messageType != websocket.BinaryMessage {
		err = fmt.Errorf("unexpected message type: %d", messageType)
	}
	var msg pb.Message
	if err == nil {
		if err = proto.Unmarshal(p, &msg); err != nil {
			err = fmt.Errorf("failed to unmarshal protobuf: %w", err)
		}
	}
	if err == nil {
		s.logProtoMessage("received", &msg)
		err = s.handleMessage(&msg)
	}
	if err != nil {
		s.error(err)
	}
	return err == nil
}

func (s *session) doSendHeartbeat() bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	err := s.sendHeartbeat()
	if err != nil {
		s.error(err)
	}
	return err == nil
}

func isExpectedWebTTYPeerCloseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
		return true
	}
	return websocket.IsCloseError(
		err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseAbnormalClosure,
	)
}

func (s *session) onReadStream(t pb.Data_Type, p []byte, err error) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	eos := isStreamEOS(err, s.ptyFile != nil)
	s.mu.Unlock()
	if err != nil {
		s.logger.Debug("stream closed", "stream", t.String(), "eos", eos, "error", err)
	}
	if eos {
		err = s.sendEOS(t)
		if err == nil {
			s.mu.Lock()
			if s.streamsActive > 0 {
				s.streamsActive--
			}
			shouldClose := s.streamsActive == 0 && s.childDone
			s.mu.Unlock()
			if shouldClose {
				err = s.doClose()
			}
		}
	} else if err == nil {
		chunk := append([]byte(nil), p...)
		err = s.sendData(t, chunk)
	}
	if err != nil {
		s.error(err)
	}
	return err == nil && !eos
}

func (s *session) onChildExit(exitCode int, err error) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if err == nil {
		s.childDone = true
		s.childExitCode = exitCode
		shouldClose := s.streamsActive == 0
		s.mu.Unlock()
		s.logger.Info("child process exited", "exit_code", exitCode)
		if shouldClose {
			err = s.doClose()
		}
	} else {
		s.mu.Unlock()
	}
	if err != nil {
		s.error(err)
	}
	return err == nil
}

func (s *session) handleMessage(m *pb.Message) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	s.mu.Unlock()
	if m == nil {
		return errors.New("missing message")
	}
	switch payload := m.Payload.(type) {
	case *pb.Message_Open:
		return s.handleOpen(payload.Open)
	case *pb.Message_Attach:
		err := errors.New(managedAttachUnsupportedMessage)
		_ = s.sendError(err)
		return err
	case *pb.Message_Data:
		return s.handleData(payload.Data)
	case *pb.Message_Error:
		if payload.Error == nil || strings.TrimSpace(payload.Error.Msg) == "" {
			return fmt.Errorf("client error")
		}
		return fmt.Errorf("client error (%s)", payload.Error.Msg)
	case *pb.Message_Heartbeat:
		return nil
	case *pb.Message_ClientHello:
		return nil
	case *pb.Message_Parameter:
		if payload.Parameter == nil {
			return fmt.Errorf("missing parameter")
		}
		switch parameter := payload.Parameter.Parameter.(type) {
		case *pb.Parameter_TerminalSize:
			return s.doResize(parameter.TerminalSize)
		default:
			return fmt.Errorf("unexpected parameter type: %T", parameter)
		}
	default:
		return fmt.Errorf("unexpected message payload type: %T", payload)
	}
}

func (s *session) waitPipedProcessLoop(cmd *exec.Cmd, stdout, stderr io.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.copyStdoutLoop(stdout)
	}()
	go func() {
		defer wg.Done()
		s.copyStderrLoop(stderr)
	}()
	wg.Wait()
	s.waitProcessLoop(cmd)
}

func usernameVariantFromProto(username *pb.Username) *UsernameVariant {
	if username == nil {
		return nil
	}
	switch p := username.Payload.(type) {
	case *pb.Username_Name:
		return &UsernameVariant{Name: &p.Name}
	case *pb.Username_Id:
		id := p.Id
		return &UsernameVariant{UID: &id}
	default:
		return nil
	}
}

func (s *session) handleOpen(openCfg *pb.Open) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	if s.cmd != nil || s.opening {
		s.mu.Unlock()
		return errors.New("process already started")
	}
	s.opening = true
	s.mu.Unlock()
	resources, err := s.prepareOpen(openCfg)
	if err != nil {
		s.mu.Lock()
		s.opening = false
		closed := s.closed
		s.mu.Unlock()
		s.logSessionRejected(err)
		if closed {
			return err
		}
		if sendErr := s.sendError(err); sendErr != nil {
			return sendErr
		}
		s.startCloseHandshake()
		return nil
	}
	s.mu.Lock()
	s.opening = false
	if s.closed {
		s.mu.Unlock()
		resources.close()
		return errors.New("session is closed")
	}
	s.payloadCrypto = resources.payloadCrypto
	s.ptyFile = resources.ptyFile
	s.stdinPipe = resources.stdinPipe
	s.cmd = resources.cmd
	s.streamsActive = resources.streamsActive
	s.opened = true
	if s.openTimer != nil {
		s.openTimer.Stop()
		s.openTimer = nil
	}
	shutdownReq := s.shutdownReq
	startHeartbeat := s.heartbeatTicker != nil
	s.mu.Unlock()
	if shutdownReq {
		if err := signalChildInterrupt(resources.cmd); err != nil {
			s.logger.Debug("failed to send child interrupt", "error", err)
		} else {
			s.logger.Debug("requested child process shutdown")
		}
	}
	if resources.allocateTTY {
		go s.waitProcessLoop(resources.cmd)
		go s.copyStdoutLoop(resources.ptyFile)
	} else {
		go s.waitPipedProcessLoop(resources.cmd, resources.stdout, resources.stderr)
	}
	if startHeartbeat {
		go s.heartbeatLoop()
	}
	return s.sendAck()
}

func (s *session) prepareOpen(openCfg *pb.Open) (*sessionOpenResources, error) {
	if openCfg == nil || openCfg.Config == nil {
		return nil, fmt.Errorf("missing open config")
	}
	if err := s.verifyClientProof(s.ctx, openCfg); err != nil {
		return nil, err
	}
	s.logSessionAccepted()
	uv := usernameVariantFromProto(openCfg.Config.Username)
	identity, err := resolveExecutionIdentity(s.cfg, uv)
	if err != nil {
		return nil, err
	}
	resources := &sessionOpenResources{}
	requireSessionKeyGrant := s.cfg.RequireSessionKeyGrant != nil && *s.cfg.RequireSessionKeyGrant
	if openCfg.SessionKeyGrant == nil && requireSessionKeyGrant {
		return nil, fmt.Errorf("E2E session key grant is required")
	}
	if openCfg.SessionKeyGrant != nil && s.cfg.PayloadCryptoResolver == nil {
		return nil, fmt.Errorf("E2E session key grant was provided but no resolver is configured")
	}
	if openCfg.SessionKeyGrant != nil {
		resources.payloadCrypto, err = s.cfg.PayloadCryptoResolver(s.ctx, sessionKeyGrantFromProto(openCfg.SessionKeyGrant))
		if err != nil {
			return nil, fmt.Errorf("failed to resolve session key grant: %w", err)
		}
	} else {
		resources.payloadCrypto = s.cfg.PayloadCrypto
	}
	ui := identity.userInfo
	workdir := ui.Home
	if wd := openCfg.Config.Workdir; wd != nil && wd.Value != "" {
		workdir = wd.Value
	}
	cmdArgs := openCfg.Config.CmdArgs
	if len(cmdArgs) == 0 {
		cmdArgs = []string{ui.Shell}
	}
	exe, args := cmdArgs[0], cmdArgs[1:]
	resources.allocateTTY = openCfg.Config.Options.GetAllocateTty()
	backend := "pipe"
	if resources.allocateTTY {
		backend = "tty"
	}
	s.logger.Info("starting child process", "backend", backend, "exe", exe, "args_count", len(args), "workdir", workdir, "execution_mode", executionModeLogValue(s.cfg))
	exePath, err := resolveExecutable(exe, workdir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exePath, args...)
	cmd.Dir = workdir
	env := BuildEnvironment(openCfg.Config.EnvVars)
	addExecutionEnvironment(&env, identity)
	for key, value := range *s.cfg.EnvVars {
		AddEnvironmentVariable(&env, key, value, false)
	}
	cmd.Env = env
	if identity.credentialRequired {
		if err := SetupCredential(cmd, ui); err != nil {
			return nil, fmt.Errorf("failed to switch user: %w", err)
		}
	}
	resources.cmd = cmd
	if resources.allocateTTY {
		resources.ptyFile, err = pty.Start(cmd)
		if err != nil {
			resources.close()
			return nil, err
		}
		resources.streamsActive = 1
		return resources, nil
	}
	resources.stdout, err = cmd.StdoutPipe()
	if err != nil {
		resources.close()
		return nil, err
	}
	resources.stderr, err = cmd.StderrPipe()
	if err != nil {
		resources.close()
		return nil, err
	}
	resources.stdinPipe, err = cmd.StdinPipe()
	if err != nil {
		resources.close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		resources.close()
		return nil, err
	}
	resources.streamsActive = 2
	return resources, nil
}

func (s *session) handleData(d *pb.Data) error {
	if d == nil {
		return errors.New("missing data message")
	}
	if d.Type != pb.Data_TYPE_STDIN {
		return fmt.Errorf("unexpected data type: %v", d.Type)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	ptyFile := s.ptyFile
	stdinPipe := s.stdinPipe
	stdinClosed := s.stdinClosed
	payloadCrypto := s.payloadCrypto
	ctx := s.ctx
	if _, ok := d.Payload.(*pb.Data_Eos); ok && ptyFile == nil && !stdinClosed && stdinPipe != nil {
		s.stdinClosed = true
		s.stdinPipe = nil
	}
	s.mu.Unlock()
	switch payload := d.Payload.(type) {
	case *pb.Data_Eos:
		if ptyFile != nil {
			return nil
		}
		if stdinClosed {
			return nil
		}
		if stdinPipe != nil {
			s.logger.Debug("closing stdin after receiving eos from peer")
			if err := stdinPipe.Close(); err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
				return err
			}
			return nil
		}
	case *pb.Data_Data:
		if stdinClosed {
			return nil
		}
		return writeStdinPayload(ptyFile, stdinPipe, payload.Data)
	case *pb.Data_EncryptedData:
		if stdinClosed {
			return nil
		}
		if payloadCrypto == nil || payloadCrypto.DecryptStdin == nil {
			return fmt.Errorf("encrypted WebTTY stdin payload requires a decrypt hook")
		}
		decrypted, err := payloadCrypto.DecryptStdin(ctx, encryptedPayloadFromProto(payload.EncryptedData))
		if err != nil {
			return fmt.Errorf("failed to decrypt stdin payload: %w", err)
		}
		return writeStdinPayload(ptyFile, stdinPipe, decrypted)
	default:
		return fmt.Errorf("unexpected data payload type: %T", payload)
	}
	return errors.New("no PTY and no stdin pipe")
}

func writeStdinPayload(ptyFile *os.File, stdinPipe io.Writer, payload []byte) error {
	if ptyFile != nil {
		_, err := ptyFile.Write(payload)
		return err
	}
	if stdinPipe != nil {
		_, err := stdinPipe.Write(payload)
		return err
	}
	return errors.New("no PTY and no stdin pipe")
}

func (s *session) doResize(ts *pb.TerminalSize) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	ptyFile := s.ptyFile
	s.mu.Unlock()
	if ts == nil {
		return fmt.Errorf("missing terminal size")
	}
	if ptyFile == nil {
		return fmt.Errorf("no PTY file descriptor")
	}
	return pty.Setsize(ptyFile, &pty.Winsize{Rows: uint16(ts.Row), Cols: uint16(ts.Col)})
}

func (s *session) doClose() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	if !s.childDone {
		s.mu.Unlock()
		return errors.New("child process is still running")
	}
	if s.streamsActive > 0 {
		s.mu.Unlock()
		return errors.New("streams are still active")
	}
	exitCode := s.childExitCode
	s.mu.Unlock()
	err := s.sendClose(exitCode)
	if err == nil {
		s.startCloseHandshake()
	}
	return err
}

func (s *session) error(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.closeErr == nil {
		s.closeErr = err
	}
	s.mu.Unlock()
	s.logger.Warn("session error", "reason_code", webTTYSessionErrorReasonCode(err), "error", err)
	s.close()
}

func (s *session) close() {
	s.mu.Lock()
	cleanup := s.detachCleanupLocked()
	s.mu.Unlock()
	if cleanup != nil {
		cleanup.run()
	}
}

func (s *session) detachCleanupLocked() *sessionCleanup {
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.openTimer != nil {
		s.openTimer.Stop()
	}
	if s.closeTimer != nil {
		s.closeTimer.Stop()
	}
	if s.heartbeatTicker != nil {
		s.heartbeatTicker.Stop()
	}
	attrs := s.sessionAuditAttrsLocked()
	attrs = append(attrs,
		"duration_ms", time.Since(s.acceptedAt).Milliseconds(),
		"opened", s.opened,
		"authenticated", !s.authenticatedAt.IsZero(),
		"child_done", s.childDone,
		"exit_code", s.childExitCode,
	)
	attrs = appendSessionPolicyAttrs(attrs, s.cfg)
	if s.closeErr != nil {
		attrs = append(attrs, "reason_code", webTTYSessionErrorReasonCode(s.closeErr), "error", s.closeErr)
	}
	return &sessionCleanup{
		logger:     s.logger,
		conn:       s.conn,
		cmd:        s.cmd,
		ptyFile:    s.ptyFile,
		stdinPipe:  s.stdinPipe,
		doneCh:     s.doneCh,
		attrs:      attrs,
		childDone:  s.childDone,
		closeFrame: websocket.FormatCloseMessage(websocket.CloseNormalClosure, "finished"),
	}
}

func (c *sessionCleanup) run() {
	if c.cmd != nil && c.cmd.Process != nil && !c.childDone {
		if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			c.logger.Debug("failed to kill child on close", "error", err)
		}
	}
	if c.ptyFile != nil {
		_ = c.ptyFile.Close()
	}
	if c.stdinPipe != nil {
		_ = c.stdinPipe.Close()
	}
	_ = c.conn.WriteControl(websocket.CloseMessage, c.closeFrame, time.Now().Add(time.Second))
	_ = c.conn.Close()
	close(c.doneCh)
	c.logger.Info("session closed", c.attrs...)
}

func (s *session) onOpenTimeout() {
	s.mu.Lock()
	if s.closed || s.opened {
		s.mu.Unlock()
		return
	}
	if s.closeErr == nil {
		s.closeErr = errSessionOperationTimeout
	}
	err := s.closeErr
	s.mu.Unlock()
	s.logger.Warn("session open timeout", "error", err)
	s.close()
}

func (s *session) onCloseTimeout() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.closeErr == nil {
		s.closeErr = errSessionOperationTimeout
	}
	err := s.closeErr
	s.mu.Unlock()
	s.logger.Warn("session close timeout", "error", err)
	s.close()
}

func (s *session) startCloseHandshake() {
	s.mu.Lock()
	if s.closed || s.disconnecting {
		s.mu.Unlock()
		return
	}
	s.disconnecting = true
	closeDeadline := time.Duration(0)
	if s.cfg != nil && s.cfg.SessionCloseDeadline != nil {
		closeDeadline = *s.cfg.SessionCloseDeadline
	}
	if closeDeadline > 0 {
		s.closeTimer = time.AfterFunc(closeDeadline, s.onCloseTimeout)
	}
	deadline := time.Now().Add(time.Second)
	if closeDeadline > 0 {
		deadline = time.Now().Add(closeDeadline)
	}
	s.mu.Unlock()
	if err := s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "finished"), deadline); err != nil {
		s.close()
	}
}

func (s *session) sendAck() error {
	msg := &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}}
	return s.writeMessage(msg)
}

func (s *session) sendError(err error) error {
	if protocolError, ok := webTTYProtocolErrorForError(err); ok {
		msg := &pb.Message{Payload: &pb.Message_ProtocolError{ProtocolError: protocolError}}
		return s.writeMessage(msg)
	}
	msg := &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: err.Error()}}}
	return s.writeMessage(msg)
}

func webTTYProtocolErrorForError(err error) (*pb.ProtocolError, bool) {
	switch {
	case errors.Is(err, errWebTTYClientProofRequired):
		return &pb.ProtocolError{
			Code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED,
			Msg:  err.Error(),
		}, true
	case errors.Is(err, errWebTTYClientProofInvalid):
		return &pb.ProtocolError{
			Code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_INVALID,
			Msg:  err.Error(),
		}, true
	case errors.Is(err, errWebTTYClientProofUnauthorized):
		return &pb.ProtocolError{
			Code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED,
			Msg:  err.Error(),
		}, true
	default:
		return nil, false
	}
}

func (s *session) sendHeartbeat() error {
	msg := &pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}}
	return s.writeMessage(msg)
}

func (s *session) sendData(t pb.Data_Type, chunk []byte) error {
	msg, err := s.dataMessage(s.ctx, t, chunk)
	if err != nil {
		return err
	}
	return s.writeMessage(msg)
}

func (s *session) dataMessage(ctx context.Context, t pb.Data_Type, chunk []byte) (*pb.Message, error) {
	var encrypt PayloadEncryptFunc
	s.mu.Lock()
	payloadCrypto := s.payloadCrypto
	s.mu.Unlock()
	if payloadCrypto != nil {
		switch t {
		case pb.Data_TYPE_STDOUT:
			encrypt = payloadCrypto.EncryptStdout
		case pb.Data_TYPE_STDERR:
			encrypt = payloadCrypto.EncryptStderr
		}
	}
	if encrypt == nil {
		return &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: t, Payload: &pb.Data_Data{Data: cloneBytes(chunk)}}}}, nil
	}
	encrypted, err := encrypt(ctx, cloneBytes(chunk))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt %s payload: %w", t.String(), err)
	}
	return &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: t, Payload: &pb.Data_EncryptedData{EncryptedData: encryptedPayloadToProto(encrypted)}}}}, nil
}

func (s *session) sendEOS(t pb.Data_Type) error {
	msg := &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: t, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}}}}
	return s.writeMessage(msg)
}

func (s *session) sendClose(code int) error {
	msg := &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: int32(code)}}}
	return s.writeMessage(msg)
}

func (s *session) writeMessage(m *pb.Message) error {
	s.logProtoMessage("sending", m)
	data, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("session is closed")
	}
	closeDeadline := time.Duration(0)
	if s.cfg != nil && s.cfg.SessionCloseDeadline != nil {
		closeDeadline = *s.cfg.SessionCloseDeadline
	}
	s.mu.Unlock()
	if closeDeadline > 0 {
		if err := s.conn.SetWriteDeadline(time.Now().Add(closeDeadline)); err != nil {
			return err
		}
		defer s.conn.SetWriteDeadline(time.Time{})
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *session) logProtoMessage(direction string, m *pb.Message) {
	if !s.logProto {
		return
	}
	s.logger.Debug("protobuf message", "direction", direction, "payload_type", webTTYMessageType(m))
}

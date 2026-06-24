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
	"runtime"
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
	closed          bool
	shutdownReq     bool
	cmd             *exec.Cmd
	ptyFile         *os.File
	stdinPipe       io.WriteCloser
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
			s.mu.Lock()
			defer s.mu.Unlock()
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
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.shutdownReq = true
	done := s.done()
	if s.cmd == nil || s.cmd.Process == nil {
		s.close()
		s.mu.Unlock()
		return
	}
	if err := s.signalChildInterruptLocked(); err != nil {
		s.logger.Debug("failed to send child interrupt", "error", err)
	}
	s.mu.Unlock()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	s.mu.Lock()
	if !s.closed {
		if s.cmd != nil && s.cmd.Process != nil && !s.childDone {
			if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				s.logger.Warn("failed to force kill child process", "error", err)
			}
		}
		s.close()
	}
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
	}
}

func (s *session) readLoop(initial *pb.Message) {
	if initial != nil {
		s.mu.Lock()
		err := s.handleMessage(initial)
		if err != nil {
			s.error(err)
		}
		closed := s.closed
		s.mu.Unlock()
		if err != nil || closed {
			return
		}
	}
	for {
		mt, data, err := s.conn.ReadMessage()
		s.mu.Lock()
		ok := s.onIncomingMessage(mt, data, err)
		s.mu.Unlock()
		if !ok {
			return
		}
	}
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
			s.mu.Lock()
			ok := s.doSendHeartbeat()
			s.mu.Unlock()
			if !ok {
				return
			}
		}
	}
}

func (s *session) copyStreamLoop(r io.Reader, t pb.Data_Type) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.mu.Lock()
			ok := s.onReadStream(t, buf[:n], nil)
			s.mu.Unlock()
			if !ok {
				return
			}
		}
		s.mu.Lock()
		ok := err == nil || s.onReadStream(t, nil, err)
		s.mu.Unlock()
		if !ok {
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
	s.mu.Lock()
	_ = s.onChildExit(exitCode, err)
	s.mu.Unlock()
}

func (s *session) onIncomingMessage(messageType int, p []byte, err error) bool {
	if s.closed {
		return false
	}
	if err != nil && (isExpectedWebTTYPeerCloseError(err) || s.disconnecting || s.shutdownReq) {
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
	if s.closed {
		return false
	}
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
	if s.closed {
		return false
	}
	eos := isStreamEOS(err, s.ptyFile != nil)
	if err != nil {
		s.logger.Debug("stream closed", "stream", t.String(), "eos", eos, "error", err)
	}
	if eos {
		err = s.sendEOS(t)
		if err == nil {
			if s.streamsActive > 0 {
				s.streamsActive--
			}
			if s.streamsActive == 0 && s.childDone {
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
	if s.closed {
		return false
	}
	if err == nil {
		s.childDone = true
		s.childExitCode = exitCode
		s.logger.Info("child process exited", "exit_code", exitCode, "error", err)
		if s.streamsActive == 0 {
			err = s.doClose()
		}
	}
	if err != nil {
		s.error(err)
	}
	return err == nil
}

func (s *session) handleMessage(m *pb.Message) error {
	if s.closed {
		return errors.New("session is closed")
	}
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
	if s.closed {
		return errors.New("session is closed")
	}
	if s.cmd != nil {
		return errors.New("process already started")
	}
	setup := func() error {
		if openCfg == nil || openCfg.Config == nil {
			return fmt.Errorf("missing open config")
		}
		if err := s.verifyClientProof(s.ctx, openCfg); err != nil {
			return err
		}
		s.logSessionAccepted()
		uv := usernameVariantFromProto(openCfg.Config.Username)
		identity, err := resolveExecutionIdentity(s.cfg, uv)
		if err != nil {
			return err
		}
		requireSessionKeyGrant := s.cfg.RequireSessionKeyGrant != nil && *s.cfg.RequireSessionKeyGrant
		if openCfg.SessionKeyGrant == nil && requireSessionKeyGrant {
			return fmt.Errorf("E2E session key grant is required")
		}
		if openCfg.SessionKeyGrant != nil && s.cfg.PayloadCryptoResolver == nil {
			return fmt.Errorf("E2E session key grant was provided but no resolver is configured")
		}
		if openCfg.SessionKeyGrant != nil && s.cfg.PayloadCryptoResolver != nil {
			payloadCrypto, err := s.cfg.PayloadCryptoResolver(s.ctx, sessionKeyGrantFromProto(openCfg.SessionKeyGrant))
			if err != nil {
				return fmt.Errorf("failed to resolve session key grant: %w", err)
			}
			s.payloadCrypto = payloadCrypto
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
		backend := "pipe"
		if openCfg.Config.Options.GetAllocateTty() {
			backend = "tty"
		}
		s.logger.Info(
			"starting child process",
			"backend", backend,
			"exe", exe,
			"args_count", len(args),
			"workdir", workdir,
			"execution_mode", executionModeLogValue(s.cfg),
		)
		exePath, err := resolveExecutable(exe, workdir)
		if err != nil {
			return err
		}
		cmd := exec.Command(exePath, args...)
		cmd.Dir = workdir
		env := BuildEnvironment(openCfg.Config.EnvVars)
		if p := os.Getenv("PATH"); p != "" {
			AddEnvironmentVariable(&env, "PATH", p, false)
		}
		if runtime.GOOS == "windows" {
			for _, key := range []string{"ALLUSERSPROFILE", "COMPUTERNAME", "COMSPEC", "CYGWIN", "OS", "PATHEXT", "PROGRAMFILES", "SYSTEMDRIVE", "SYSTEMROOT", "TEMP", "TMP", "USERNAME", "USERPROFILE", "WINDIR"} {
				if value := os.Getenv(key); value != "" {
					AddEnvironmentVariable(&env, key, value, false)
				}
			}
		} else {
			AddEnvironmentVariable(&env, "USER", ui.Name, false)
			AddEnvironmentVariable(&env, "SHELL", ui.Shell, false)
			AddEnvironmentVariable(&env, "HOME", ui.Home, false)
		}
		for key, value := range *s.cfg.EnvVars {
			AddEnvironmentVariable(&env, key, value, false)
		}
		cmd.Env = env
		if identity.credentialRequired {
			if err := SetupCredential(cmd, ui); err != nil {
				return fmt.Errorf("failed to switch user: %w", err)
			}
		}
		allocateTty := openCfg.Config.Options.GetAllocateTty()
		if allocateTty {
			ptmx, err := pty.Start(cmd)
			if err != nil {
				return err
			}
			s.ptyFile = ptmx
			s.cmd = cmd
			s.streamsActive = 1
			go s.waitProcessLoop(cmd)
			if s.shutdownReq {
				if err := s.signalChildInterruptLocked(); err != nil {
					s.logger.Debug("failed to send child interrupt", "error", err)
				}
			}
			go s.copyStdoutLoop(ptmx)
		} else {
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return err
			}
			stderr, err := cmd.StderrPipe()
			if err != nil {
				return err
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				return err
			}
			s.stdinPipe = stdin
			s.cmd = cmd
			if err := cmd.Start(); err != nil {
				return err
			}
			s.streamsActive = 2
			if s.shutdownReq {
				if err := s.signalChildInterruptLocked(); err != nil {
					s.logger.Debug("failed to send child interrupt", "error", err)
				}
			}
			go s.waitPipedProcessLoop(cmd, stdout, stderr)
		}
		return nil
	}
	err := setup()
	if err != nil {
		s.logSessionRejected(err)
		if sendErr := s.sendError(err); sendErr != nil {
			return sendErr
		}
		s.startCloseHandshake()
		return nil
	}
	s.opened = true
	if s.openTimer != nil {
		s.openTimer.Stop()
		s.openTimer = nil
	}
	if s.heartbeatTicker != nil {
		go s.heartbeatLoop()
	}
	return s.sendAck()
}

func (s *session) handleData(d *pb.Data) error {
	if s.closed {
		return errors.New("session is closed")
	}
	if d == nil {
		return errors.New("missing data message")
	}
	if d.Type != pb.Data_TYPE_STDIN {
		return fmt.Errorf("unexpected data type: %v", d.Type)
	}
	switch payload := d.Payload.(type) {
	case *pb.Data_Eos:
		if s.ptyFile != nil {
			return nil
		}
		if s.stdinPipe != nil {
			s.logger.Debug("closing stdin after receiving eos from peer")
			return s.stdinPipe.Close()
		}
	case *pb.Data_Data:
		return s.writeStdinPayload(payload.Data)
	case *pb.Data_EncryptedData:
		if s.payloadCrypto == nil || s.payloadCrypto.DecryptStdin == nil {
			return fmt.Errorf("encrypted WebTTY stdin payload requires a decrypt hook")
		}
		decrypted, err := s.payloadCrypto.DecryptStdin(s.ctx, encryptedPayloadFromProto(payload.EncryptedData))
		if err != nil {
			return fmt.Errorf("failed to decrypt stdin payload: %w", err)
		}
		return s.writeStdinPayload(decrypted)
	default:
		return fmt.Errorf("unexpected data payload type: %T", payload)
	}
	return errors.New("no PTY and no stdin pipe")
}

func (s *session) writeStdinPayload(payload []byte) error {
	if s.ptyFile != nil {
		_, err := s.ptyFile.Write(payload)
		return err
	}
	if s.stdinPipe != nil {
		_, err := s.stdinPipe.Write(payload)
		return err
	}
	return errors.New("no PTY and no stdin pipe")
}

func (s *session) doResize(ts *pb.TerminalSize) error {
	if s.closed {
		return errors.New("session is closed")
	}
	if ts == nil {
		return fmt.Errorf("missing terminal size")
	}
	if s.ptyFile == nil {
		return fmt.Errorf("no PTY file descriptor")
	}
	return pty.Setsize(s.ptyFile, &pty.Winsize{Rows: uint16(ts.Row), Cols: uint16(ts.Col)})
}

func (s *session) doClose() error {
	if s.closed {
		return errors.New("session is closed")
	}
	if !s.childDone {
		return errors.New("child process is still running")
	}
	if s.streamsActive > 0 {
		return errors.New("streams are still active")
	}
	err := s.sendClose(s.childExitCode)
	if err == nil {
		s.startCloseHandshake()
	}
	return err
}

func (s *session) error(err error) {
	if s.closed {
		return
	}
	if s.closeErr == nil {
		s.closeErr = err
	}
	s.logger.Warn("session error", "reason_code", webTTYSessionErrorReasonCode(err), "error", err)
	s.close()
}

func (s *session) close() {
	if s.closed {
		return
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
	if s.cmd != nil && s.cmd.Process != nil && !s.childDone {
		if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			s.logger.Debug("failed to kill child on close", "error", err)
		}
	}
	if s.ptyFile != nil {
		_ = s.ptyFile.Close()
	}
	if s.stdinPipe != nil {
		_ = s.stdinPipe.Close()
	}
	_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "finished"))
	_ = s.conn.Close()
	select {
	case <-s.doneCh:
	default:
		close(s.doneCh)
	}
	attrs := s.sessionAuditAttrs()
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
	s.logger.Info("session closed", attrs...)
}

func (s *session) onOpenTimeout() {
	if s.closed || s.opened {
		return
	}
	if s.closeErr == nil {
		s.closeErr = errSessionOperationTimeout
	}
	s.logger.Warn("session open timeout", "error", s.closeErr)
	s.close()
}

func (s *session) onCloseTimeout() {
	if s.closed {
		return
	}
	if s.closeErr == nil {
		s.closeErr = errSessionOperationTimeout
	}
	s.logger.Warn("session close timeout", "error", s.closeErr)
	s.close()
}

func (s *session) signalChildInterruptLocked() error {
	if s.cmd == nil || s.cmd.Process == nil || s.childDone {
		return nil
	}
	err := interruptChildProcess(s.cmd)
	if err == nil {
		s.logger.Debug("requested child process shutdown")
	}
	return err
}

func (s *session) startCloseHandshake() {
	if s.closed || s.disconnecting {
		return
	}
	s.disconnecting = true
	if s.cfg.SessionCloseDeadline != nil && *s.cfg.SessionCloseDeadline > 0 {
		s.closeTimer = time.AfterFunc(*s.cfg.SessionCloseDeadline, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.onCloseTimeout()
		})
	}
	deadline := time.Now().Add(time.Second)
	if s.cfg.SessionCloseDeadline != nil && *s.cfg.SessionCloseDeadline > 0 {
		deadline = time.Now().Add(*s.cfg.SessionCloseDeadline)
	}
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
	if s.payloadCrypto != nil {
		switch t {
		case pb.Data_TYPE_STDOUT:
			encrypt = s.payloadCrypto.EncryptStdout
		case pb.Data_TYPE_STDERR:
			encrypt = s.payloadCrypto.EncryptStderr
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
	if s.closed {
		return errors.New("session is closed")
	}
	s.logProtoMessage("sending", m)
	data, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}
	if s.cfg.SessionCloseDeadline != nil && *s.cfg.SessionCloseDeadline > 0 {
		if err := s.conn.SetWriteDeadline(time.Now().Add(*s.cfg.SessionCloseDeadline)); err != nil {
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

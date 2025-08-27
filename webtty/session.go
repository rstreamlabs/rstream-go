// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/creack/pty"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type session struct {
	conn            *websocket.Conn
	cfg             *ServerConfig
	mu              sync.Mutex
	closed          bool
	cmd             *exec.Cmd
	ptyFile         *os.File
	stdinPipe       io.WriteCloser
	doneCh          chan struct{}
	heartbeatTicker *time.Ticker
	streamsActive   int
	childDone       bool
	childExitCode   int
}

func newSession(conn *websocket.Conn, cfg *ServerConfig) *session {
	s := &session{
		conn:            conn,
		cfg:             cfg,
		doneCh:          make(chan struct{}),
		heartbeatTicker: time.NewTicker(20 * time.Second), // TODO, make configurable
	}
	return s
}

func (s *session) run() {
	go s.readLoop()
	<-s.doneCh
}

func (s *session) readLoop() {
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

func (s *session) heartbeatLoop() {
	defer s.heartbeatTicker.Stop()
	for {
		select {
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
	buf := make([]byte, 4096) // TODO, make configurable
	for {
		n, err := r.Read(buf)
		s.mu.Lock()
		ok := s.onReadStream(t, buf[:n], err)
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
	if err == nil {
		if messageType != websocket.BinaryMessage {
			err = fmt.Errorf("unexpected message type: %d", messageType)
		}
	}
	var msg pb.Message
	if err == nil {
		if err = proto.Unmarshal(p, &msg); err != nil {
			err = fmt.Errorf("failed to unmarshal protobuf: %w", err)
		}
	}
	if err == nil {
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

func (s *session) onReadStream(t pb.Data_Type, p []byte, err error) bool {
	if s.closed {
		return false
	}
	eos := (err == io.EOF)
	if eos {
		err = nil
		err = s.sendEOS(t)
		if err == nil {
			if s.streamsActive > 0 {
				s.streamsActive--
			}
			if s.streamsActive == 0 {
				go s.waitProcessLoop(s.cmd)
			}
		}
	} else if err == nil {
		chunk := append([]byte(nil), p...)
		s.sendData(t, chunk)
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
		log.Printf("[session %p] child exited with code=%d", s, exitCode)
		s.childDone = true
		s.childExitCode = exitCode
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
	{
		json, err := protojson.MarshalOptions{Indent: " ", EmitDefaultValues: true}.Marshal(m)
		if err != nil {
			return fmt.Errorf("failed to marshal protobuf: %w", err)
		}
		log.Printf("[session %p] received message\n%s", s, string(json))
	}
	switch payload := m.Payload.(type) {
	case *pb.Message_Open:
		return s.handleOpen(payload.Open)
	case *pb.Message_Data:
		return s.handleData(payload.Data)
	case *pb.Message_Error:
		return fmt.Errorf("client error (%s)", payload.Error.Msg)
	case *pb.Message_Heartbeat:
		return nil
	case *pb.Message_Parameter:
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

func (s *session) handleOpen(openCfg *pb.Open) error {
	if s.closed {
		return errors.New("session is closed")
	}
	if s.cmd != nil {
		return errors.New("process already started")
	}
	setup := func() error {
		var uv *UsernameVariant
		if pu := openCfg.Config.Username; pu != nil {
			switch p := pu.Payload.(type) {
			case *pb.Username_Name:
				uv = &UsernameVariant{Name: &p.Name}
			case *pb.Username_Id:
				id := p.Id
				uv = &UsernameVariant{UID: &id}
			}
		}
		if runtime.GOOS == "windows" && uv != nil {
			return fmt.Errorf("changing user is not supported on Windows")
		}
		ui, err := GetUserInfo(uv)
		if err != nil {
			return fmt.Errorf("failed to get user info: %w", err)
		}
		cmdArgs := openCfg.Config.CmdArgs
		if len(cmdArgs) == 0 {
			cmdArgs = []string{ui.Shell}
		}
		exe, args := cmdArgs[0], cmdArgs[1:]
		cmd := exec.Command(exe, args...)
		if wd := openCfg.Config.Workdir; wd != nil && wd.Value != "" {
			cmd.Dir = wd.Value
		} else {
			cmd.Dir = ui.Home
		}
		env := BuildEnvironment(openCfg.Config.EnvVars)
		if p := os.Getenv("PATH"); p != "" {
			AddEnvironmentVariable(&env, "PATH", p, false)
		}
		if runtime.GOOS == "windows" {
			for _, key := range []string{
				"ALLUSERSPROFILE",
				"COMPUTERNAME",
				"ComSpec",
				"CYGWIN",
				"OS",
				"PATHEXT",
				"PROGRAMFILES",
				"SYSTEMDRIVE",
				"SYSTEMROOT",
				"TEMP",
				"TMP",
				"USERNAME",
				"USERPROFILE",
				"WINDIR",
			} {
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
		if uv != nil {
			if err := SetupCredential(cmd, ui); err != nil {
				return fmt.Errorf("failed to switch user: %w", err)
			}
		}
		allocateTty := openCfg.Config.Options.AllocateTty
		if allocateTty {
			ptmx, err := pty.Start(cmd)
			if err != nil {
				return err
			}
			s.ptyFile = ptmx
			s.cmd = cmd
			s.streamsActive = 1
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
			go s.copyStdoutLoop(stdout)
			go s.copyStderrLoop(stderr)
		}
		return nil
	}
	err := setup()
	if err != nil {
		_ = s.sendError(err)
		return err
	} else {
		go s.heartbeatLoop()
		return s.sendAck()
	}
}

func (s *session) handleData(d *pb.Data) error {
	if s.closed {
		return errors.New("session is closed")
	}
	if d.Type != pb.Data_TYPE_STDIN {
		return fmt.Errorf("unexpected data type: %v", d.Type)
	}
	switch payload := d.Payload.(type) {
	case *pb.Data_Eos:
		if s.ptyFile != nil {
			err := s.ptyFile.Close()
			return err
		} else if s.stdinPipe != nil {
			err := s.stdinPipe.Close()
			return err
		}
	case *pb.Data_Data:
		if s.ptyFile != nil {
			_, err := s.ptyFile.Write(payload.Data)
			return err
		} else if s.stdinPipe != nil {
			_, err := s.stdinPipe.Write(payload.Data)
			return err
		}
	default:
		return fmt.Errorf("unexpected data payload type: %T", payload)
	}
	return errors.New("no PTY and no stdin pipe")
}

func (s *session) doResize(ts *pb.TerminalSize) error {
	if s.closed {
		return errors.New("session is closed")
	}
	if s.ptyFile == nil {
		return fmt.Errorf("no PTY file descriptor")
	}
	return pty.Setsize(s.ptyFile, &pty.Winsize{
		Rows: uint16(ts.Row),
		Cols: uint16(ts.Col),
	})
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
		s.close()
	}
	return err
}

func (s *session) error(err error) {
	if s.closed {
		return
	}
	log.Printf("[session %p] error: %v", s, err)
	s.close()
}

func (s *session) close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.heartbeatTicker != nil {
		s.heartbeatTicker.Stop()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.ptyFile != nil {
		_ = s.ptyFile.Close()
	}
	_ = s.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "finished"),
	)
	_ = s.conn.Close()
	select {
	case <-s.doneCh:
	default:
		close(s.doneCh)
	}
	log.Printf("[session %p] closed", s)
}

func (s *session) sendAck() error {
	msg := &pb.Message{
		Payload: &pb.Message_Ack{
			Ack: &pb.Ack{},
		},
	}
	return s.writeMessage(msg)
}

func (s *session) sendError(err error) error {
	msg := &pb.Message{
		Payload: &pb.Message_Error{
			Error: &pb.Error{
				Msg: err.Error(),
			},
		},
	}
	return s.writeMessage(msg)
}

func (s *session) sendHeartbeat() error {
	msg := &pb.Message{
		Payload: &pb.Message_Heartbeat{
			Heartbeat: &pb.Heartbeat{},
		},
	}
	return s.writeMessage(msg)
}

func (s *session) sendData(t pb.Data_Type, chunk []byte) error {
	msg := &pb.Message{
		Payload: &pb.Message_Data{
			Data: &pb.Data{
				Type: t,
				Payload: &pb.Data_Data{
					Data: chunk,
				},
			},
		},
	}
	return s.writeMessage(msg)
}

func (s *session) sendEOS(t pb.Data_Type) error {
	msg := &pb.Message{
		Payload: &pb.Message_Data{
			Data: &pb.Data{
				Type: t,
				Payload: &pb.Data_Eos{
					Eos: &pb.EndOfStream{},
				},
			},
		},
	}
	return s.writeMessage(msg)
}

func (s *session) sendClose(code int) error {
	msg := &pb.Message{
		Payload: &pb.Message_Close{
			Close: &pb.Close{
				ReturnCode: int32(code),
			},
		},
	}
	return s.writeMessage(msg)
}

func (s *session) writeMessage(m *pb.Message) error {
	if s.closed {
		return errors.New("session is closed")
	}
	{
		json, err := protojson.MarshalOptions{Indent: " ", EmitDefaultValues: true}.Marshal(m)
		if err != nil {
			return fmt.Errorf("failed to marshal protobuf: %w", err)
		}
		log.Printf("[session %p] sending message\n%s", s, string(json))
	}
	data, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

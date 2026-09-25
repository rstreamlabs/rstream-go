// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

type blockingSessionMessageConn struct {
	closeOnce    sync.Once
	startedOnce  sync.Once
	closed       chan struct{}
	writeStarted chan struct{}
}

type blockingSessionCloseConn struct {
	closeOnce    sync.Once
	closed       chan struct{}
	closeStarted chan struct{}
}

type trackingSessionStdin struct {
	closed     bool
	closeCalls int
	writes     []byte
}

type recordingSessionMessageConn struct {
	mu       sync.Mutex
	messages [][]byte
}

func (c *recordingSessionMessageConn) Close() error { return nil }

func (c *recordingSessionMessageConn) ReadMessage() (int, []byte, error) {
	return 0, nil, io.EOF
}

func (c *recordingSessionMessageConn) SetReadLimit(int64) {}

func (c *recordingSessionMessageConn) SetWriteDeadline(time.Time) error { return nil }

func (c *recordingSessionMessageConn) WriteControl(int, []byte, time.Time) error { return nil }

func (c *recordingSessionMessageConn) WriteMessage(_ int, payload []byte) error {
	c.mu.Lock()
	c.messages = append(c.messages, append([]byte(nil), payload...))
	c.mu.Unlock()
	return nil
}

func (c *recordingSessionMessageConn) decoded(t *testing.T) []*pb.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	messages := make([]*pb.Message, 0, len(c.messages))
	for _, payload := range c.messages {
		message := &pb.Message{}
		if err := proto.Unmarshal(payload, message); err != nil {
			t.Fatalf("decode recorded WebTTY message: %v", err)
		}
		messages = append(messages, message)
	}
	return messages
}

func TestSessionSerializesAllOutputEOSBeforeCloseForConcurrentTerminalEvents(t *testing.T) {
	for attempt := range 256 {
		conn := &recordingSessionMessageConn{}
		ctx, cancel := context.WithCancel(t.Context())
		zero := time.Duration(0)
		s := &session{
			conn:          conn,
			cfg:           resolveServerConfig(&ServerConfig{HeartbeatInterval: &zero, SessionCloseDeadline: &zero}),
			logger:        slog.Default(),
			ctx:           ctx,
			cancel:        cancel,
			doneCh:        make(chan struct{}),
			streamsActive: 2,
			streamsEnding: make(map[pb.Data_Type]bool, 2),
			streamsOpen: map[pb.Data_Type]bool{
				pb.Data_TYPE_STDOUT: true,
				pb.Data_TYPE_STDERR: true,
			},
		}
		if !s.onReadStream(pb.Data_TYPE_STDOUT, nil, nil) {
			t.Fatalf("attempt %d: zero-byte stdout was not accepted", attempt)
		}
		if !s.onReadStream(pb.Data_TYPE_STDERR, []byte{byte(attempt)}, nil) {
			t.Fatalf("attempt %d: one-byte stderr was not accepted", attempt)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_ = s.onReadStream(pb.Data_TYPE_STDOUT, nil, io.EOF)
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = s.onReadStream(pb.Data_TYPE_STDERR, nil, io.EOF)
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = s.onChildExit(17, nil)
		}()
		close(start)
		wg.Wait()
		messages := conn.decoded(t)
		if len(messages) != 5 {
			t.Fatalf("attempt %d: message count = %d, want 5", attempt, len(messages))
		}
		if messages[0].GetData() == nil || messages[1].GetData() == nil {
			t.Fatalf("attempt %d: first messages are not stream data", attempt)
		}
		seenEOS := map[pb.Data_Type]bool{}
		for _, message := range messages[2:4] {
			data := message.GetData()
			if data == nil || data.GetEos() == nil {
				t.Fatalf("attempt %d: message before close is %T, want EOS", attempt, message.Payload)
			}
			seenEOS[data.Type] = true
		}
		if !seenEOS[pb.Data_TYPE_STDOUT] || !seenEOS[pb.Data_TYPE_STDERR] {
			t.Fatalf("attempt %d: EOS streams = %v", attempt, seenEOS)
		}
		if closeMessage := messages[4].GetClose(); closeMessage == nil || closeMessage.ReturnCode != 17 {
			t.Fatalf("attempt %d: final message = %T, want close(17)", attempt, messages[4].Payload)
		}
		before := len(messages)
		_ = s.onReadStream(pb.Data_TYPE_STDOUT, nil, io.EOF)
		if got := len(conn.decoded(t)); got != before {
			t.Fatalf("attempt %d: duplicate EOS emitted another message", attempt)
		}
		cancel()
	}
}

func TestSessionClosedChildInputDoesNotCloseOutput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	s := &session{stdinPipe: writer, ctx: t.Context()}
	message := &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: []byte("late input")}}
	if err := s.handleData(message); err != nil {
		t.Fatalf("child closing stdin interrupted its output: %v", err)
	}
	if !s.stdinClosed || s.stdinPipe != nil || s.closed {
		t.Fatal("child stdin closure must leave the session open to drain output")
	}
	if err := s.handleData(message); err != nil {
		t.Fatalf("subsequent input was not ignored: %v", err)
	}
}

func (s *trackingSessionStdin) Close() error {
	s.closeCalls++
	if s.closed {
		return os.ErrClosed
	}
	s.closed = true
	return nil
}

func (s *trackingSessionStdin) Write(p []byte) (int, error) {
	if s.closed {
		return 0, os.ErrClosed
	}
	s.writes = append(s.writes, p...)
	return len(p), nil
}

func newBlockingSessionCloseConn() *blockingSessionCloseConn {
	return &blockingSessionCloseConn{closed: make(chan struct{}), closeStarted: make(chan struct{})}
}

func (c *blockingSessionCloseConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *blockingSessionCloseConn) ReadMessage() (int, []byte, error) {
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (c *blockingSessionCloseConn) SetReadLimit(int64) {}

func (c *blockingSessionCloseConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *blockingSessionCloseConn) WriteControl(int, []byte, time.Time) error {
	select {
	case <-c.closeStarted:
	default:
		close(c.closeStarted)
	}
	<-c.closed
	return net.ErrClosed
}

func (c *blockingSessionCloseConn) WriteMessage(int, []byte) error {
	return nil
}

func newBlockingSessionMessageConn() *blockingSessionMessageConn {
	return &blockingSessionMessageConn{
		closed:       make(chan struct{}),
		writeStarted: make(chan struct{}),
	}
}

func (c *blockingSessionMessageConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
	return nil
}

func (c *blockingSessionMessageConn) ReadMessage() (int, []byte, error) {
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (c *blockingSessionMessageConn) SetReadLimit(int64) {}

func (c *blockingSessionMessageConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *blockingSessionMessageConn) WriteControl(int, []byte, time.Time) error {
	return c.Close()
}

func (c *blockingSessionMessageConn) WriteMessage(int, []byte) error {
	c.startedOnce.Do(func() {
		close(c.writeStarted)
	})
	<-c.closed
	return net.ErrClosed
}

func TestSessionShutdownInterruptsBlockedHeartbeatWrite(t *testing.T) {
	heartbeat := time.Millisecond
	zero := time.Duration(0)
	conn := newBlockingSessionMessageConn()
	cfg := resolveServerConfig(&ServerConfig{HeartbeatInterval: &heartbeat, SessionCloseDeadline: &zero})
	s := newSession(conn, cfg, nil, "blocked-write", WebTTYTransportWebSocket)
	go s.heartbeatLoop()
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("heartbeat write did not start")
	}
	stateAccessible := make(chan bool)
	go func() {
		stateAccessible <- s.isClosed()
	}()
	stateAvailable := false
	select {
	case <-stateAccessible:
		stateAvailable = true
	case <-time.After(100 * time.Millisecond):
	}
	shutdownDone := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		s.shutdown(ctx)
		close(shutdownDone)
	}()
	shutdownCompleted := false
	select {
	case <-shutdownDone:
		shutdownCompleted = true
	case <-time.After(250 * time.Millisecond):
	}
	_ = conn.Close()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("session shutdown remained blocked after transport close")
	}
	if !stateAvailable {
		t.Fatal("session state lock was held by a blocked transport write")
	}
	if !shutdownCompleted {
		t.Fatal("session shutdown did not interrupt the blocked transport write")
	}
}

func TestSessionShutdownFinishesConcurrentCleanup(t *testing.T) {
	zero := time.Duration(0)
	conn := newBlockingSessionCloseConn()
	cfg := resolveServerConfig(&ServerConfig{HeartbeatInterval: &zero, SessionCloseDeadline: &zero})
	s := newSession(conn, cfg, nil, "concurrent-cleanup", WebTTYTransportWebSocket)
	go s.close()
	select {
	case <-conn.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("session cleanup did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan struct{})
	go func() {
		s.shutdown(ctx)
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		select {
		case <-s.done():
		default:
			t.Fatal("shutdown returned before concurrent cleanup finished")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not force concurrent cleanup to finish")
	}
}

func TestSessionStdinEOFFinalizesInputOnceWithoutClosingSession(t *testing.T) {
	stdin := &trackingSessionStdin{}
	s := &session{ctx: t.Context(), logger: slog.Default(), stdinPipe: stdin}
	data := &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: []byte("before")}}
	eof := &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}}
	if err := s.handleData(data); err != nil {
		t.Fatalf("write before EOF: %v", err)
	}
	if err := s.handleData(eof); err != nil {
		t.Fatalf("first EOF: %v", err)
	}
	if err := s.handleData(eof); err != nil {
		t.Fatalf("repeated EOF: %v", err)
	}
	if err := s.handleData(data); err != nil {
		t.Fatalf("late data: %v", err)
	}
	if got := string(stdin.writes); got != "before" {
		t.Fatalf("stdin writes = %q", got)
	}
	if stdin.closeCalls != 1 {
		t.Fatalf("stdin close calls = %d, want 1", stdin.closeCalls)
	}
	if s.closed {
		t.Fatal("stdin EOF closed the WebTTY session")
	}
}

func TestIsExpectedWebTTYPeerCloseError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "eof",
			err:  io.EOF,
			want: true,
		},
		{
			name: "wrapped eof",
			err:  fmt.Errorf("read websocket: %w", io.EOF),
			want: true,
		},
		{
			name: "closed file",
			err:  os.ErrClosed,
			want: true,
		},
		{
			name: "normal websocket close",
			err:  &websocket.CloseError{Code: websocket.CloseNormalClosure},
			want: true,
		},
		{
			name: "going away websocket close",
			err:  &websocket.CloseError{Code: websocket.CloseGoingAway},
			want: true,
		},
		{
			name: "abnormal websocket close",
			err:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure},
			want: true,
		},
		{
			name: "protocol error",
			err:  &websocket.CloseError{Code: websocket.CloseProtocolError},
			want: false,
		},
		{
			name: "authorization error",
			err:  errors.New("WebTTY client signing key is not authorized"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExpectedWebTTYPeerCloseError(tt.err); got != tt.want {
				t.Fatalf("isExpectedWebTTYPeerCloseError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebTTYProtocolErrorForError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code pb.ProtocolErrorCode
		ok   bool
	}{
		{
			name: "client proof required",
			err:  errWebTTYClientProofRequired,
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED,
			ok:   true,
		},
		{
			name: "client proof invalid",
			err:  fmt.Errorf("%w: bad signature", errWebTTYClientProofInvalid),
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_INVALID,
			ok:   true,
		},
		{
			name: "wrapped client proof required",
			err:  fmt.Errorf("open rejected: %w", errWebTTYClientProofRequired),
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED,
			ok:   true,
		},
		{
			name: "client unauthorized",
			err:  errWebTTYClientProofUnauthorized,
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED,
			ok:   true,
		},
		{
			name: "wrapped client unauthorized",
			err:  fmt.Errorf("open rejected: %w", errWebTTYClientProofUnauthorized),
			code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED,
			ok:   true,
		},
		{
			name: "generic error",
			err:  errors.New("generic failure"),
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := webTTYProtocolErrorForError(tt.err)
			if ok != tt.ok {
				t.Fatalf("webTTYProtocolErrorForError() ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got == nil {
				t.Fatal("webTTYProtocolErrorForError() returned nil protocol error")
			}
			if got.Code != tt.code {
				t.Fatalf("protocol error code = %v, want %v", got.Code, tt.code)
			}
			if got.Msg == "" {
				t.Fatal("protocol error message is empty")
			}
		})
	}
}

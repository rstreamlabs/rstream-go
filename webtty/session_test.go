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

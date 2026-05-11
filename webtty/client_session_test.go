// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestResolveSessionConfigDefaults(t *testing.T) {
	cfg, err := resolveSessionConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "ws://127.0.0.1:8080" {
		t.Fatalf("unexpected default URL: %q", cfg.URL)
	}
	if cfg.MaxMessageSize == nil || *cfg.MaxMessageSize != defaultMaxMessageSize {
		t.Fatalf("unexpected max message size: %v", cfg.MaxMessageSize)
	}
	if cfg.ReadBufferSize == nil || *cfg.ReadBufferSize != defaultReadBufferSize {
		t.Fatalf("unexpected read buffer size: %v", cfg.ReadBufferSize)
	}
	if cfg.WriteBufferSize == nil || *cfg.WriteBufferSize != defaultWriteBufferSize {
		t.Fatalf("unexpected write buffer size: %v", cfg.WriteBufferSize)
	}
	if cfg.OpenDeadline == nil || *cfg.OpenDeadline != defaultClientOpenDeadline {
		t.Fatalf("unexpected open deadline: %v", cfg.OpenDeadline)
	}
	if cfg.CloseDeadline == nil || *cfg.CloseDeadline != defaultClientCloseDeadline {
		t.Fatalf("unexpected close deadline: %v", cfg.CloseDeadline)
	}
	if cfg.HeartbeatInterval == nil || *cfg.HeartbeatInterval != defaultHeartbeatInterval {
		t.Fatalf("unexpected heartbeat interval: %v", cfg.HeartbeatInterval)
	}
	if cfg.Logger == nil {
		t.Fatalf("expected default logger")
	}
}

func TestClientSessionConfigConversionsCopySlices(t *testing.T) {
	workdir := "/tmp"
	username := "demo"
	cfg := (&ClientConfig{
		URL:         "ws://example.test",
		EnvVars:     []string{"A=1"},
		Workdir:     &workdir,
		Username:    &username,
		CmdArgs:     []string{"sh", "-lc", "true"},
		Interactive: true,
		AllocateTTY: true,
	}).sessionConfig()
	cfg.EnvVars[0] = "A=2"
	cfg.CmdArgs[0] = "bash"
	if got := (&ClientConfig{EnvVars: []string{"A=1"}, CmdArgs: []string{"sh"}}).sessionConfig(); got.EnvVars[0] != "A=1" || got.CmdArgs[0] != "sh" {
		t.Fatalf("sessionConfig should copy slices")
	}
	clientCfg := cfg.clientConfig()
	clientCfg.EnvVars[0] = "A=3"
	clientCfg.CmdArgs[0] = "zsh"
	if cfg.EnvVars[0] != "A=2" || cfg.CmdArgs[0] != "bash" {
		t.Fatalf("clientConfig should copy slices")
	}
	if clientCfg.Workdir != &workdir || clientCfg.Username != &username {
		t.Fatalf("pointer fields should be preserved")
	}
	if (*ClientConfig)(nil).sessionConfig() != nil {
		t.Fatalf("nil client config should convert to nil session config")
	}
	if (*SessionConfig)(nil).clientConfig() != nil {
		t.Fatalf("nil session config should convert to nil client config")
	}
}

func TestDecodeClientSessionEvent(t *testing.T) {
	tests := []struct {
		name    string
		data    *pb.Data
		want    ClientSessionEvent
		wantOK  bool
		wantErr bool
	}{
		{
			name:   "stdout data",
			data:   &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("out")}},
			want:   ClientSessionEvent{Stream: ClientSessionStdout, Data: []byte("out")},
			wantOK: true,
		},
		{
			name:   "stderr data",
			data:   &pb.Data{Type: pb.Data_TYPE_STDERR, Payload: &pb.Data_Data{Data: []byte("err")}},
			want:   ClientSessionEvent{Stream: ClientSessionStderr, Data: []byte("err")},
			wantOK: true,
		},
		{
			name:   "eos is ignored",
			data:   &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}},
			wantOK: false,
		},
		{name: "nil data", wantErr: true},
		{name: "unexpected stream", data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: []byte("in")}}, wantErr: true},
		{name: "unexpected payload", data: &pb.Data{Type: pb.Data_TYPE_STDOUT}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := decodeClientSessionEvent(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestClientSessionWaitClosedChannel(t *testing.T) {
	session := &ClientSession{resultCh: make(chan clientSessionResult)}
	close(session.resultCh)
	exitCode, err := session.Wait()
	if exitCode != -1 || !errors.Is(err, io.EOF) {
		t.Fatalf("got exit=%d err=%v", exitCode, err)
	}
}

func TestClientSessionCloseStoresLocalResult(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != -1 {
		t.Fatalf("Wait() after Close() = %d, %v", exitCode, err)
	}
}

func TestClientSessionRunRejectsMalformedMessages(t *testing.T) {
	cases := []clientEvent{
		{msg: nil},
		{msg: &pb.Message{Payload: &pb.Message_Close{}}},
		{msg: &pb.Message{Payload: &pb.Message_Error{}}},
	}
	for i, event := range cases {
		session := newBareClientSession()
		readEvents := make(chan clientEvent, 1)
		loopErrCh := make(chan error, 1)
		readEvents <- event
		go session.run(readEvents, loopErrCh)
		exitCode, err := session.Wait()
		if exitCode != -1 || err == nil {
			t.Fatalf("case %d: Wait() = %d, %v; want error", i, exitCode, err)
		}
	}
}

func TestClientSessionStringFolding(t *testing.T) {
	if !stringsEqualFoldTrimmed(" Dev ", "dev") {
		t.Fatalf("trimmed fold comparison failed")
	}
	if got := stringsTrimSpaceFold(" Dev "); got != "dev" {
		t.Fatalf("unexpected folded string: %q", got)
	}
}

func TestOpenClientSessionSendsInputEOFResizeAndWaitsForClose(t *testing.T) {
	received := make(chan *pb.Message, 4)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		msg := readWebTTYMessage(t, conn)
		received <- msg
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		for i := 0; i < 3; i++ {
			received <- readWebTTYMessage(t, conn)
		}
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 7}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	open := receiveMessage(t, received).GetOpen()
	if open == nil || open.Config == nil {
		t.Fatalf("expected open message, got %#v", open)
	}
	if err := session.SendText("hello"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	if err := session.Resize(24, 80); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	stdin := receiveMessage(t, received).GetData()
	if stdin.GetType() != pb.Data_TYPE_STDIN || string(stdin.GetData()) != "hello" {
		t.Fatalf("unexpected stdin message: %#v", stdin)
	}
	eof := receiveMessage(t, received).GetData()
	if eof.GetEos() == nil {
		t.Fatalf("expected stdin EOF message: %#v", eof)
	}
	size := receiveMessage(t, received).GetParameter().GetTerminalSize()
	if size.GetRow() != 24 || size.GetCol() != 80 {
		t.Fatalf("unexpected terminal size: %#v", size)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 7 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionReceivesEvents(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("out")}}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 3}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	select {
	case event := <-session.Events():
		if event.Stream != ClientSessionStdout || string(event.Data) != "out" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for stdout event")
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 3 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionSendsBearerToken(t *testing.T) {
	authToken := "  secret-token  "
	receivedAuth := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth <- r.Header.Get("Authorization")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	}))
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		AuthToken:    &authToken,
		OpenDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	select {
	case got := <-receivedAuth:
		if got != "Bearer secret-token" {
			t.Fatalf("Authorization header = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Authorization header")
	}
}

func TestOpenClientSessionSendsHeartbeat(t *testing.T) {
	heartbeat := 10 * time.Millisecond
	received := make(chan *pb.Message, 2)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), SendHeartbeat: true, HeartbeatInterval: &heartbeat, OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if receiveMessage(t, received).GetOpen() == nil {
		t.Fatalf("expected open message")
	}
	if receiveMessage(t, received).GetHeartbeat() == nil {
		t.Fatalf("expected heartbeat message")
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionEnforcesReadLimit(t *testing.T) {
	maxSize := int64(8)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		large := strings.Repeat("x", 64)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: large}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), MaxMessageSize: &maxSize, OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	exitCode, err := session.Wait()
	if exitCode != -1 || err == nil {
		t.Fatalf("Wait() = %d, %v; want read limit error", exitCode, err)
	}
}

func TestOpenClientSessionRequiresCustomDialerForRstreamURL(t *testing.T) {
	_, err := OpenClientSession(t.Context(), &SessionConfig{URL: "rstrm://shell"})
	if err == nil || !strings.Contains(err.Error(), "requires a custom dialer") {
		t.Fatalf("expected custom dialer error, got %v", err)
	}
}

func newClientSessionTestServer(t *testing.T, handle func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		handle(conn)
	}))
}

func readWebTTYMessage(t *testing.T, conn *websocket.Conn) *pb.Message {
	t.Helper()
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type = %d", messageType)
	}
	msg := &pb.Message{}
	if err := proto.Unmarshal(raw, msg); err != nil {
		t.Fatalf("decode protobuf message: %v", err)
	}
	return msg
}

func writeWebTTYMessage(t *testing.T, conn *websocket.Conn, msg *pb.Message) {
	t.Helper()
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal protobuf message: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatalf("write websocket message: %v", err)
	}
}

func receiveMessage(t *testing.T, ch <-chan *pb.Message) *pb.Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for message")
		return nil
	}
}

func testWebTTYURL(raw string) string {
	return "ws" + strings.TrimPrefix(raw, "http")
}

func durationPtr(value time.Duration) *time.Duration {
	return &value
}

func newBareClientSession() *ClientSession {
	_, cancel := context.WithCancel(context.Background())
	return &ClientSession{
		loopCancel: cancel,
		doneRead:   make(chan struct{}),
		events:     make(chan ClientSessionEvent, 1),
		resultCh:   make(chan clientSessionResult, 1),
	}
}

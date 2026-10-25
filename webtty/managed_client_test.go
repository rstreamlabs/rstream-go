// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func managedExecutionTestConfig() *ManagedSessionConfig {
	return &ManagedSessionConfig{
		ExecutionSeconds: 60,
		Capabilities: rstream.WebTTYCapabilities{
			ManagedProtocol:     true,
			DetachedExecution:   true,
			ExplicitTermination: true,
			MaxExecutionSeconds: 300,
			ExecutionTransports: []rstream.WebTTYTransport{rstream.WebTTYTransportWebSocket, rstream.WebTTYTransportWebTransport},
		},
	}
}

func TestManagedClientRejectsUnsupportedExecutionBeforeDial(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*SessionConfig)
		want   string
	}{
		{"legacy engine", func(c *SessionConfig) { c.Managed.Capabilities.ManagedProtocol = false }, "managed WebTTY"},
		{"no detached support", func(c *SessionConfig) { c.Managed.Capabilities.DetachedExecution = false }, "detached WebTTY"},
		{"no duration limit", func(c *SessionConfig) { c.Managed.Capabilities.MaxExecutionSeconds = 0 }, "Engine limit"},
		{"over duration limit", func(c *SessionConfig) { c.Managed.ExecutionSeconds = 301 }, "Engine limit"},
		{"negative duration", func(c *SessionConfig) { c.Managed.ExecutionSeconds = -1 }, "positive"},
		{"interactive", func(c *SessionConfig) { c.Interactive = true }, "non-interactive"},
		{"tty", func(c *SessionConfig) { c.AllocateTTY = true }, "without a TTY"},
		{"attach", func(c *SessionConfig) { c.Attach = &AttachConfig{} }, "new non-interactive"},
		{"plain", func(c *SessionConfig) { c.Transport = WebTTYTransportPlain; c.URL = "rstrm://fixture" }, "over plain"},
		{"transport not advertised", func(c *SessionConfig) { c.Managed.Capabilities.ExecutionTransports = nil }, "over websocket"},
		{"duplicate query", func(c *SessionConfig) { c.URL += "?execution_seconds=60" }, "only through Managed"},
		{"interactive query", func(c *SessionConfig) { c.URL += "?session_mode=interactive" }, "conflicts"},
		{"repeated mode", func(c *SessionConfig) { c.URL += "?session_mode=non-interactive&session_mode=non-interactive" }, "conflicts"},
		{"malformed query", func(c *SessionConfig) { c.URL += "?tag=%GG" }, "invalid managed WebTTY query"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			cfg := &SessionConfig{URL: "rstrm://fixture", Managed: managedExecutionTestConfig()}
			cfg.DialContext = func(context.Context, string, string) (net.Conn, error) {
				calls.Add(1)
				return nil, errors.New("unexpected dial")
			}
			test.change(cfg)
			if session, err := OpenClientSession(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), test.want) {
				if session != nil {
					_ = session.Close()
				}
				t.Fatalf("unsupported execution: %v, want %q", err, test.want)
			}
			if calls.Load() != 0 {
				t.Fatal("unsupported execution attempted network I/O")
			}
		})
	}
}

func TestManagedClientExecutionConfirmsIdentityAndBudget(t *testing.T) {
	for _, transport := range []WebTTYTransport{WebTTYTransportWebSocket, WebTTYTransportWebTransport} {
		t.Run(string(transport), func(t *testing.T) { checkManagedClientExecutionIdentity(t, transport) })
	}
}

func checkManagedClientExecutionIdentity(t *testing.T, transport WebTTYTransport) {
	t.Helper()
	queries := make(chan url.Values, 1)
	endpoint, tlsConfig := managedClientTestEndpoint(t, transport, func(conn messageConn, r *http.Request) {
		queries <- r.URL.Query()
		if message := readWebTTYMessage(t, conn); message.GetOpen() == nil || message.GetOpen().GetConfig().GetOptions().GetInteractive() {
			t.Error("expected one non-interactive Open")
			return
		}
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{ManagedSessionId: "execution-1"}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 23}}})
	})
	cfg := &SessionConfig{URL: endpoint + "?purpose=generic-test", Transport: transport, TLSConfig: tlsConfig, Managed: managedExecutionTestConfig()}
	session, err := OpenClientSession(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.ManagedSessionID() != "execution-1" || strings.Contains(cfg.URL, "execution_seconds") {
		t.Fatal("execution ID lost or caller configuration mutated")
	}
	query := <-queries
	if query.Get("execution_seconds") != "60" || query.Get("session_mode") != "non-interactive" || query.Get("purpose") != "generic-test" {
		t.Fatalf("execution metadata = %v", query)
	}
	if code, err := session.Wait(); err != nil || code != 23 {
		t.Fatalf("native result = %d, %v", code, err)
	}
}

func managedClientTestEndpoint(t *testing.T, transport WebTTYTransport, handle func(messageConn, *http.Request)) (string, *tls.Config) {
	t.Helper()
	if transport == WebTTYTransportWebSocket {
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			handle(conn, r)
		}))
		t.Cleanup(server.Close)
		return testWebTTYURL(server.URL), nil
	}
	packets, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := &webtransport.Server{
		H3:          &http3.Server{Handler: mux, TLSConfig: testWebTransportTLSConfig(t), EnableDatagrams: true},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtty", func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			t.Error(err)
			return
		}
		defer session.CloseWithError(0, "")
		stream, err := session.AcceptStream(t.Context())
		if err != nil {
			t.Error(err)
			return
		}
		conn := newWebTransportMessageConn(session, stream)
		handle(conn, r)
		_ = stream.Close()
		_, _, _ = conn.ReadMessage()
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(packets) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packets.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "server closed") {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("WebTransport fixture did not release its listener")
		}
	})
	return "https://" + packets.LocalAddr().String() + "/webtty", &tls.Config{InsecureSkipVerify: true}
}

func TestManagedClientRejectsUnconfirmedOrMismatchedIdentity(t *testing.T) {
	for _, attach := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing detached identity", true: "wrong attached identity"}[attach], func(t *testing.T) {
			closed := make(chan struct{})
			server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
				defer close(closed)
				defer conn.Close()
				readWebTTYMessage(t, conn)
				ack := &pb.Ack{}
				if attach {
					ack.ManagedSessionId = "wrong-session"
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: ack}})
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				_, _, _ = conn.ReadMessage()
			})
			defer server.Close()
			cfg := &SessionConfig{URL: testWebTTYURL(server.URL), Managed: managedExecutionTestConfig()}
			if attach {
				cfg.Managed.ExecutionSeconds = 0
				cfg.Attach = &AttachConfig{SessionID: "session-1", ParticipantID: "reader", AttachGrant: []byte("grant")}
			}
			session, err := OpenClientSession(t.Context(), cfg)
			if session != nil {
				_ = session.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "session") {
				t.Fatalf("unconfirmed identity was accepted: %v", err)
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("rejected session retained its connection")
			}
		})
	}
}

func TestManagedClientTerminationRequiresRelayCapability(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		managed   bool
		supported bool
	}{
		{"older relay", true, false},
		{"supported relay", true, true},
		{"unmanaged relay", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			supported := scenario.supported
			received := make(chan *pb.Message, 1)
			server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
				defer conn.Close()
				readWebTTYMessage(t, conn)
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{Capabilities: []pb.SessionCapability{pb.SessionCapability_SESSION_CAPABILITY_TERMINATE}}}})
				received <- readWebTTYMessage(t, conn)
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 29}}})
			})
			defer server.Close()
			managed := managedExecutionTestConfig()
			managed.ExecutionSeconds = 0
			managed.Capabilities.ManagedProtocol = scenario.managed
			managed.Capabilities.ExplicitTermination = supported
			session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), Managed: managed})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.CanTerminate() != supported {
				t.Fatal("native Ack bypassed the Engine capability gate")
			}
			err = session.RequestTermination(t.Context())
			if supported && err != nil || !supported && !errors.Is(err, ErrTerminationUnsupported) {
				t.Fatalf("termination result = %v", err)
			}
			if !supported {
				if err := session.SendEOF(); err != nil {
					t.Fatal(err)
				}
			}
			message := receiveMessage(t, received)
			if (message.GetTerminate() != nil) != supported || !supported && message.GetData().GetEos() == nil {
				t.Fatalf("unexpected managed request: %v", message)
			}
			if code, err := session.Wait(); code != 29 || err != nil {
				t.Fatalf("native result = %d, %v", code, err)
			}
		})
	}
}

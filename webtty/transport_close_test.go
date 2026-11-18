// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

func TestWebTransportMessageCloseUnblocksFlowControlledSession(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	mux := http.NewServeMux()
	server := &webtransport.Server{H3: &http3.Server{Handler: mux, TLSConfig: testWebTransportTLSConfig(t), QUICConfig: &quic.Config{EnableStreamResetPartialDelivery: true, InitialStreamReceiveWindow: 32 << 10, MaxStreamReceiveWindow: 32 << 10, InitialConnectionReceiveWindow: 64 << 10, MaxConnectionReceiveWindow: 64 << 10}, EnableDatagrams: true}, CheckOrigin: func(*http.Request) bool { return true }}
	webtransport.ConfigureHTTP3Server(server.H3)
	defer server.Close()
	accepted := make(chan *webtransport.Session, 1)
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		accepted <- session
	})
	served := make(chan error, 1)
	go func() { served <- server.Serve(packet) }()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-served:
		case <-time.After(time.Second):
			t.Error("WebTransport server leaked")
		}
	})
	dialer := &webtransport.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true}}
	defer dialer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, session, err := dialer.Dial(ctx, "https://"+packet.LocalAddr().String()+"/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	var peer *webtransport.Session
	select {
	case peer = <-accepted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	writers := make([]<-chan error, 0, 3)
	for range 2 {
		stream, err := session.OpenStreamSync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := stream.Write(make([]byte, 1<<20)); done <- err }()
		writers = append(writers, done)
		if _, err := peer.AcceptStream(ctx); err != nil {
			t.Fatal(err)
		}
	}
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn := newWebTransportMessageConn(session, stream)
	written := make(chan error, 1)
	go func() { written <- conn.WriteMessage(websocket.BinaryMessage, make([]byte, 1<<20)) }()
	writers = append(writers, written)
	select {
	case err := <-written:
		t.Fatalf("third stream did not encounter connection backpressure: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		_ = dialer.Close()
		t.Fatal("message close remained blocked by WebTransport backpressure")
	}
	for _, done := range writers {
		select {
		case err := <-done:
			if err == nil {
				t.Error("flow-controlled writer unexpectedly completed")
			}
		case <-time.After(500 * time.Millisecond):
			_ = dialer.Close()
			t.Fatal("message close leaked a writer")
		}
	}
}

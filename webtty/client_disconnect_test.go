// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestManagedDisconnectBoundsUnresponsivePeer(t *testing.T) {
	for _, transport := range []WebTTYTransport{WebTTYTransportWebSocket, WebTTYTransportWebTransport} {
		t.Run(string(transport), func(t *testing.T) {
			release, handled := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releasePeer := func() { releaseOnce.Do(func() { close(release) }) }
			endpoint, tlsConfig := managedClientTestEndpoint(t, transport, func(conn messageConn, _ *http.Request) {
				defer close(handled)
				readWebTTYMessage(t, conn)
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{ManagedSessionId: "execution-1"}}})
				<-release
			})
			defer releasePeer()
			session, err := OpenClientSession(t.Context(), &SessionConfig{URL: endpoint, Transport: transport, TLSConfig: tlsConfig, Managed: managedExecutionTestConfig(), CloseDeadline: new(25 * time.Millisecond), SendHeartbeat: true, HeartbeatInterval: new(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			drained := make(chan struct{})
			go func() {
				defer close(drained)
				for range session.Events() {
				}
			}()
			started := time.Now()
			if err := session.Disconnect(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("unconfirmed disconnect = %v", err)
			}
			if time.Since(started) > time.Second {
				t.Error("disconnect exceeded its cleanup bound")
			}
			if _, err := session.Wait(); err == nil {
				t.Error("reader timeout invented a native result")
			}
			select {
			case <-drained:
			case <-time.After(time.Second):
				t.Error("reader event loop retained")
			}
			releasePeer()
			select {
			case <-handled:
			case <-time.After(time.Second):
				t.Error("peer handler retained")
			}
		})
	}
}

func TestManagedDisconnectPreservesConfirmedNativeResult(t *testing.T) {
	for _, transport := range []WebTTYTransport{WebTTYTransportWebSocket, WebTTYTransportWebTransport} {
		t.Run(string(transport), func(t *testing.T) {
			endpoint, tlsConfig := managedClientTestEndpoint(t, transport, func(conn messageConn, _ *http.Request) {
				readWebTTYMessage(t, conn)
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{ManagedSessionId: "execution-1"}}})
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 23}}})
			})
			session, err := OpenClientSession(t.Context(), &SessionConfig{URL: endpoint, Transport: transport, TLSConfig: tlsConfig, Managed: managedExecutionTestConfig()})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if code, err := session.Wait(); err != nil || code != 23 {
				t.Fatalf("native completion = %d, %v", code, err)
			}
			for range 2 {
				if err := session.Disconnect(t.Context()); err != nil {
					t.Fatal(err)
				}
				if code, err := session.Wait(); err != nil || code != 23 {
					t.Fatalf("disconnect changed completion = %d, %v", code, err)
				}
			}
		})
	}
}

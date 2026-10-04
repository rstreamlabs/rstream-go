// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

type handshakeTestDialer struct{ conn net.Conn }

func (d handshakeTestDialer) Dial(context.Context, string, *tls.Config) (net.Conn, error) {
	return d.conn, nil
}

func TestConnectCanceledContextDoesNotDial(t *testing.T) {
	dialer := &countingFailDialer{}
	client := &Client{EngineURL: StringPtr("engine.example.com:443"), Token: StringPtr("test-token"), Transport: dialer}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("caller stopped connection setup")
	cancel(cause)
	if _, err := client.Connect(ctx, nil); !errors.Is(err, cause) {
		t.Fatalf("Connect() = %v, want cancellation cause", err)
	}
	if calls := dialer.calls.Load(); calls != 0 {
		t.Fatalf("canceled setup dialed %d times", calls)
	}
}

func TestConnectCancellationInterruptsHandshake(t *testing.T) {
	for _, stage := range []string{"write", "read"} {
		for _, cancellation := range []string{"cancel", "deadline"} {
			t.Run(stage+"/"+cancellation, func(t *testing.T) {
				conn, peer := net.Pipe()
				defer conn.Close()
				defer peer.Close()
				writing := make(chan struct{})
				client := &Client{
					EngineURL: StringPtr("engine.example.com:443"), Token: StringPtr("test-token"),
					Transport:       handshakeTestDialer{conn: &observedWriteConn{Conn: conn, started: writing}},
					TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS12},
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				wantErr := context.Canceled
				if cancellation == "deadline" {
					var cancelDeadline context.CancelFunc
					ctx, cancelDeadline = context.WithTimeout(ctx, 250*time.Millisecond)
					defer cancelDeadline()
					wantErr = context.DeadlineExceeded
				}
				result := make(chan error, 1)
				go func() {
					channel, err := client.Connect(ctx, &Config{EnableHeartbeat: BoolPtr(false)})
					if channel != nil {
						_ = channel.Close()
					}
					result <- err
				}()
				select {
				case <-writing:
				case <-time.After(time.Second):
					t.Fatal("handshake write did not start")
				}
				if stage == "read" {
					if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
						t.Fatal(err)
					}
					if _, err := readPbMessage(bufio.NewReader(peer)); err != nil {
						t.Fatalf("read opening request: %v", err)
					}
				}
				if cancellation == "cancel" {
					cancel()
				}
				<-ctx.Done()
				select {
				case err := <-result:
					if !errors.Is(err, wantErr) {
						t.Fatalf("Connect() = %v, want %v", err, wantErr)
					}
				case <-time.After(time.Second):
					// Release the original implementation too: a failing regression
					// must not leave its Connect goroutine behind.
					_ = peer.Close()
					select {
					case <-result:
					case <-time.After(time.Second):
						t.Fatal("Connect did not return even after peer closure")
					}
					t.Fatal("Connect ignored cancellation during the handshake")
				}
			})
		}
	}
}

func TestConnectContextDoesNotOwnEstablishedChannel(t *testing.T) {
	dialer := newQueuedDialer(1)
	dialer.enqueue(serveControlChannelLifecycle)
	client := newTestClientWithDialer(dialer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	channel, err := client.Connect(ctx, &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	cancel()
	operationCtx, cancelOperation := context.WithTimeout(t.Context(), time.Second)
	defer cancelOperation()
	if _, err := channel.CreateTunnel(operationCtx, TunnelProperties{Name: StringPtr("web")}); err != nil {
		t.Fatalf("successful handshake retained the setup cancellation: %v", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	dialer.wait(t, 1)
}

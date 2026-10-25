// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSessionIsolationPreservesActiveClientsAndServer(t *testing.T) {
	for _, operation := range []string{"terminate", "disconnect", "cancel reader"} {
		t.Run(operation, func(t *testing.T) {
			zero := time.Duration(0)
			handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
			server := httptest.NewServer(handler)
			defer server.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := handler.Shutdown(ctx); err != nil {
					t.Errorf("persistent server shutdown: %v", err)
				}
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			open := func(parent context.Context, mode string) *ClientSession {
				session, err := OpenClientSession(parent, &SessionConfig{
					URL: testWebTTYURL(server.URL), Interactive: true,
					CmdArgs: []string{os.Args[0], "-test.run=^TestWebTTYTerminationProcess$"},
					EnvVars: []string{"RSTREAM_WEBTTY_TERMINATION_HELPER=" + mode},
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				return session
			}
			read := func(session *ClientSession, expected string) {
				var output strings.Builder
				for output.Len() < len(expected) {
					select {
					case event, ok := <-session.Events():
						if !ok {
							code, err := session.Wait()
							t.Fatalf("active session ended: code=%d error=%v output=%q", code, err, output.String())
						}
						output.Write(event.Data)
					case <-ctx.Done():
						t.Fatalf("active session did not respond: %v", ctx.Err())
					}
				}
				if output.String() != expected {
					t.Fatalf("session output=%q, expected=%q", output.String(), expected)
				}
			}
			firstCtx, cancelFirst := context.WithCancel(ctx)
			defer cancelFirst()
			first := open(firstCtx, "1")
			second := open(ctx, "probe")
			read(first, "ready\n")
			read(second, "ready\n")
			switch operation {
			case "terminate":
				if err := first.RequestTermination(ctx); err != nil {
					t.Fatal(err)
				}
				for range first.Events() {
				}
				want := 23
				if runtime.GOOS == "windows" {
					want = -1073741510
				}
				if code, err := first.Wait(); err != nil || code != want {
					t.Fatalf("stopped session result=%d, %v", code, err)
				}
			case "disconnect":
				_ = first.Close()
				if _, err := first.Wait(); !errors.Is(err, ErrSessionDisconnected) {
					t.Fatalf("disconnected reader result=%v", err)
				}
			case "cancel reader":
				cancelFirst()
				if _, err := first.Wait(); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled reader result=%v", err)
				}
			}
			if err := second.SendText("still active\n"); err != nil {
				t.Fatal(err)
			}
			read(second, "still active\n")
			third := open(ctx, "probe")
			read(third, "ready\n")
			for _, session := range []*ClientSession{second, third} {
				if err := session.SendText("finish\n"); err != nil {
					t.Fatal(err)
				}
				for range session.Events() {
				}
				if code, err := session.Wait(); err != nil || code != 23 {
					t.Fatalf("unrelated session result=%d, %v", code, err)
				}
			}
		})
	}
}

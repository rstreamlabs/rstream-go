// See LICENSE file in the project root for license information.

package rtc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"
)

func testRelay(t *testing.T) []webrtc.ICEServer {
	t.Helper()
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := turn.NewServer(turn.ServerConfig{Realm: "files-test", AuthHandler: func(request *turn.RequestAttributes) (string, []byte, bool) {
		return request.Username, turn.GenerateAuthKey("reader", "files-test", "test-only-password"), request.Username == "reader"
	}, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: listener, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}}}})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close() })
	return []webrtc.ICEServer{{URLs: []string{"turn:" + listener.LocalAddr().String() + "?transport=udp"}, Username: "reader", Credential: "test-only-password"}}
}

func testService(t *testing.T, config ServerConfig) (*Server, *httptest.Server) {
	t.Helper()
	service := NewServer(config)
	server := httptest.NewServer(service)
	t.Cleanup(func() { server.Close(); _ = service.Close() })
	return service, server
}

func awaitClosed(t *testing.T, server *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for server.ActiveSessions() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.ActiveSessions() != 0 {
		t.Fatalf("leaked %d filesystem peers", server.ActiveSessions())
	}
}

func TestTransferDirectAndTURN(t *testing.T) {
	for _, relay := range []bool{false, true} {
		t.Run(fmt.Sprintf("relay=%v", relay), func(t *testing.T) {
			var servers []webrtc.ICEServer
			if relay {
				servers = testRelay(t)
			}
			payload := bytes.Repeat([]byte("rstream WebRTC file\x00"), 200000)
			service, server := testService(t, ServerConfig{ICE: func(context.Context) ([]webrtc.ICEServer, error) { return servers, nil }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/fs/file.txt" || r.Header.Get("Range") != "bytes=7-" {
					t.Errorf("unexpected request: %s %v", r.URL, r.Header)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				http.ServeContent(w, r, "file.txt", time.Unix(1, 0), bytes.NewReader(payload))
			})})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/file.txt", nil)
			request.Header.Set("Range", "bytes=7-")
			response, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL, Info: Info{ICEServers: servers, LeaseSeconds: 90}, RelayOnly: relay})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			hash := sha256.New()
			count, err := io.Copy(hash, response.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := sha256.Sum256(payload[7:])
			if response.StatusCode != 206 || count != int64(len(payload)-7) || !bytes.Equal(hash.Sum(nil), want[:]) {
				t.Fatalf("invalid transferred file: status=%d bytes=%d", response.StatusCode, count)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			awaitClosed(t, service)
		})
	}
}

func TestCancellationAndBoundedSlowConsumer(t *testing.T) {
	var produced atomic.Int64
	service, server := testService(t, ServerConfig{MaxSessions: 1, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, ChunkSize)
		for {
			count, err := w.Write(chunk)
			produced.Add(int64(count))
			if err != nil {
				return
			}
		}
	})})
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/fs/huge", nil)
	response, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL, Info: Info{LeaseSeconds: 90}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	time.Sleep(150 * time.Millisecond)
	if produced.Load() > Window*ChunkSize || service.ActiveSessions() != 1 {
		t.Fatalf("unbounded producer or missing peer: bytes=%d sessions=%d", produced.Load(), service.ActiveSessions())
	}
	second, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL, Info: Info{LeaseSeconds: 90}})
	if err == nil {
		_ = second.Body.Close()
		t.Fatal("session limit did not reject a second reader")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	awaitClosed(t, service)
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWebRTCWriteRejectedBeforeReadingUpload(t *testing.T) {
	service, server := testService(t, ServerConfig{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("write reached filesystem") })})
	for _, method := range []string{"PUT", "DELETE", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "PATCH"} {
		request, _ := http.NewRequest(method, server.URL+"/fs/file", nil)
		response, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 403 || !strings.Contains(string(data), "read-only") {
			t.Fatalf("%s: expected clear read-only error", method)
		}
		input := Request{Method: method, URI: "/fs/file"}
		err = SignalHTTP(context.Background(), server.Client(), server.URL, nil, Signal{Action: "offer", Request: &input}, nil)
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("server accepted %s offer: %v", method, err)
		}
	}
	if service.ActiveSessions() != 0 {
		t.Fatal("writes created a WebRTC peer")
	}
}

func TestLeaseAndICERestartPreserveTransfer(t *testing.T) {
	servers := testRelay(t)
	var issued atomic.Int32
	service, server := testService(t, ServerConfig{LeaseDuration: 3 * time.Second, RestartInterval: time.Second, ICE: func(context.Context) ([]webrtc.ICEServer, error) {
		issued.Add(1)
		return servers, nil
	}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 80 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := w.Write(bytes.Repeat([]byte("r"), ChunkSize)); err != nil {
				return
			}
		}
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/slow", nil)
	response, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL, Info: Info{ICEServers: servers, LeaseSeconds: 3, RestartSeconds: 1}, RelayOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	count, err := io.Copy(io.Discard, response.Body)
	if err != nil || count != 80*ChunkSize {
		t.Fatalf("restart interrupted transfer: bytes=%d error=%v", count, err)
	}
	if issued.Load() < 3 {
		t.Fatal("transfer did not obtain fresh ICE credentials")
	}
	_ = response.Body.Close()
	awaitClosed(t, service)
}

func TestAuthorizationRenewalFailureStopsPeer(t *testing.T) {
	var revoked atomic.Bool
	service := NewServer(ServerConfig{LeaseDuration: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for {
			if _, err := w.Write(make([]byte, ChunkSize)); err != nil {
				return
			}
		}
	})})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() {
			http.Error(w, "Session revoked", http.StatusUnauthorized)
			return
		}
		service.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/file", nil)
	response, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL, Info: Info{LeaseSeconds: 3}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	revoked.Store(true)
	_, err = io.Copy(io.Discard, response.Body)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("revoked authorization did not interrupt the transfer: %v", err)
	}
	_ = response.Body.Close()
	awaitClosed(t, service)
}

func TestIncompleteResponseIsNeverSuccessful(t *testing.T) {
	for _, length := range []string{"5", "1", "invalid"} {
		t.Run(length, func(t *testing.T) {
			_, server := testService(t, ServerConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", length)
				_, _ = w.Write([]byte("abc"))
			})})
			request, _ := http.NewRequest(http.MethodGet, server.URL+"/fs/file", nil)
			response, err := RoundTrip(request, ClientConfig{HTTP: server.Client(), Endpoint: server.URL})
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			if err == nil {
				t.Fatal("incomplete or invalid response reported success")
			}
		})
	}
}

func TestShutdownOwnsPendingICEAndBoundsAdmission(t *testing.T) {
	started := make(chan struct{}, 1)
	service := NewServer(ServerConfig{MaxSessions: 1, ICE: func(ctx context.Context) ([]webrtc.ICEServer, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	server := httptest.NewServer(service)
	defer server.Close()
	defer service.Close()
	request := Signal{Action: "offer", SDP: "pending", Request: &Request{Method: "GET", URI: "/fs/file"}}
	finished := make(chan error, 1)
	go func() { finished <- SignalHTTP(context.Background(), server.Client(), server.URL, nil, request, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("ICE provider never started")
	}
	if err := SignalHTTP(context.Background(), server.Client(), server.URL, nil, request, nil); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("pending setup escaped admission limit: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("closed setup succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown leaked pending setup")
	}
}

func TestSignalingDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	err := SignalHTTP(context.Background(), server.Client(), server.URL, http.Header{"Authorization": []string{"Bearer fixture"}}, Signal{Action: "renew"}, nil)
	if err == nil || !strings.Contains(err.Error(), "307") || reached.Load() {
		t.Fatalf("signaling redirect escaped origin: %v reached=%v", err, reached.Load())
	}
}

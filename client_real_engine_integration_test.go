//go:build integration

// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRealEnginePrivateBytestreamLivenessAndConcurrency(t *testing.T) {
	engine := requiredRealEngineEnvironment(t, "RSTREAM_GO_E2E_ENGINE")
	token := requiredRealEngineEnvironment(t, "RSTREAM_GO_E2E_TOKEN")
	client := newRealEngineClient(t, engine, token, false)
	t.Cleanup(func() { assertRealEngineClose(t, "client", client.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	heartbeatInterval := time.Second
	control, err := client.Connect(ctx, &Config{HeartbeatInterval: &heartbeatInterval})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { assertRealEngineClose(t, "control channel", control.Close()) })
	name := "go-e2e-" + uuid.NewString()[:12]
	created, err := control.CreateTunnel(ctx, TunnelProperties{Name: &name, Type: TunnelTypePtr(TunnelTypeBytestream), Publish: BoolPtr(false)})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	tunnel, ok := created.(BytestreamTunnel)
	if !ok {
		t.Fatalf("CreateTunnel() = %T, want BytestreamTunnel", created)
	}
	t.Cleanup(func() { assertRealEngineClose(t, "tunnel", tunnel.Close()) })
	properties, err := tunnel.Properties()
	if err != nil {
		t.Fatalf("Properties() error = %v", err)
	}
	if properties.ID == nil || *properties.ID == "" {
		t.Fatalf("Properties().ID = %#v", properties.ID)
	}
	for _, target := range []string{name, *properties.ID} {
		for _, zeroRTT := range []bool{false, true} {
			dialClient := newRealEngineClient(t, engine, token, zeroRTT)
			if err := realEngineRoundTrip(ctx, dialClient, tunnel, target, []byte(fmt.Sprintf("%s:%t", target, zeroRTT))); err != nil {
				_ = dialClient.Close()
				t.Fatalf("round trip target=%q zero_rtt=%t: %v", target, zeroRTT, err)
			}
			assertRealEngineClose(t, "dial client", dialClient.Close())
		}
	}
	const concurrentDialers = 16
	errorsCh := make(chan error, concurrentDialers)
	var waitGroup sync.WaitGroup
	for index := range concurrentDialers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			dialClient, err := newRealEngineClientForGoroutine(engine, token, index%2 == 0)
			if err != nil {
				errorsCh <- fmt.Errorf("create dial client: %w", err)
				return
			}
			defer dialClient.Close()
			payload := []byte(fmt.Sprintf("concurrent-%02d", index))
			errorsCh <- realEngineRoundTrip(ctx, dialClient, tunnel, name, payload)
		}()
	}
	waitGroup.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent round trip error = %v", err)
		}
	}
	time.Sleep(2200 * time.Millisecond)
	if err := realEngineRoundTrip(ctx, client, tunnel, name, []byte("after-heartbeats")); err != nil {
		t.Fatalf("round trip after heartbeats error = %v", err)
	}
	assertRealEngineClose(t, "tunnel", tunnel.Close())
	assertRealEngineClose(t, "control channel", control.Close())
}

func TestRealEngineControlChannelSurvivesTemporaryNetworkPartition(t *testing.T) {
	engine := requiredRealEngineEnvironment(t, "RSTREAM_GO_E2E_ENGINE")
	token := requiredRealEngineEnvironment(t, "RSTREAM_GO_E2E_TOKEN")
	proxy := newPausingTCPProxy(t, engine)
	client := newRealEngineClient(t, proxy.Address(), token, false)
	t.Cleanup(func() { assertRealEngineClose(t, "client", client.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	heartbeatInterval := time.Second
	control, err := client.Connect(ctx, &Config{HeartbeatInterval: &heartbeatInterval})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { assertRealEngineClose(t, "control channel", control.Close()) })
	name := "go-partition-e2e-" + uuid.NewString()[:12]
	created, err := control.CreateTunnel(ctx, TunnelProperties{Name: &name, Type: TunnelTypePtr(TunnelTypeBytestream), Publish: BoolPtr(false)})
	if err != nil {
		t.Fatalf("CreateTunnel() before temporary partition error = %v", err)
	}
	tunnel, ok := created.(BytestreamTunnel)
	if !ok {
		t.Fatalf("CreateTunnel() = %T, want BytestreamTunnel", created)
	}
	t.Cleanup(func() { assertRealEngineClose(t, "tunnel", tunnel.Close()) })
	dialClient := newRealEngineClient(t, engine, token, false)
	t.Cleanup(func() { assertRealEngineClose(t, "dial client", dialClient.Close()) })
	dialConnection, acceptedConnection := establishRealEngineStream(t, ctx, dialClient, tunnel, name)
	t.Cleanup(func() { assertRealEngineClose(t, "dial connection", dialConnection.Close()) })
	t.Cleanup(func() { assertRealEngineClose(t, "accepted connection", acceptedConnection.Close()) })
	assertRealEngineStreamExchange(t, dialConnection, acceptedConnection, []byte("before-temporary-partition"))
	proxy.Pause()
	time.Sleep(4200 * time.Millisecond)
	assertRealEngineStreamExchange(t, dialConnection, acceptedConnection, []byte("during-temporary-partition"))
	proxy.Resume()
	if err := realEngineRoundTrip(ctx, dialClient, tunnel, name, []byte("after-temporary-partition")); err != nil {
		t.Fatalf("round trip after temporary partition error = %v", err)
	}
	assertRealEngineClose(t, "dial client", dialClient.Close())
}

func establishRealEngineStream(t *testing.T, ctx context.Context, client *Client, tunnel BytestreamTunnel, target string) (net.Conn, net.Conn) {
	t.Helper()
	acceptedCh := make(chan struct {
		connection net.Conn
		err        error
	}, 1)
	go func() {
		connection, err := tunnel.Accept()
		acceptedCh <- struct {
			connection net.Conn
			err        error
		}{connection: connection, err: err}
	}()
	dialConnection, err := client.Dial(ctx, Addr{IdOrName: target})
	if err != nil {
		t.Fatalf("dial established stream: %v", err)
	}
	select {
	case accepted := <-acceptedCh:
		if accepted.err != nil {
			_ = dialConnection.Close()
			t.Fatalf("accept established stream: %v", accepted.err)
		}
		return dialConnection, accepted.connection
	case <-ctx.Done():
		_ = dialConnection.Close()
		t.Fatalf("accept established stream: %v", context.Cause(ctx))
		return nil, nil
	}
}

func assertRealEngineStreamExchange(t *testing.T, dialConnection, acceptedConnection net.Conn, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	if err := dialConnection.SetDeadline(deadline); err != nil {
		t.Fatalf("set dial connection deadline: %v", err)
	}
	if err := acceptedConnection.SetDeadline(deadline); err != nil {
		t.Fatalf("set accepted connection deadline: %v", err)
	}
	if _, err := dialConnection.Write(payload); err != nil {
		t.Fatalf("write established stream request: %v", err)
	}
	request := make([]byte, len(payload))
	if _, err := io.ReadFull(acceptedConnection, request); err != nil {
		t.Fatalf("read established stream request: %v", err)
	}
	if string(request) != string(payload) {
		t.Fatalf("established stream request = %q, want %q", request, payload)
	}
	response := []byte(strings.ToUpper(string(request)))
	if _, err := acceptedConnection.Write(response); err != nil {
		t.Fatalf("write established stream response: %v", err)
	}
	got := make([]byte, len(response))
	if _, err := io.ReadFull(dialConnection, got); err != nil {
		t.Fatalf("read established stream response: %v", err)
	}
	if string(got) != string(response) {
		t.Fatalf("established stream response = %q, want %q", got, response)
	}
}

func requiredRealEngineEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for real-engine integration tests", name)
	}
	return value
}

func newRealEngineClient(t *testing.T, engine, token string, zeroRTT bool) *Client {
	t.Helper()
	client, err := newRealEngineClientForGoroutine(engine, token, zeroRTT)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func newRealEngineClientForGoroutine(engine, token string, zeroRTT bool) (*Client, error) {
	var tlsConfig *tls.Config
	if os.Getenv("RSTREAM_GO_E2E_TLS_INSECURE") == "1" {
		// #nosec G402 -- explicit opt-in for locally generated integration certificates.
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return NewClient(ClientOptions{Engine: engine, Token: token, TLSClientConfig: tlsConfig, ZeroRTT: &zeroRTT})
}

func realEngineRoundTrip(ctx context.Context, client *Client, tunnel BytestreamTunnel, target string, payload []byte) error {
	if client == nil {
		return errors.New("create dial client")
	}
	serverResult := make(chan error, 1)
	go func() {
		conn, err := tunnel.Accept()
		if err != nil {
			serverResult <- fmt.Errorf("accept: %w", err)
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			serverResult <- fmt.Errorf("set accepted connection deadline: %w", err)
			return
		}
		request := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, request); err != nil {
			serverResult <- fmt.Errorf("read accepted connection: %w", err)
			return
		}
		_, err = conn.Write([]byte(strings.ToUpper(string(request))))
		serverResult <- err
	}()
	conn, err := client.Dial(ctx, Addr{IdOrName: target})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set dial connection deadline: %w", err)
	}
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, response); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if want := strings.ToUpper(string(payload)); string(response) != want {
		return fmt.Errorf("response = %q, want %q", response, want)
	}
	select {
	case err := <-serverResult:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func assertRealEngineClose(t *testing.T, resource string, err error) {
	t.Helper()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("close %s: %v", resource, err)
	}
}

type pausingTCPProxy struct {
	listener    net.Listener
	address     string
	upstream    string
	context     context.Context
	cancel      context.CancelFunc
	gate        pauseGate
	mutex       sync.Mutex
	connections map[net.Conn]struct{}
	waitGroup   sync.WaitGroup
	serveDone   chan struct{}
	closeOnce   sync.Once
	closed      bool
	firstError  error
}

func newPausingTCPProxy(t *testing.T, upstream string) *pausingTCPProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen pausing TCP proxy: %v", err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatalf("split pausing TCP proxy address: %v", err)
	}
	proxyContext, cancel := context.WithCancel(t.Context())
	proxy := &pausingTCPProxy{
		listener:    listener,
		address:     net.JoinHostPort("localhost", port),
		upstream:    upstream,
		context:     proxyContext,
		cancel:      cancel,
		connections: make(map[net.Conn]struct{}),
		serveDone:   make(chan struct{}),
	}
	go proxy.serve()
	t.Cleanup(func() {
		if err := proxy.Close(); err != nil {
			t.Errorf("close pausing TCP proxy: %v", err)
		}
	})
	return proxy
}

func (proxy *pausingTCPProxy) Address() string {
	return proxy.address
}

func (proxy *pausingTCPProxy) Pause() {
	proxy.gate.Pause()
}

func (proxy *pausingTCPProxy) Resume() {
	proxy.gate.Resume()
}

func (proxy *pausingTCPProxy) Close() error {
	proxy.closeOnce.Do(func() {
		proxy.cancel()
		proxy.gate.Resume()
		_ = proxy.listener.Close()
		proxy.mutex.Lock()
		proxy.closed = true
		for connection := range proxy.connections {
			_ = connection.Close()
		}
		proxy.mutex.Unlock()
		select {
		case <-proxy.serveDone:
		case <-time.After(5 * time.Second):
			proxy.recordError(errors.New("timed out waiting for pausing TCP proxy listener to stop"))
		}
		waitDone := make(chan struct{})
		go func() {
			proxy.waitGroup.Wait()
			close(waitDone)
		}()
		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
			proxy.recordError(errors.New("timed out waiting for pausing TCP proxy connections to stop"))
		}
	})
	proxy.mutex.Lock()
	defer proxy.mutex.Unlock()
	return proxy.firstError
}

func (proxy *pausingTCPProxy) serve() {
	defer close(proxy.serveDone)
	for {
		downstream, err := proxy.listener.Accept()
		if err != nil {
			if proxy.context.Err() == nil {
				proxy.recordError(fmt.Errorf("accept downstream: %w", err))
			}
			return
		}
		proxy.waitGroup.Add(1)
		go proxy.forward(downstream)
	}
}

func (proxy *pausingTCPProxy) forward(downstream net.Conn) {
	defer proxy.waitGroup.Done()
	upstream, err := (&net.Dialer{}).DialContext(proxy.context, "tcp", proxy.upstream)
	if err != nil {
		_ = downstream.Close()
		if proxy.context.Err() == nil {
			proxy.recordError(fmt.Errorf("dial upstream: %w", err))
		}
		return
	}
	if !proxy.track(downstream, upstream) {
		_ = downstream.Close()
		_ = upstream.Close()
		return
	}
	defer proxy.untrack(downstream, upstream)
	done := make(chan struct{}, 2)
	go proxy.copy(upstream, downstream, done)
	go proxy.copy(downstream, upstream, done)
	<-done
	_ = downstream.Close()
	_ = upstream.Close()
	<-done
}

func (proxy *pausingTCPProxy) copy(destination, source net.Conn, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	buffer := make([]byte, 32*1024)
	for {
		read, err := source.Read(buffer)
		if read > 0 {
			if waitErr := proxy.gate.Wait(proxy.context); waitErr != nil {
				return
			}
			for written := 0; written < read; {
				count, writeErr := destination.Write(buffer[written:read])
				if writeErr != nil || count == 0 {
					return
				}
				written += count
			}
		}
		if err != nil {
			return
		}
	}
}

func (proxy *pausingTCPProxy) track(connections ...net.Conn) bool {
	proxy.mutex.Lock()
	defer proxy.mutex.Unlock()
	if proxy.closed {
		return false
	}
	for _, connection := range connections {
		proxy.connections[connection] = struct{}{}
	}
	return true
}

func (proxy *pausingTCPProxy) untrack(connections ...net.Conn) {
	proxy.mutex.Lock()
	defer proxy.mutex.Unlock()
	for _, connection := range connections {
		delete(proxy.connections, connection)
	}
}

func (proxy *pausingTCPProxy) recordError(err error) {
	proxy.mutex.Lock()
	defer proxy.mutex.Unlock()
	if proxy.firstError == nil {
		proxy.firstError = err
	}
}

type pauseGate struct {
	mutex  sync.Mutex
	paused bool
	resume chan struct{}
}

func (gate *pauseGate) Pause() {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	if gate.paused {
		return
	}
	gate.paused = true
	gate.resume = make(chan struct{})
}

func (gate *pauseGate) Resume() {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	if !gate.paused {
		return
	}
	close(gate.resume)
	gate.paused = false
}

func (gate *pauseGate) Wait(ctx context.Context) error {
	gate.mutex.Lock()
	if !gate.paused {
		gate.mutex.Unlock()
		return nil
	}
	resume := gate.resume
	gate.mutex.Unlock()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-resume:
		return nil
	}
}

// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestClientEngineAndDetailsResolution(t *testing.T) {
	client := &Client{}
	if _, err := client.getEngine(); err == nil || !strings.Contains(err.Error(), "engine URL is required") {
		t.Fatalf("expected missing engine error, got %v", err)
	}
	engine := "engine.example.com:443"
	client.EngineURL = &engine
	got, err := client.getEngine()
	if err != nil || got != &engine {
		t.Fatalf("getEngine() = %v, %v", got, err)
	}
	if _, err := client.getClientDetails(&engine, nil); err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("expected missing token error, got %v", err)
	}
	client.NoToken = BoolPtr(true)
	details, err := client.getClientDetails(&engine, nil)
	if err != nil {
		t.Fatalf("getClientDetails() with NoToken error = %v", err)
	}
	if details.Token != nil {
		t.Fatalf("NoToken client should not populate token: %#v", details.Token)
	}
	token := "token"
	client.NoToken = nil
	client.Token = &token
	details, err = client.getClientDetails(&engine, nil)
	if err != nil {
		t.Fatalf("getClientDetails() with token error = %v", err)
	}
	if details.Token == nil || *details.Token != "token" {
		t.Fatalf("token not propagated: %#v", details.Token)
	}
	client.TLSClientConfig = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{{1}}}}}
	if _, err := client.getClientDetails(&engine, nil); err == nil || !strings.Contains(err.Error(), "token and mTLS authentication cannot be used together") {
		t.Fatalf("expected token/mTLS conflict, got %v", err)
	}
	proxySecret := "proxy-secret"
	details, err = client.getProxyClientDetails(&engine, &proxySecret)
	if err != nil {
		t.Fatalf("getProxyClientDetails() with mTLS and proxy secret error = %v", err)
	}
	if details.Token == nil || *details.Token != proxySecret {
		t.Fatalf("proxy secret not propagated: %#v", details.Token)
	}
	details, err = client.getProxyClientDetails(&engine, nil)
	if err != nil {
		t.Fatalf("getProxyClientDetails() with mTLS and no proxy secret error = %v", err)
	}
	if details.Token != nil {
		t.Fatalf("proxy details without a secret should not fall back to agent token: %#v", details.Token)
	}
}

func TestToServerDetails(t *testing.T) {
	if toServerDetails(nil) != nil {
		t.Fatalf("nil server details should stay nil")
	}
	got := toServerDetails(&pb.ServerDetails{
		Agent:    stringPbValueOrNil(StringPtr("agent")),
		Channel:  stringPbValueOrNil(StringPtr("dev")),
		Version:  stringPbValueOrNil(StringPtr("1.2.3")),
		Plan:     stringPbValueOrNil(StringPtr("pro")),
		Provider: stringPbValueOrNil(StringPtr("aws")),
		Region:   stringPbValueOrNil(StringPtr("eu-west-1")),
		Update:   stringPbValueOrNil(StringPtr("available")),
	})
	if got.Agent == nil || *got.Agent != "agent" || got.Update == nil || *got.Update != "available" {
		t.Fatalf("unexpected server details: %#v", got)
	}
}

func TestIsNilDialerHandlesTypedNil(t *testing.T) {
	var transport *Transport
	if !isNilDialer(transport) {
		t.Fatalf("typed nil transport should be treated as nil")
	}
	if !isNilDialer(nil) {
		t.Fatalf("nil dialer should be treated as nil")
	}
	if isNilDialer(&Transport{}) {
		t.Fatalf("non-nil transport should not be treated as nil")
	}
}

func TestDialEngineHTTP1RejectsMissingAddress(t *testing.T) {
	client := &Client{}
	if _, err := client.DialEngineHTTP1(context.Background(), " "); err == nil || !strings.Contains(err.Error(), "engine address") {
		t.Fatalf("expected address error, got %v", err)
	}
}

func TestDialEngineWithTransportUsesConfiguredALPN(t *testing.T) {
	engine := "engine.example.com:443"
	nextProtos := []string{"http/1.1"}
	dialer := &recordingDialer{
		dial: func(context.Context, string, *tls.Config) (net.Conn, error) {
			return stubConn{}, nil
		},
	}
	client := &Client{}
	conn, err := client.dialEngineWithTransport(context.Background(), &engine, &nextProtos, dialer)
	if err != nil {
		t.Fatalf("dialEngineWithTransport() error = %v", err)
	}
	_ = conn.Close()
	if len(dialer.calls) != 1 {
		t.Fatalf("dial calls = %d, want 1", len(dialer.calls))
	}
	if got := dialer.calls[0].NextProtos; len(got) != 1 || got[0] != "http/1.1" {
		t.Fatalf("NextProtos = %#v", got)
	}
	if dialer.calls[0].ServerName != "engine.example.com" {
		t.Fatalf("ServerName = %q", dialer.calls[0].ServerName)
	}
}

func TestControlChannelConnectCreateTunnelAndClose(t *testing.T) {
	serverErr := make(chan error, 1)
	dialer := pipeDialer{serve: func(conn net.Conn) {
		serverErr <- serveControlChannelLifecycle(conn)
	}}
	engine := "engine.example.com:443"
	token := "token"
	client := &Client{
		EngineURL: &engine,
		Token:     &token,
		Transport: dialer,
		TLSClientConfig: &tls.Config{
			MaxVersion: tls.VersionTLS12,
		},
	}
	channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	details := channel.ServerDetails()
	if details == nil || details.Agent == nil || *details.Agent != "engine" {
		t.Fatalf("unexpected server details: %#v", details)
	}
	tunnel, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("web"), Type: TunnelTypePtr(TunnelTypeBytestream)})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	props, err := tunnel.Properties()
	if err != nil {
		t.Fatalf("Properties() error = %v", err)
	}
	if props.ID == nil || *props.ID != "tun-1" || props.Name == nil || *props.Name != "web" {
		t.Fatalf("unexpected tunnel properties: %#v", props)
	}
	bytestream, ok := tunnel.(BytestreamTunnel)
	if !ok {
		t.Fatalf("CreateTunnel() returned %T, want BytestreamTunnel", tunnel)
	}
	if addr := bytestream.Addr().String(); addr != "tun-1" {
		t.Fatalf("tunnel Addr() = %q", addr)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
	if got := channel.Err(); got != nil {
		t.Fatalf("channel Err() = %v", got)
	}
	select {
	case <-channel.Done():
	default:
		t.Fatalf("Done() should be closed after Close()")
	}
}

func TestAutoTransportDoesNotFallbackAfterEngineProtocolError(t *testing.T) {
	quic := newQueuedDialer(1)
	quic.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		msg, err := readPbMessage(reader)
		if err != nil {
			return err
		}
		if msg.GetOpenControlChannelReq() == nil {
			return errUnexpectedTestMessage("OpenControlChannelReq")
		}
		return writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{Payload: &pb.OpenControlChannelRsp_Error{Error: &pb.Error{Code: 401, Message: wrapperspb.String("unauthorized")}}}}})
	})
	tlsFallback := &autoTestDialer{}
	delay := time.Hour
	transport := &AutoTransport{quicDialer: quic, tlsDialer: tlsFallback, FallbackDelay: &delay}
	engine := "engine.example.com:443"
	token := "invalid-token"
	client := &Client{EngineURL: &engine, Token: &token, Transport: transport, TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS12}}
	_, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("Connect() error = %v", err)
	}
	if transport.SelectedMode() != TunnelTransportModeQUIC || tlsFallback.callCount() != 0 {
		t.Fatalf("protocol error changed selection: mode=%q tls calls=%d", transport.SelectedMode(), tlsFallback.callCount())
	}
	quic.wait(t, 1)
}

func TestControlChannelCreateTunnelReportsEngineAndMalformedResponses(t *testing.T) {
	tests := []struct {
		name      string
		response  *pb.OpenTunnelRsp
		wantError string
		wantCode  *EngineErrorCode
	}{
		{
			name: "engine error",
			response: &pb.OpenTunnelRsp{Payload: &pb.OpenTunnelRsp_Error{Error: &pb.Error{
				Code:    pb.ErrorCode_ERROR_CODE_FEATURE_NOT_AVAILABLE,
				Message: wrapperspb.String("forbidden"),
			}}},
			wantError: "engine error 5000: forbidden",
			wantCode:  engineErrorCodePtr(EngineErrorCodeFeatureNotAvailable),
		},
		{
			name:      "missing tunnel id",
			response:  &pb.OpenTunnelRsp{Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: toTunnelPropertiesPb(TunnelProperties{Name: StringPtr("web")})}},
			wantError: "engine did not return a tunnel ID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialer := newQueuedDialer(1)
			dialer.enqueue(func(conn net.Conn) error {
				return serveCreateTunnelResponse(conn, tt.response)
			})
			client := newTestClientWithDialer(dialer)
			channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
			if err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
			_, err = channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("web")})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("CreateTunnel() error = %v, want containing %q", err, tt.wantError)
			}
			var engineErr *EngineError
			if tt.wantCode != nil && (!errors.As(err, &engineErr) || engineErr.Code != *tt.wantCode) {
				t.Fatalf("CreateTunnel() error = %#v, want EngineError code %d", err, *tt.wantCode)
			}
			if tt.wantCode == nil && errors.As(err, &engineErr) {
				t.Fatalf("CreateTunnel() error = %#v, want non-engine error", err)
			}
			if err := channel.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			dialer.wait(t, 1)
		})
	}
}

func engineErrorCodePtr(value EngineErrorCode) *EngineErrorCode {
	return &value
}

func TestControlChannelCreateTunnelDoesNotWriteAfterCancellation(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	lifecycleCtx, lifecycleCancel := context.WithCancel(t.Context())
	defer lifecycleCancel()
	channel := &controlChannelImpl{
		conn:             clientConn,
		w:                bufio.NewWriter(clientConn),
		doneCh:           make(chan error, 1),
		closedCh:         make(chan struct{}),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		lifecycleCtx:     lifecycleCtx,
		lifecycleCancel:  lifecycleCancel,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := channel.CreateTunnel(ctx, TunnelProperties{Name: StringPtr("web")}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CreateTunnel() error = %v, want context deadline exceeded", err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if msg, err := readPbMessage(bufio.NewReader(serverConn)); err == nil {
		t.Fatalf("received %T after CreateTunnel cancellation", msg.Payload)
	}
}

func TestControlChannelCreateTunnelCancellationWhileWriterBusy(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	observedConn := &observedWriteConn{Conn: clientConn, started: make(chan struct{})}
	lifecycleCtx, lifecycleCancel := context.WithCancel(t.Context())
	defer lifecycleCancel()
	channel := &controlChannelImpl{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		conn:             observedConn,
		w:                bufio.NewWriter(observedConn),
		r:                bufio.NewReader(observedConn),
		doneCh:           make(chan error, 1),
		closedCh:         make(chan struct{}),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		lifecycleCtx:     lifecycleCtx,
		lifecycleCancel:  lifecycleCancel,
	}
	go channel.readLoop()
	firstResult := make(chan error, 1)
	go func() {
		_, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("first")})
		firstResult <- err
	}()
	select {
	case <-observedConn.started:
	case <-time.After(time.Second):
		t.Fatal("first tunnel write did not start")
	}
	secondCtx, secondCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer secondCancel()
	if _, err := channel.CreateTunnel(secondCtx, TunnelProperties{Name: StringPtr("second")}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second CreateTunnel() error = %v, want context deadline exceeded", err)
	}
	reader := bufio.NewReader(serverConn)
	writer := bufio.NewWriter(serverConn)
	msg, err := readPbMessage(reader)
	if err != nil {
		t.Fatalf("read first OpenTunnelReq: %v", err)
	}
	req := msg.GetOpenTunnelReq()
	if req == nil {
		t.Fatalf("received %T, want OpenTunnelReq", msg.Payload)
	}
	props := TunnelProperties{ID: StringPtr("tun-first"), Name: StringPtr("first"), Type: TunnelTypePtr(TunnelTypeBytestream)}
	rsp := &pb.OpenTunnelRsp{RequestId: req.RequestId, Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: toTunnelPropertiesPb(props)}}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: rsp}}); err != nil {
		t.Fatalf("write first OpenTunnelRsp: %v", err)
	}
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatalf("first CreateTunnel() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first CreateTunnel() remained blocked")
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if msg, err := readPbMessage(reader); err == nil {
		t.Fatalf("received queued %T after second CreateTunnel cancellation", msg.Payload)
	}
}

func TestControlChannelHeartbeatSendsPeriodicMessage(t *testing.T) {
	heartbeatSeen := make(chan struct{}, 1)
	serverErr := make(chan error, 1)
	dialer := pipeDialer{serve: func(conn net.Conn) {
		serverErr <- serveControlChannelHeartbeat(conn, heartbeatSeen)
	}}
	engine := "engine.example.com:443"
	token := "token"
	interval := 10 * time.Millisecond
	client := &Client{EngineURL: &engine, Token: &token, Transport: dialer}
	channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(true), HeartbeatInterval: &interval})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	select {
	case <-heartbeatSeen:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for heartbeat")
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestControlChannelHeartbeatDoesNotQueueWrites(t *testing.T) {
	writer := newBlockingControlWriter()
	interval := time.Millisecond
	lifecycleCtx, lifecycleCancel := context.WithCancel(t.Context())
	defer lifecycleCancel()
	channel := &controlChannelImpl{
		heartbeatInterval: interval,
		w:                 bufio.NewWriter(writer),
		doneCh:            make(chan error, 1),
		closedCh:          make(chan struct{}),
		pendingTunnels:    make(map[string]*pendingOpenTunnelReq),
		tunnels:           make(map[string]*bytestreamTunnelImpl),
		proxyTransports:   make(map[string]Dialer),
		datagramTunnels:   make(map[string]*quicDatagramListener),
		datagramChannels:  make(map[datagramChannelID]*quicDatagramChannel),
		lifecycleCtx:      lifecycleCtx,
		lifecycleCancel:   lifecycleCancel,
	}
	baseline := runtime.NumGoroutine()
	done := make(chan struct{})
	go func() {
		channel.heartbeatLoop()
		close(done)
	}()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("heartbeat write did not start")
	}
	time.Sleep(20 * time.Millisecond)
	goroutineGrowth := runtime.NumGoroutine() - baseline
	close(writer.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not stop after write failure")
	}
	time.Sleep(20 * time.Millisecond)
	if got := writer.writeCount(); got != 1 {
		t.Fatalf("heartbeat writes = %d, want 1", got)
	}
	if goroutineGrowth > 8 {
		t.Fatalf("blocked heartbeat grew goroutine count by %d, want at most 8", goroutineGrowth)
	}
}

func TestTunnelCloseDoesNotWaitIndefinitelyForServer(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	lifecycleCtx, lifecycleCancel := context.WithCancel(t.Context())
	closeTimeout := 20 * time.Millisecond
	channel := &controlChannelImpl{
		conn:             clientConn,
		w:                bufio.NewWriter(clientConn),
		doneCh:           make(chan error, 1),
		closedCh:         make(chan struct{}),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		lifecycleCtx:     lifecycleCtx,
		lifecycleCancel:  lifecycleCancel,
		closeTimeout:     closeTimeout,
	}
	tunnelCtx, tunnelCancel := context.WithCancel(lifecycleCtx)
	tunnel := &bytestreamTunnelImpl{ctrl: channel, tunnelID: "tun-1", closedCh: make(chan struct{}), conns: make(chan net.Conn, 1), ctx: tunnelCtx, cancel: tunnelCancel}
	channel.tunnels[tunnel.tunnelID] = tunnel
	requestSeen := make(chan struct{})
	go func() {
		msg, err := readPbMessage(bufio.NewReader(serverConn))
		if err == nil && msg.GetCloseTunnelReq() != nil {
			close(requestSeen)
		}
		_, _ = io.Copy(io.Discard, serverConn)
	}()
	result := make(chan error, 1)
	go func() { result <- tunnel.Close() }()
	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		channel.onError(errors.New("test cleanup"))
		t.Fatal("CloseTunnelReq was not sent")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("Close() error = %v, want timeout", err)
		}
	case <-time.After(5 * closeTimeout):
		channel.onError(errors.New("test cleanup"))
		t.Fatal("Tunnel.Close() remained blocked")
	}
}

func TestControlChannelCloseDoesNotWaitIndefinitelyForServer(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	lifecycleCtx, lifecycleCancel := context.WithCancel(t.Context())
	closeTimeout := 20 * time.Millisecond
	channel := &controlChannelImpl{
		conn:             clientConn,
		w:                bufio.NewWriter(clientConn),
		doneCh:           make(chan error, 1),
		closedCh:         make(chan struct{}),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		lifecycleCtx:     lifecycleCtx,
		lifecycleCancel:  lifecycleCancel,
		closeTimeout:     closeTimeout,
	}
	requestSeen := make(chan struct{})
	go func() {
		msg, err := readPbMessage(bufio.NewReader(serverConn))
		if err == nil && msg.GetCloseControlChannelReq() != nil {
			close(requestSeen)
		}
		_, _ = io.Copy(io.Discard, serverConn)
	}()
	result := make(chan error, 1)
	go func() { result <- channel.Close() }()
	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		channel.onError(errors.New("test cleanup"))
		t.Fatal("CloseControlChannelReq was not sent")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("Close() error = %v, want timeout", err)
		}
	case <-time.After(5 * closeTimeout):
		channel.onError(errors.New("test cleanup"))
		t.Fatal("ControlChannel.Close() remained blocked")
	}
}

func TestClientDialWaitsForStreamResponseAndUsesReturnedConnection(t *testing.T) {
	dialer := newQueuedDialer(1)
	dialer.enqueue(func(conn net.Conn) error {
		return serveStreamDial(conn, "web", "token", "ping", "pong")
	})
	client := newTestClientWithDialer(dialer)
	conn, err := client.Dial(t.Context(), Addr{IdOrName: "web"})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("stream write error = %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("stream read error = %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("stream reply = %q, want pong", buf)
	}
	dialer.wait(t, 1)
}

func TestClientDialReportsStreamError(t *testing.T) {
	dialer := newQueuedDialer(1)
	dialer.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		msg, err := readPbMessage(reader)
		if err != nil {
			return err
		}
		if msg.GetStreamReq() == nil {
			return errUnexpectedTestMessage("StreamReq")
		}
		return writePbMessage(writer, &pb.Message{Payload: &pb.Message_StreamRsp{StreamRsp: &pb.StreamRsp{
			Payload: &pb.StreamRsp_Error{Error: &pb.Error{
				Code:    409,
				Message: wrapperspb.String("denied"),
			}},
		}}})
	})
	client := newTestClientWithDialer(dialer)
	_, err := client.Dial(t.Context(), Addr{IdOrName: "web"})
	if err == nil || !strings.Contains(err.Error(), "engine error 409: denied") {
		t.Fatalf("Dial() error = %v, want engine error", err)
	}
	dialer.wait(t, 1)
}

func TestClientPacketDialFramesPackets(t *testing.T) {
	dialer := newQueuedDialer(1)
	dialer.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		if _, err := expectStreamReq(reader, "datagrams", "token"); err != nil {
			return err
		}
		if err := writeStreamRsp(writer, "stream-1"); err != nil {
			return err
		}
		payload, err := readMessage(reader)
		if err != nil {
			return err
		}
		if string(payload) != "packet" {
			return fmt.Errorf("framed payload = %q, want packet", payload)
		}
		return writeMessage(writer, []byte("reply"))
	})
	client := newTestClientWithDialer(dialer)
	packetConn, err := client.PacketDial(t.Context(), Addr{IdOrName: "datagrams"})
	if err != nil {
		t.Fatalf("PacketDial() error = %v", err)
	}
	defer packetConn.Close()
	remote := Addr{IdOrName: "datagrams"}
	if n, err := packetConn.WriteTo([]byte("packet"), &remote); err != nil || n != len("packet") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("packet"))
	}
	if err := packetConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 16)
	n, addr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "reply" || addr.String() != "datagrams" {
		t.Fatalf("ReadFrom() = %q from %v, want reply from datagrams", buf[:n], addr)
	}
	dialer.wait(t, 1)
}

func TestClientPacketDialUsesQUICDatagramChannel(t *testing.T) {
	done := make(chan struct{})
	dialer := newQueuedDatagramDialer(1)
	dialer.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		streamReq, err := expectStreamReq(reader, "datagrams", "token")
		if err != nil {
			return err
		}
		if !streamReq.GetDatagramChannel().GetValue() {
			return errors.New("StreamReq datagram_channel = false, want true")
		}
		if err := writeStreamRsp(writer, "01020304-0000-0000-0000-000000000000"); err != nil {
			return err
		}
		<-done
		return nil
	})
	client := newTestClientWithDatagramDialer(dialer)
	packetConn, err := client.PacketDial(t.Context(), Addr{IdOrName: "datagrams"})
	if err != nil {
		t.Fatalf("PacketDial() error = %v", err)
	}
	defer packetConn.Close()
	channelID := mustDatagramChannelID(t, "01020304-0000-0000-0000-000000000000")
	remote := Addr{IdOrName: "datagrams"}
	if n, err := packetConn.WriteTo([]byte("packet"), &remote); err != nil || n != len("packet") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("packet"))
	}
	sent := dialer.sentDatagram(t)
	if string(sent[:datagramChannelIDSize]) != string(channelID[:]) || string(sent[datagramChannelIDSize:]) != "packet" {
		t.Fatalf("unexpected datagram frame: %#v", sent)
	}
	dialer.receiveDatagram(t, channelID, []byte("reply"))
	buf := make([]byte, 16)
	n, addr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "reply" || addr.String() != "datagrams" {
		t.Fatalf("ReadFrom() = %q from %v, want reply from datagrams", buf[:n], addr)
	}
	close(done)
	if err := packetConn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	dialer.wait(t, 1)
}

func TestClientPacketDialUsesDatagramsAfterAutoSelectsQUIC(t *testing.T) {
	done := make(chan struct{})
	quic := newQueuedDatagramDialer(1)
	quic.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		streamReq, err := expectStreamReq(reader, "datagrams", "token")
		if err != nil {
			return err
		}
		if !streamReq.GetDatagramChannel().GetValue() {
			return errors.New("StreamReq datagram_channel = false, want true")
		}
		if err := writeStreamRsp(writer, "01020304-0000-0000-0000-000000000000"); err != nil {
			return err
		}
		<-done
		return nil
	})
	delay := time.Hour
	auto := &AutoTransport{quicDialer: quic, tlsDialer: &autoTestDialer{}, FallbackDelay: &delay}
	client := newTestClientWithDatagramDialer(quic)
	client.Transport = auto
	packetConn, err := client.PacketDial(t.Context(), Addr{IdOrName: "datagrams"})
	if err != nil {
		t.Fatalf("PacketDial() error = %v", err)
	}
	if _, ok := packetConn.(*quicDatagramChannel); !ok || auto.SelectedMode() != TunnelTransportModeQUIC {
		t.Fatalf("PacketDial() = %T, selection=%q", packetConn, auto.SelectedMode())
	}
	close(done)
	_ = packetConn.Close()
	quic.wait(t, 1)
}

func TestClientPacketDialUsesFramingAfterAutoFallsBackToTLS(t *testing.T) {
	tlsDialer := newQueuedDialer(2)
	tlsDialer.enqueue(func(conn net.Conn) error {
		buffer := make([]byte, 1)
		_, err := conn.Read(buffer)
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("selection stream read error = %v, want EOF", err)
		}
		return nil
	})
	tlsDialer.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		streamReq, err := expectStreamReq(reader, "datagrams", "token")
		if err != nil {
			return err
		}
		if streamReq.GetDatagramChannel().GetValue() {
			return errors.New("framed fallback requested a QUIC datagram channel")
		}
		return writeStreamRsp(writer, "stream-1")
	})
	quic := &autoTestDialer{err: errors.New("udp blocked")}
	delay := time.Hour
	auto := &AutoTransport{quicDialer: quic, tlsDialer: tlsDialer, FallbackDelay: &delay}
	engine := "engine.example.com:443"
	token := "token"
	client := &Client{EngineURL: &engine, Token: &token, ZeroRTT: BoolPtr(false), Transport: auto, TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS12}}
	conn, err := client.PacketDial(t.Context(), Addr{IdOrName: "datagrams"})
	if err != nil {
		t.Fatalf("PacketDial() error = %v", err)
	}
	if _, ok := conn.(*connWrapper); !ok || auto.SelectedMode() != TunnelTransportModeTLS {
		t.Fatalf("PacketDial() = %T, selection=%q", conn, auto.SelectedMode())
	}
	_ = conn.Close()
	tlsDialer.wait(t, 2)
}

func TestClientPacketDialFallsBackWhenQUICDatagramChannelIsUnavailable(t *testing.T) {
	dialer := newQueuedDatagramDialer(2)
	dialer.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		streamReq, err := expectStreamReq(reader, "datagrams", "token")
		if err != nil {
			return err
		}
		if !streamReq.GetDatagramChannel().GetValue() {
			return errors.New("StreamReq datagram_channel = false, want true")
		}
		return writePbMessage(writer, &pb.Message{Payload: &pb.Message_StreamRsp{StreamRsp: &pb.StreamRsp{
			Payload: &pb.StreamRsp_Error{Error: &pb.Error{
				Code:    pb.ErrorCode_ERROR_CODE_INVALID_REQUEST,
				Message: wrapperspb.String("QUIC datagrams are not available for this tunnel."),
			}},
		}}})
	})
	dialer.enqueue(func(conn net.Conn) error {
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		streamReq, err := expectStreamReq(reader, "datagrams", "token")
		if err != nil {
			return err
		}
		if streamReq.GetDatagramChannel().GetValue() {
			return errors.New("fallback StreamReq datagram_channel = true, want false")
		}
		if err := writeStreamRsp(writer, "stream-1"); err != nil {
			return err
		}
		payload, err := readMessage(reader)
		if err != nil {
			return err
		}
		if string(payload) != "packet" {
			return fmt.Errorf("framed payload = %q, want packet", payload)
		}
		return writeMessage(writer, []byte("reply"))
	})
	client := newTestClientWithDatagramDialer(dialer)
	packetConn, err := client.PacketDial(t.Context(), Addr{IdOrName: "datagrams"})
	if err != nil {
		t.Fatalf("PacketDial() error = %v", err)
	}
	defer packetConn.Close()
	remote := Addr{IdOrName: "datagrams"}
	if n, err := packetConn.WriteTo([]byte("packet"), &remote); err != nil || n != len("packet") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("packet"))
	}
	buf := make([]byte, 16)
	n, addr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "reply" || addr.String() != "datagrams" {
		t.Fatalf("ReadFrom() = %q from %v, want reply from datagrams", buf[:n], addr)
	}
	dialer.wait(t, 2)
}

func TestControlChannelAcceptsProxyConnectionAndClosesTunnel(t *testing.T) {
	testControlChannelAcceptsProxyConnection(t, "")
}

func TestControlChannelAcceptsProxyConnectionAtIngressEndpoint(t *testing.T) {
	testControlChannelAcceptsProxyConnection(t, "ingress.example.com:443")
}

func TestControlChannelLossCancelsRedirectedProxyDial(t *testing.T) {
	dialer := newControlThenBlockingDialer()
	client := newTestClientWithDialer(&dialer.queuedDialer)
	client.Transport = dialer
	channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("web"), Type: TunnelTypePtr(TunnelTypeBytestream)}); err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	select {
	case <-dialer.proxyDialCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("redirected proxy dial was not canceled with the control channel")
	}
	select {
	case err := <-channel.Done():
		if err == nil {
			t.Fatal("control channel closed without reporting the lost connection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("control channel did not close")
	}
	if err := <-dialer.serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestControlChannelConcurrentCloseReturnsConsistentError(t *testing.T) {
	want := errors.New("control connection lost")
	channel := &controlChannelImpl{
		conn:             stubConn{},
		doneCh:           make(chan error, 1),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		closing:          true,
		lifecycleCtx:     t.Context(),
		lifecycleCancel:  func() {},
	}
	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			results <- channel.Close()
		}()
	}
	close(start)
	time.Sleep(20 * time.Millisecond)
	channel.onError(want)
	for i := 0; i < callers; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, want) {
				t.Fatalf("Close() error = %v, want %v", err, want)
			}
		case <-time.After(time.Second):
			t.Fatal("Close() remained blocked")
		}
	}
}

func TestTunnelConcurrentWaitersReceiveClosureCause(t *testing.T) {
	for _, operation := range []string{"accept", "close"} {
		t.Run(operation, func(t *testing.T) {
			channel := &controlChannelImpl{closedCh: make(chan struct{})}
			tunnel := &bytestreamTunnelImpl{
				ctrl:     channel,
				closedCh: make(chan struct{}),
				closing:  operation == "close",
				conns:    make(chan net.Conn),
			}
			const callers = 16
			start := make(chan struct{})
			results := make(chan error, callers)
			for i := 0; i < callers; i++ {
				go func() {
					<-start
					if operation == "accept" {
						_, err := tunnel.Accept()
						results <- err
						return
					}
					results <- tunnel.Close()
				}()
			}
			close(start)
			time.Sleep(20 * time.Millisecond)
			want := errors.New("tunnel connection lost")
			tunnel.onError(want)
			for i := 0; i < callers; i++ {
				select {
				case err := <-results:
					if !errors.Is(err, want) {
						t.Fatalf("%s error = %v, want %v", operation, err, want)
					}
				case <-time.After(time.Second):
					t.Fatalf("%s remained blocked", operation)
				}
			}
		})
	}
}

func TestTunnelCloseReleasesQueuedConnections(t *testing.T) {
	channel := &controlChannelImpl{}
	client, server := net.Pipe()
	defer server.Close()
	tunnel := &bytestreamTunnelImpl{
		ctrl:     channel,
		closedCh: make(chan struct{}),
		conns:    make(chan net.Conn, 1),
	}
	tunnel.conns <- client
	tunnel.onError(errors.New("tunnel connection lost"))
	result := make(chan error, 1)
	go func() {
		_, err := server.Read(make([]byte, 1))
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("queued connection remained open after tunnel closure")
		}
	case <-time.After(time.Second):
		t.Fatal("queued connection remained blocked after tunnel closure")
	}
}

func TestControlChannelGracefulCloseDoesNotInventTunnelError(t *testing.T) {
	channel := &controlChannelImpl{
		conn:             stubConn{},
		doneCh:           make(chan error, 1),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		lifecycleCtx:     t.Context(),
		lifecycleCancel:  func() {},
	}
	tunnel := &bytestreamTunnelImpl{ctrl: channel, closedCh: make(chan struct{}), conns: make(chan net.Conn, 1)}
	channel.tunnels["tunnel"] = tunnel
	channel.onError(nil)
	if tunnel.err != nil {
		t.Fatalf("graceful tunnel close error = %v, want nil", tunnel.err)
	}
}

func TestControlChannelErrorCleanupDoesNotHoldStateLockDuringIO(t *testing.T) {
	conn := &blockingCloseControlConn{started: make(chan struct{}), release: make(chan struct{})}
	channel := &controlChannelImpl{
		conn:             conn,
		w:                bufio.NewWriter(failingControlWriter{}),
		doneCh:           make(chan error, 1),
		closedCh:         make(chan struct{}),
		pendingTunnels:   make(map[string]*pendingOpenTunnelReq),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
	}
	go channel.sendProxyConnRsp("stream", errors.New("rejected"))
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("connection cleanup did not start")
	}
	errCh := make(chan error, 1)
	go func() { errCh <- channel.Err() }()
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "failed to send ProxyConnRsp") {
			t.Fatalf("Err() = %v, want proxy response write error", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("state lock remained held during connection cleanup")
	}
	select {
	case <-channel.closedCh:
		t.Fatal("control channel signaled completion before cleanup finished")
	default:
	}
	close(conn.release)
	select {
	case <-channel.closedCh:
	case <-time.After(time.Second):
		t.Fatal("control channel cleanup did not finish")
	}
}

func TestControlChannelWriteRequiresLifecycleContext(t *testing.T) {
	channel := &controlChannelImpl{w: bufio.NewWriter(io.Discard)}
	err := channel.writePbMessage(&pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	if err == nil || !strings.Contains(err.Error(), "lifecycle context") {
		t.Fatalf("writePbMessage() error = %v, want missing lifecycle context", err)
	}
}

func TestTunnelCloseCancelsRedirectedProxyDial(t *testing.T) {
	dialer := newControlThenBlockingDialer()
	dialer.calls = 1
	engine := "engine.example.com:443"
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	defer lifecycleCancel()
	tunnelCtx, tunnelCancel := context.WithCancel(lifecycleCtx)
	channel := &controlChannelImpl{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:           &Client{EngineURL: &engine, Transport: dialer, TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS12}},
		conn:             stubConn{},
		w:                bufio.NewWriter(stubConn{}),
		doneCh:           make(chan error, 1),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		lifecycleCtx:     lifecycleCtx,
		lifecycleCancel:  lifecycleCancel,
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
	}
	tunnel := &bytestreamTunnelImpl{ctrl: channel, tunnelID: "tun-1", closedCh: make(chan struct{}), conns: make(chan net.Conn, 1), ctx: tunnelCtx, cancel: tunnelCancel}
	channel.tunnels[tunnel.tunnelID] = tunnel
	channel.mu.Lock()
	channel.handleProxyConnReq(&pb.ProxyConnReq{TunnelId: tunnel.tunnelID, StreamId: "stream-1", Secret: wrapperspb.String("proxy-secret"), ProxyEndpoint: wrapperspb.String("ingress.example.com:443")})
	channel.mu.Unlock()
	select {
	case <-dialer.proxyDialStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("redirected proxy dial did not start")
	}
	channel.mu.Lock()
	cleanup := tunnel.onCloseLocked()
	channel.mu.Unlock()
	cleanup.run()
	select {
	case <-dialer.proxyDialCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("redirected proxy dial was not canceled with the tunnel")
	}
}

func TestDeliverProxyConnectionWaitsForListenerCapacity(t *testing.T) {
	channel := &controlChannelImpl{}
	tunnel := &bytestreamTunnelImpl{conns: make(chan net.Conn, 1)}
	first := stubConn{}
	second := stubConn{}
	tunnel.conns <- first
	result := make(chan error, 1)
	go func() { result <- channel.deliverProxyConnection(t.Context(), tunnel, second) }()
	select {
	case err := <-result:
		t.Fatalf("deliverProxyConnection() returned before capacity was available: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	<-tunnel.conns
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("deliverProxyConnection() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deliverProxyConnection() did not observe available capacity")
	}
	if got := <-tunnel.conns; got != second {
		t.Fatalf("delivered connection = %#v, want second connection", got)
	}
}

func TestDeliverProxyConnectionStopsWhenTunnelCloses(t *testing.T) {
	channel := &controlChannelImpl{}
	tunnelCtx, cancel := context.WithCancel(t.Context())
	tunnel := &bytestreamTunnelImpl{conns: make(chan net.Conn, 1)}
	tunnel.conns <- stubConn{}
	cancel()
	if err := channel.deliverProxyConnection(tunnelCtx, tunnel, stubConn{}); err == nil || err.Error() != "tunnel is closing or closed" {
		t.Fatalf("deliverProxyConnection() error = %v", err)
	}
}

func TestControlChannelRejectsInvalidProxyRedirectsBeforeDial(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *wrapperspb.StringValue
		secret   *wrapperspb.StringValue
	}{
		{name: "empty endpoint", endpoint: wrapperspb.String(""), secret: wrapperspb.String("proxy-secret")},
		{name: "missing secret", endpoint: wrapperspb.String("ingress.example.com:443")},
		{name: "empty secret", endpoint: wrapperspb.String("ingress.example.com:443"), secret: wrapperspb.String("")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialer := newQueuedDialer(1)
			response := make(chan *pb.Error, 1)
			dialer.enqueue(func(conn net.Conn) error {
				return serveControlChannelWithProxyConnection(conn, test.endpoint, test.secret, response)
			})
			client := newTestClientWithDialer(dialer)
			channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
			if err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
			tunnel, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("web"), Type: TunnelTypePtr(TunnelTypeBytestream)})
			if err != nil {
				t.Fatalf("CreateTunnel() error = %v", err)
			}
			select {
			case responseErr := <-response:
				if responseErr == nil || responseErr.Code != pb.ErrorCode_ERROR_CODE_SERVICE_UNAVAILABLE {
					t.Fatalf("ProxyConnRsp error = %#v, want service unavailable", responseErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for ProxyConnRsp")
			}
			if err := tunnel.Close(); err != nil {
				t.Fatalf("tunnel Close() error = %v", err)
			}
			if err := channel.Close(); err != nil {
				t.Fatalf("channel Close() error = %v", err)
			}
			dialer.wait(t, 1)
			addresses := dialer.dialedAddresses(t, 1)
			if addresses[0] != "engine.example.com:443" {
				t.Fatalf("dialed addresses = %#v, want control connection only", addresses)
			}
		})
	}
}

func TestControlChannelRejectsProxyRequestWithoutTunnelLifecycle(t *testing.T) {
	controlClient, controlServer := net.Pipe()
	defer controlClient.Close()
	defer controlServer.Close()
	dialer := &countingFailDialer{}
	engine := "engine.example.com:443"
	token := "token"
	channel := &controlChannelImpl{
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:           &Client{EngineURL: &engine, Token: &token, Transport: dialer, TLSClientConfig: &tls.Config{MaxVersion: tls.VersionTLS12}},
		conn:             controlClient,
		w:                bufio.NewWriter(controlClient),
		tunnels:          make(map[string]*bytestreamTunnelImpl),
		proxyTransports:  make(map[string]Dialer),
		lifecycleCtx:     t.Context(),
		lifecycleCancel:  func() {},
		datagramTunnels:  make(map[string]*quicDatagramListener),
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
	}
	tunnel := &bytestreamTunnelImpl{ctrl: channel, tunnelID: "tun-1", closedCh: make(chan struct{}), conns: make(chan net.Conn, 1)}
	channel.tunnels[tunnel.tunnelID] = tunnel
	response := make(chan *pb.ProxyConnRsp, 1)
	go func() {
		reader := bufio.NewReader(controlServer)
		msg, err := readPbMessage(reader)
		if err != nil {
			response <- nil
			return
		}
		response <- msg.GetProxyConnRsp()
	}()
	channel.mu.Lock()
	channel.handleProxyConnReq(&pb.ProxyConnReq{TunnelId: tunnel.tunnelID, StreamId: "stream-1", Secret: wrapperspb.String("proxy-secret"), ProxyEndpoint: wrapperspb.String("ingress.example.com:443")})
	channel.mu.Unlock()
	select {
	case rsp := <-response:
		if rsp == nil || rsp.Error == nil || rsp.Error.Code != pb.ErrorCode_ERROR_CODE_SERVICE_UNAVAILABLE {
			t.Fatalf("ProxyConnRsp = %#v, want service unavailable", rsp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ProxyConnRsp")
	}
	if calls := dialer.calls.Load(); calls != 0 {
		t.Fatalf("proxy dial calls = %d, want 0", calls)
	}
}

func testControlChannelAcceptsProxyConnection(t *testing.T, proxyEndpoint string) {
	t.Helper()
	dialer := newQueuedDialer(2)
	dialer.enqueue(func(conn net.Conn) error { return serveControlChannelWithProxyConnectionAt(conn, proxyEndpoint) })
	dialer.enqueue(serveProxyConnection)
	client := newTestClientWithDialer(dialer)
	client.TLSClientConfig.ServerName = "engine.example.com"
	channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	tunnel, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("web"), Type: TunnelTypePtr(TunnelTypeBytestream)})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	listener, ok := tunnel.(BytestreamTunnel)
	if !ok {
		t.Fatalf("CreateTunnel() returned %T, want BytestreamTunnel", tunnel)
	}
	accepted := acceptBytestream(t, listener)
	defer accepted.Close()
	local, ok := accepted.LocalAddr().(*Addr)
	if !ok {
		t.Fatalf("LocalAddr() returned %T, want *Addr", accepted.LocalAddr())
	}
	if local.String() != "stream-1" || !local.SourceIP.Equal(net.IPv4(192, 0, 2, 10)) {
		t.Fatalf("LocalAddr() = %#v, want stream-1 from 192.0.2.10", local)
	}
	if accepted.RemoteAddr().String() != "tun-1" {
		t.Fatalf("RemoteAddr() = %q, want tun-1", accepted.RemoteAddr())
	}
	if err := accepted.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if err := accepted.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := accepted.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	if _, err := accepted.Write([]byte("hello")); err != nil {
		t.Fatalf("accepted connection write error = %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(accepted, buf); err != nil {
		t.Fatalf("accepted connection read error = %v", err)
	}
	if string(buf) != "world" {
		t.Fatalf("accepted connection reply = %q, want world", buf)
	}
	if err := tunnel.Close(); err != nil {
		t.Fatalf("tunnel Close() error = %v", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("channel Close() error = %v", err)
	}
	dialer.wait(t, 2)
	addresses := dialer.dialedAddresses(t, 2)
	wantProxyEndpoint := "engine.example.com:443"
	if proxyEndpoint != "" {
		wantProxyEndpoint = proxyEndpoint
	}
	if addresses[0] != "engine.example.com:443" || addresses[1] != wantProxyEndpoint {
		t.Fatalf("dialed addresses = %#v, want control then %s", addresses, wantProxyEndpoint)
	}
	configs := dialer.dialedTLSConfigs(t, 2)
	if configs[0].ServerName != "engine.example.com" {
		t.Fatalf("control connection server name = %q", configs[0].ServerName)
	}
	wantProxyServerName := "engine.example.com"
	if proxyEndpoint != "" {
		wantProxyServerName = "ingress.example.com"
	}
	if configs[1].ServerName != wantProxyServerName {
		t.Fatalf("proxy connection server name = %q, want %q", configs[1].ServerName, wantProxyServerName)
	}
}

func TestControlChannelDatagramProxyRoutesPacketsAndClosesChannels(t *testing.T) {
	dialer := newQueuedDatagramDialer(1)
	dialer.enqueue(serveControlChannelWithDatagramProxyConnection)
	client := newTestClientWithDatagramDialer(dialer)
	channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	ctrl := channel.(*controlChannelImpl)
	tunnel, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("dns"), Type: TunnelTypePtr(TunnelTypeDatagram)})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	listener, ok := tunnel.(DatagramTunnel)
	if !ok {
		t.Fatalf("CreateTunnel() returned %T, want DatagramTunnel", tunnel)
	}
	if listener.Addr().String() != "tun-dgram" {
		t.Fatalf("datagram listener Addr() = %v", listener.Addr())
	}
	if forwarding, err := tunnel.ForwardingAddress(); err != nil || forwarding == "" {
		t.Fatalf("ForwardingAddress() = %q, %v", forwarding, err)
	}
	if props, err := tunnel.Properties(); err != nil || props.ID == nil || *props.ID != "tun-dgram" {
		t.Fatalf("Properties() = %#v, %v", props, err)
	}
	packetConn, addr := acceptDatagram(t, listener)
	channelID := mustDatagramChannelID(t, "01020304-0000-0000-0000-000000000000")
	dialer.receiveDatagram(t, channelID, []byte("from-engine"))
	buf := make([]byte, 32)
	n, readAddr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "from-engine" || readAddr.String() != "01020304-0000-0000-0000-000000000000" {
		t.Fatalf("ReadFrom() = %q from %v", buf[:n], readAddr)
	}
	if n, err := packetConn.WriteTo([]byte("to-engine"), addr); err != nil || n != len("to-engine") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("to-engine"))
	}
	sent := dialer.sentDatagram(t)
	if string(sent[:datagramChannelIDSize]) != string(channelID[:]) || string(sent[datagramChannelIDSize:]) != "to-engine" {
		t.Fatalf("unexpected datagram frame: %#v", sent)
	}
	assertDatagramChannelPresent(t, ctrl, channelID, true)
	if err := tunnel.Close(); err != nil {
		t.Fatalf("tunnel Close() error = %v", err)
	}
	assertDatagramChannelPresent(t, ctrl, channelID, false)
	if _, _, err := packetConn.ReadFrom(buf); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadFrom() after tunnel close error = %v, want net.ErrClosed", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("channel Close() error = %v", err)
	}
	dialer.wait(t, 1)
}

func TestControlChannelRedirectedDatagramProxyUsesFramedStream(t *testing.T) {
	dialer := newQueuedDatagramDialer(2)
	dialer.enqueue(func(conn net.Conn) error {
		return serveControlChannelWithDatagramProxyConnectionAt(conn, wrapperspb.String("ingress.example.com:443"))
	})
	dialer.enqueue(serveRedirectedDatagramProxyConnection)
	client := newTestClientWithDatagramDialer(dialer)
	channel, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	tunnel, err := channel.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("dns"), Type: TunnelTypePtr(TunnelTypeDatagram)})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	listener, ok := tunnel.(DatagramTunnel)
	if !ok {
		t.Fatalf("CreateTunnel() returned %T, want DatagramTunnel", tunnel)
	}
	packetConn, addr := acceptDatagram(t, listener)
	defer packetConn.Close()
	if addr.String() != "01020304-0000-0000-0000-000000000000" {
		t.Fatalf("redirected datagram address = %q", addr)
	}
	buf := make([]byte, 32)
	n, readAddr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "from-ingress" || readAddr.String() != addr.String() {
		t.Fatalf("ReadFrom() = %q from %v", buf[:n], readAddr)
	}
	if n, err := packetConn.WriteTo([]byte("to-ingress"), addr); err != nil || n != len("to-ingress") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("to-ingress"))
	}
	if err := tunnel.Close(); err != nil {
		t.Fatalf("tunnel Close() error = %v", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("channel Close() error = %v", err)
	}
	dialer.wait(t, 2)
	addresses := dialer.dialedAddresses(t, 2)
	if addresses[0] != "engine.example.com:443" || addresses[1] != "ingress.example.com:443" {
		t.Fatalf("dialed addresses = %#v", addresses)
	}
}

func TestTunnelClosePrefersConfirmedTunnelResultOverControlChannelEOF(t *testing.T) {
	ctrl := &controlChannelImpl{err: io.EOF}
	tunnel := &bytestreamTunnelImpl{ctrl: ctrl}
	closedCh := make(chan struct{})
	close(closedCh)

	if err := tunnel.closeResultAfterControlClosed(closedCh); err != nil {
		t.Fatalf("close result = %v, want confirmed tunnel success", err)
	}
	tunnel.err = errors.New("tunnel rejected close")
	if err := tunnel.closeResultAfterControlClosed(closedCh); err == nil || err.Error() != "tunnel rejected close" {
		t.Fatalf("close result = %v, want tunnel-specific error", err)
	}
}

func TestControlChannelProxyTransportUsesOneQUICTransportPerEndpoint(t *testing.T) {
	tlsProxyConfig := &tls.Config{ServerName: "proxy.example.com"}
	selected := &QUICTransport{ForceIPv4: BoolPtr(true), ProxyHTTPHeaders: map[string]string{"X-Test": "value"}, TLSProxyConfig: tlsProxyConfig}
	auto := &AutoTransport{selected: selected, selectedMode: TunnelTransportModeQUIC}
	channel := &controlChannelImpl{client: &Client{Transport: auto}, proxyTransports: make(map[string]Dialer)}
	firstEndpoint := "ingress-a.example.com:443"
	secondEndpoint := "ingress-b.example.com:443"
	first := channel.proxyTransportLocked(&firstEndpoint)
	if first == selected {
		t.Fatalf("redirected proxy reused the control-channel QUIC transport")
	}
	firstQUIC, ok := first.(*QUICTransport)
	if !ok || firstQUIC.ForceIPv4 != selected.ForceIPv4 || firstQUIC.ProxyHTTPHeaders["X-Test"] != "value" || firstQUIC.TLSProxyConfig == tlsProxyConfig || firstQUIC.TLSProxyConfig.ServerName != tlsProxyConfig.ServerName {
		t.Fatalf("redirected proxy transport = %#v, want cloned QUIC configuration", first)
	}
	if again := channel.proxyTransportLocked(&firstEndpoint); again != first {
		t.Fatalf("same endpoint returned a different proxy transport")
	}
	if second := channel.proxyTransportLocked(&secondEndpoint); second == first || second == selected {
		t.Fatalf("second endpoint reused another endpoint transport")
	}
	selected.ProxyHTTPHeaders["X-Test"] = "changed"
	if firstQUIC.ProxyHTTPHeaders["X-Test"] != "value" {
		t.Fatalf("proxy headers were not cloned")
	}
	channel.conn = stubConn{}
	channel.doneCh = make(chan error, 1)
	channel.onError(nil)
	firstQUIC.mu.Lock()
	closeGeneration := firstQUIC.closeGeneration
	firstQUIC.mu.Unlock()
	if closeGeneration != 1 {
		t.Fatalf("proxy transport close generation = %d, want 1", closeGeneration)
	}
}

func TestControlChannelProxyTransportReusesStatelessDialer(t *testing.T) {
	selected := &Transport{ForceIPv4: BoolPtr(true)}
	channel := &controlChannelImpl{client: &Client{Transport: selected}, proxyTransports: make(map[string]Dialer)}
	endpoint := "ingress.example.com:443"
	if got := channel.proxyTransportLocked(&endpoint); got != selected {
		t.Fatalf("proxyTransportLocked() = %T, want configured TLS transport", got)
	}
	if got := channel.proxyTransportLocked(nil); got != nil {
		t.Fatalf("proxyTransportLocked(nil) = %T, want nil", got)
	}
}

func TestDatagramChannelCloseUnregistersFromControlChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	channelID := mustDatagramChannelID(t, "010203040000000000000001")
	ctrl := &controlChannelImpl{datagramChannels: make(map[datagramChannelID]*quicDatagramChannel)}
	channel := &quicDatagramChannel{channelID: channelID, ctx: ctx, cancel: cancel, recvCh: make(chan []byte, 1)}
	channel.onClose = func(ch *quicDatagramChannel) {
		ctrl.unregisterDatagramChannel(channelID, ch)
	}
	ctrl.datagramChannels[channelID] = channel
	if err := channel.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertDatagramChannelPresent(t, ctrl, channelID, false)
	if err := channel.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestDatagramProxyRejectsInvalidAndCollidingStreamIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listenerCtx, listenerCancel := context.WithCancel(context.Background())
	defer listenerCancel()
	ctrl := &controlChannelImpl{
		datagramProvider: &recordingDatagramProvider{},
		lifecycleCtx:     ctx,
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		datagramTunnels: map[string]*quicDatagramListener{
			"tun-dgram": {
				conns:  make(chan net.PacketConn, 4),
				ctx:    listenerCtx,
				cancel: listenerCancel,
				laddr:  stubNetAddr("tun-dgram"),
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	tunnel := &bytestreamTunnelImpl{tunnelID: "tun-dgram", ctx: ctx, cancel: cancel}
	if ctrl.handleDatagramProxyConnReq(&pb.ProxyConnReq{StreamId: "zzzzzzzz0000"}, tunnel) {
		t.Fatalf("invalid stream ID should not be accepted")
	}
	if len(ctrl.datagramChannels) != 0 || len(ctrl.datagramTunnels["tun-dgram"].conns) != 0 {
		t.Fatalf("invalid stream ID should not create channel")
	}
	if !ctrl.handleDatagramProxyConnReq(&pb.ProxyConnReq{StreamId: "010203040000000000000001"}, tunnel) {
		t.Fatalf("valid stream ID should be accepted")
	}
	firstID := mustDatagramChannelID(t, "010203040000000000000001")
	first := ctrl.datagramChannels[firstID]
	if first == nil || len(ctrl.datagramTunnels["tun-dgram"].conns) != 1 {
		t.Fatalf("first datagram channel was not registered")
	}
	secondID := mustDatagramChannelID(t, "01020304ffffffffffffffff")
	if !ctrl.handleDatagramProxyConnReq(&pb.ProxyConnReq{StreamId: "01020304ffffffffffffffff"}, tunnel) {
		t.Fatalf("same-prefix stream ID should be accepted with full-width channel routing")
	}
	if got := ctrl.datagramChannels[firstID]; got != first {
		t.Fatalf("first datagram channel was replaced")
	}
	if got := ctrl.datagramChannels[secondID]; got == nil || got == first {
		t.Fatalf("second datagram channel was not registered independently")
	}
	if len(ctrl.datagramTunnels["tun-dgram"].conns) != 2 {
		t.Fatalf("second datagram channel was not delivered to listener")
	}
}

func TestDatagramProxyChannelUsesTunnelLifecycle(t *testing.T) {
	ctrlCtx, ctrlCancel := context.WithCancel(t.Context())
	defer ctrlCancel()
	tunnelCtx, tunnelCancel := context.WithCancel(ctrlCtx)
	listenerCtx, listenerCancel := context.WithCancel(ctrlCtx)
	defer listenerCancel()
	ctrl := &controlChannelImpl{
		datagramProvider: &recordingDatagramProvider{},
		lifecycleCtx:     ctrlCtx,
		datagramChannels: make(map[datagramChannelID]*quicDatagramChannel),
		datagramTunnels: map[string]*quicDatagramListener{
			"tun-dgram": {
				conns:  make(chan net.PacketConn, 1),
				ctx:    listenerCtx,
				cancel: listenerCancel,
				laddr:  stubNetAddr("tun-dgram"),
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	tunnel := &bytestreamTunnelImpl{tunnelID: "tun-dgram", ctx: tunnelCtx, cancel: tunnelCancel}
	if !ctrl.handleDatagramProxyConnReq(&pb.ProxyConnReq{StreamId: "010203040000000000000001"}, tunnel) {
		t.Fatal("datagram proxy request was rejected")
	}
	channelID := mustDatagramChannelID(t, "010203040000000000000001")
	channel := ctrl.datagramChannels[channelID]
	if channel == nil {
		t.Fatal("datagram channel was not registered")
	}
	tunnelCancel()
	select {
	case <-channel.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("datagram channel outlived its tunnel")
	}
	channel.Close()
}

type pipeDialer struct {
	serve func(net.Conn)
}

type countingFailDialer struct {
	calls atomic.Int64
}

func (d *countingFailDialer) Dial(context.Context, string, *tls.Config) (net.Conn, error) {
	d.calls.Add(1)
	return nil, errors.New("unexpected dial")
}

func (d pipeDialer) Dial(_ context.Context, _ string, _ *tls.Config) (net.Conn, error) {
	client, server := net.Pipe()
	go d.serve(server)
	return client, nil
}

type queuedDialer struct {
	serves    chan func(net.Conn) error
	errs      chan error
	addresses chan string
	configs   chan *tls.Config
}

type controlThenBlockingDialer struct {
	queuedDialer
	mu                sync.Mutex
	calls             int
	proxyDialStarted  chan struct{}
	proxyDialCanceled chan struct{}
	serverErr         chan error
}

func newControlThenBlockingDialer() *controlThenBlockingDialer {
	return &controlThenBlockingDialer{
		queuedDialer:      *newQueuedDialer(1),
		proxyDialStarted:  make(chan struct{}),
		proxyDialCanceled: make(chan struct{}),
		serverErr:         make(chan error, 1),
	}
}

func (d *controlThenBlockingDialer) Dial(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
	d.mu.Lock()
	d.calls++
	call := d.calls
	d.mu.Unlock()
	if call == 1 {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			d.serverErr <- serveControlChannelUntilProxyDial(server, d.proxyDialStarted)
		}()
		return client, nil
	}
	close(d.proxyDialStarted)
	<-ctx.Done()
	close(d.proxyDialCanceled)
	return nil, ctx.Err()
}

type queuedDatagramDialer struct {
	*queuedDialer
	mu       sync.Mutex
	channels map[datagramChannelID]*quicDatagramChannel
	incoming chan []byte
	sent     chan []byte
}

func newQueuedDatagramDialer(size int) *queuedDatagramDialer {
	return &queuedDatagramDialer{
		queuedDialer: newQueuedDialer(size),
		channels:     make(map[datagramChannelID]*quicDatagramChannel),
		incoming:     make(chan []byte, 8),
		sent:         make(chan []byte, 8),
	}
}

func (d *queuedDatagramDialer) registerDatagramChannel(id datagramChannelID, ch *quicDatagramChannel) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.channels[id] != nil {
		return false
	}
	d.channels[id] = ch
	return true
}

func (d *queuedDatagramDialer) unregisterDatagramChannel(id datagramChannelID, ch *quicDatagramChannel) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.channels[id] == ch {
		delete(d.channels, id)
	}
}

func (d *queuedDatagramDialer) SendDatagram(data []byte) error {
	d.sent <- append([]byte(nil), data...)
	return nil
}

func (d *queuedDatagramDialer) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case data := <-d.incoming:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *queuedDatagramDialer) receiveDatagram(t *testing.T, channelID datagramChannelID, payload []byte) {
	t.Helper()
	frame := make([]byte, datagramChannelIDSize+len(payload))
	copy(frame[:datagramChannelIDSize], channelID[:])
	copy(frame[datagramChannelIDSize:], payload)
	d.mu.Lock()
	ch := d.channels[channelID]
	d.mu.Unlock()
	if ch != nil {
		select {
		case ch.recvCh <- append([]byte(nil), payload...):
		case <-time.After(time.Second):
			t.Fatalf("timed out routing datagram")
		}
		return
	}
	select {
	case d.incoming <- frame:
	case <-time.After(time.Second):
		t.Fatalf("timed out queueing datagram")
	}
}

func (d *queuedDatagramDialer) sentDatagram(t *testing.T) []byte {
	t.Helper()
	select {
	case data := <-d.sent:
		return data
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for sent datagram")
		return nil
	}
}

func newQueuedDialer(size int) *queuedDialer {
	return &queuedDialer{
		serves:    make(chan func(net.Conn) error, size),
		errs:      make(chan error, size),
		addresses: make(chan string, size),
		configs:   make(chan *tls.Config, size),
	}
}

func (d *queuedDialer) enqueue(serve func(net.Conn) error) {
	d.serves <- serve
}

func (d *queuedDialer) Dial(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
	var serve func(net.Conn) error
	select {
	case serve = <-d.serves:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Second):
		return nil, errors.New("test dialer has no server handler")
	}
	d.addresses <- address
	if config == nil {
		d.configs <- nil
	} else {
		d.configs <- config.Clone()
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		d.errs <- serve(server)
	}()
	return client, nil
}

func (d *queuedDialer) dialedTLSConfigs(t *testing.T, count int) []*tls.Config {
	t.Helper()
	configs := make([]*tls.Config, 0, count)
	for range count {
		select {
		case config := <-d.configs:
			configs = append(configs, config)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for dialed TLS configuration")
		}
	}
	return configs
}

func (d *queuedDialer) dialedAddresses(t *testing.T, count int) []string {
	t.Helper()
	addresses := make([]string, 0, count)
	for range count {
		select {
		case address := <-d.addresses:
			addresses = append(addresses, address)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for dialed address")
		}
	}
	return addresses
}

func (d *queuedDialer) wait(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case err := <-d.errs:
			if err != nil {
				t.Fatalf("server %d error = %v", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for server %d", i)
		}
	}
}

func newTestClientWithDialer(dialer *queuedDialer) *Client {
	engine := "engine.example.com:443"
	token := "token"
	return &Client{
		EngineURL: &engine,
		Token:     &token,
		ZeroRTT:   BoolPtr(false),
		Transport: dialer,
		TLSClientConfig: &tls.Config{
			MaxVersion: tls.VersionTLS12,
		},
	}
}

func newTestClientWithDatagramDialer(dialer *queuedDatagramDialer) *Client {
	engine := "engine.example.com:443"
	token := "token"
	return &Client{
		EngineURL: &engine,
		Token:     &token,
		ZeroRTT:   BoolPtr(false),
		Transport: dialer,
		TLSClientConfig: &tls.Config{
			MaxVersion: tls.VersionTLS12,
		},
	}
}

func serveControlChannelLifecycle(conn net.Conn) error {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errUnexpectedTestMessage("OpenControlChannelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{
		Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{
			ClientId: "client-1",
			ServerDetails: &pb.ServerDetails{
				Agent:   wrapperspb.String("engine"),
				Version: wrapperspb.String("1.2.3"),
			},
		}},
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	openTunnelReq := msg.GetOpenTunnelReq()
	if openTunnelReq == nil {
		return errUnexpectedTestMessage("OpenTunnelReq")
	}
	props := toTunnelProperties(openTunnelReq.TunnelProperties)
	if props.Name == nil || *props.Name != "web" {
		return errUnexpectedTestMessage("OpenTunnelReq with web name")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: &pb.OpenTunnelRsp{
		RequestId: openTunnelReq.RequestId,
		Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: toTunnelPropertiesPb(TunnelProperties{
			ID:   StringPtr("tun-1"),
			Name: props.Name,
			Type: TunnelTypePtr(TunnelTypeBytestream),
		})},
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetCloseControlChannelReq() == nil {
		return errUnexpectedTestMessage("CloseControlChannelReq")
	}
	return writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseControlChannelRsp{CloseControlChannelRsp: &pb.CloseControlChannelRsp{}}})
}

func serveControlChannelHeartbeat(conn net.Conn, heartbeatSeen chan<- struct{}) error {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errUnexpectedTestMessage("OpenControlChannelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{
		Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{ClientId: "client-1"}},
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetHeartbeat() == nil {
		return errUnexpectedTestMessage("Heartbeat")
	}
	heartbeatSeen <- struct{}{}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetCloseControlChannelReq() == nil {
		return errUnexpectedTestMessage("CloseControlChannelReq")
	}
	return writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseControlChannelRsp{CloseControlChannelRsp: &pb.CloseControlChannelRsp{}}})
}

func serveCreateTunnelResponse(conn net.Conn, rsp *pb.OpenTunnelRsp) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errUnexpectedTestMessage("OpenControlChannelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{
		Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{ClientId: "client-1"}},
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	openTunnelReq := msg.GetOpenTunnelReq()
	if openTunnelReq == nil {
		return errUnexpectedTestMessage("OpenTunnelReq")
	}
	rsp.RequestId = openTunnelReq.RequestId
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: rsp}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetCloseControlChannelReq() == nil {
		return errUnexpectedTestMessage("CloseControlChannelReq")
	}
	return writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseControlChannelRsp{CloseControlChannelRsp: &pb.CloseControlChannelRsp{}}})
}

func serveControlChannelWithProxyConnectionAt(conn net.Conn, proxyEndpoint string) error {
	var endpoint *wrapperspb.StringValue
	if proxyEndpoint != "" {
		endpoint = wrapperspb.String(proxyEndpoint)
	}
	return serveControlChannelWithProxyConnection(conn, endpoint, wrapperspb.String("proxy-secret"), nil)
}

func serveControlChannelUntilProxyDial(conn net.Conn, proxyDialStarted <-chan struct{}) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errUnexpectedTestMessage("OpenControlChannelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{ClientId: "client-1"}}}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	openTunnelReq := msg.GetOpenTunnelReq()
	if openTunnelReq == nil {
		return errUnexpectedTestMessage("OpenTunnelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: &pb.OpenTunnelRsp{RequestId: openTunnelReq.RequestId, Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: toTunnelPropertiesPb(TunnelProperties{ID: StringPtr("tun-1"), Name: StringPtr("web"), Type: TunnelTypePtr(TunnelTypeBytestream)})}}}}); err != nil {
		return err
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_ProxyConnReq{ProxyConnReq: &pb.ProxyConnReq{TunnelId: "tun-1", StreamId: "stream-1", Secret: wrapperspb.String("proxy-secret"), ProxyEndpoint: wrapperspb.String("ingress.example.com:443")}}}); err != nil {
		return err
	}
	select {
	case <-proxyDialStarted:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("timed out waiting for redirected proxy dial")
	}
}

func serveControlChannelWithProxyConnection(conn net.Conn, proxyEndpoint, secret *wrapperspb.StringValue, response chan<- *pb.Error) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errUnexpectedTestMessage("OpenControlChannelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{
		Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{ClientId: "client-1"}},
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	openTunnelReq := msg.GetOpenTunnelReq()
	if openTunnelReq == nil {
		return errUnexpectedTestMessage("OpenTunnelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: &pb.OpenTunnelRsp{
		RequestId: openTunnelReq.RequestId,
		Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: toTunnelPropertiesPb(TunnelProperties{
			ID:   StringPtr("tun-1"),
			Name: StringPtr("web"),
			Type: TunnelTypePtr(TunnelTypeBytestream),
		})},
	}}}); err != nil {
		return err
	}
	proxyReq := &pb.ProxyConnReq{
		TunnelId:      "tun-1",
		StreamId:      "stream-1",
		Secret:        secret,
		SourceIp:      &pb.IpAddress{Addr: &pb.IpAddress_V4{V4: 0xc000020a}},
		ProxyEndpoint: proxyEndpoint,
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_ProxyConnReq{ProxyConnReq: proxyReq}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	proxyConnRsp := msg.GetProxyConnRsp()
	if proxyConnRsp == nil || proxyConnRsp.StreamId != "stream-1" {
		return errUnexpectedTestMessage("ProxyConnRsp for stream-1")
	}
	if response != nil {
		response <- proxyConnRsp.Error
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	closeTunnelReq := msg.GetCloseTunnelReq()
	if closeTunnelReq == nil || closeTunnelReq.TunnelId != "tun-1" {
		return errUnexpectedTestMessage("CloseTunnelReq for tun-1")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseTunnelRsp{CloseTunnelRsp: &pb.CloseTunnelRsp{
		TunnelId: "tun-1",
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetCloseControlChannelReq() == nil {
		return errUnexpectedTestMessage("CloseControlChannelReq")
	}
	return writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseControlChannelRsp{CloseControlChannelRsp: &pb.CloseControlChannelRsp{}}})
}

func serveControlChannelWithDatagramProxyConnection(conn net.Conn) error {
	return serveControlChannelWithDatagramProxyConnectionAt(conn, nil)
}

func serveControlChannelWithDatagramProxyConnectionAt(conn net.Conn, endpoint *wrapperspb.StringValue) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errUnexpectedTestMessage("OpenControlChannelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{
		Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{ClientId: "client-1"}},
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	openTunnelReq := msg.GetOpenTunnelReq()
	if openTunnelReq == nil {
		return errUnexpectedTestMessage("OpenTunnelReq")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: &pb.OpenTunnelRsp{
		RequestId: openTunnelReq.RequestId,
		Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: toTunnelPropertiesPb(TunnelProperties{
			ID:   StringPtr("tun-dgram"),
			Name: StringPtr("dns"),
			Type: TunnelTypePtr(TunnelTypeDatagram),
		})},
	}}}); err != nil {
		return err
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_ProxyConnReq{ProxyConnReq: &pb.ProxyConnReq{
		TunnelId:      "tun-dgram",
		StreamId:      "01020304-0000-0000-0000-000000000000",
		Secret:        wrapperspb.String("unused-for-datagrams"),
		SourceIp:      &pb.IpAddress{Addr: &pb.IpAddress_V4{V4: 0xcb007101}},
		ProxyEndpoint: endpoint,
	}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	proxyConnRsp := msg.GetProxyConnRsp()
	if proxyConnRsp == nil || proxyConnRsp.StreamId != "01020304-0000-0000-0000-000000000000" {
		return errUnexpectedTestMessage("ProxyConnRsp for datagram stream")
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	closeTunnelReq := msg.GetCloseTunnelReq()
	if closeTunnelReq == nil || closeTunnelReq.TunnelId != "tun-dgram" {
		return errUnexpectedTestMessage("CloseTunnelReq for tun-dgram")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseTunnelRsp{CloseTunnelRsp: &pb.CloseTunnelRsp{TunnelId: "tun-dgram"}}}); err != nil {
		return err
	}
	msg, err = readPbMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetCloseControlChannelReq() == nil {
		return errUnexpectedTestMessage("CloseControlChannelReq")
	}
	return writePbMessage(writer, &pb.Message{Payload: &pb.Message_CloseControlChannelRsp{CloseControlChannelRsp: &pb.CloseControlChannelRsp{}}})
}

func serveRedirectedDatagramProxyConnection(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	proxyReq := msg.GetProxyReq()
	if proxyReq == nil || proxyReq.StreamId != "01020304-0000-0000-0000-000000000000" {
		return errUnexpectedTestMessage("ProxyReq for redirected datagram stream")
	}
	if got := proxyReq.GetClientDetails().GetToken().GetValue(); got != "unused-for-datagrams" {
		return fmt.Errorf("ProxyReq token = %q, want unused-for-datagrams", got)
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_ProxyRsp{ProxyRsp: &pb.ProxyRsp{}}}); err != nil {
		return err
	}
	if err := writeMessage(writer, []byte("from-ingress")); err != nil {
		return err
	}
	payload, err := readMessage(reader)
	if err != nil {
		return err
	}
	if string(payload) != "to-ingress" {
		return fmt.Errorf("redirected datagram payload = %q, want to-ingress", payload)
	}
	return nil
}

func serveProxyConnection(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readPbMessage(reader)
	if err != nil {
		return err
	}
	proxyReq := msg.GetProxyReq()
	if proxyReq == nil {
		return errUnexpectedTestMessage("ProxyReq")
	}
	if proxyReq.StreamId != "stream-1" {
		return fmt.Errorf("ProxyReq stream_id = %q, want stream-1", proxyReq.StreamId)
	}
	if got := proxyReq.GetClientDetails().GetToken().GetValue(); got != "proxy-secret" {
		return fmt.Errorf("ProxyReq token = %q, want proxy-secret", got)
	}
	if proxyReq.GetZeroRtt().GetValue() {
		return errors.New("ProxyReq zero_rtt = true, want false")
	}
	if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_ProxyRsp{ProxyRsp: &pb.ProxyRsp{}}}); err != nil {
		return err
	}
	if err := readExactString(reader, "hello"); err != nil {
		return err
	}
	if _, err := writer.WriteString("world"); err != nil {
		return err
	}
	return writer.Flush()
}

func serveStreamDial(conn net.Conn, wantTunnel, wantToken, wantPayload, reply string) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	if _, err := expectStreamReq(reader, wantTunnel, wantToken); err != nil {
		return err
	}
	if err := writeStreamRsp(writer, "stream-1"); err != nil {
		return err
	}
	if err := readExactString(reader, wantPayload); err != nil {
		return err
	}
	if _, err := writer.WriteString(reply); err != nil {
		return err
	}
	return writer.Flush()
}

func expectStreamReq(reader *bufio.Reader, wantTunnel, wantToken string) (*pb.StreamReq, error) {
	msg, err := readPbMessage(reader)
	if err != nil {
		return nil, err
	}
	streamReq := msg.GetStreamReq()
	if streamReq == nil {
		return nil, errUnexpectedTestMessage("StreamReq")
	}
	if streamReq.TunnelIdName != wantTunnel {
		return nil, fmt.Errorf("StreamReq tunnel_id_name = %q, want %q", streamReq.TunnelIdName, wantTunnel)
	}
	if got := streamReq.GetClientDetails().GetToken().GetValue(); got != wantToken {
		return nil, fmt.Errorf("StreamReq token = %q, want %q", got, wantToken)
	}
	if streamReq.GetZeroRtt().GetValue() {
		return nil, errors.New("StreamReq zero_rtt = true, want false")
	}
	return streamReq, nil
}

func writeStreamRsp(writer *bufio.Writer, streamID string) error {
	return writePbMessage(writer, &pb.Message{Payload: &pb.Message_StreamRsp{StreamRsp: &pb.StreamRsp{
		Payload: &pb.StreamRsp_StreamId{StreamId: streamID},
	}}})
}

func readExactString(reader *bufio.Reader, want string) error {
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(reader, buf); err != nil {
		return err
	}
	if string(buf) != want {
		return fmt.Errorf("read %q, want %q", buf, want)
	}
	return nil
}

func acceptBytestream(t *testing.T, listener BytestreamTunnel) net.Conn {
	t.Helper()
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan acceptResult, 1)
	go func() {
		conn, err := listener.Accept()
		resultCh <- acceptResult{conn: conn, err: err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("Accept() error = %v", result.err)
		}
		return result.conn
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for Accept()")
		return nil
	}
}

func acceptDatagram(t *testing.T, listener DatagramTunnel) (net.PacketConn, net.Addr) {
	t.Helper()
	type acceptResult struct {
		conn net.PacketConn
		addr net.Addr
		err  error
	}
	resultCh := make(chan acceptResult, 1)
	go func() {
		conn, addr, err := listener.Accept()
		resultCh <- acceptResult{conn: conn, addr: addr, err: err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("Accept() error = %v", result.err)
		}
		return result.conn, result.addr
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for datagram Accept()")
		return nil, nil
	}
}

func assertDatagramChannelPresent(t *testing.T, ctrl *controlChannelImpl, channelID datagramChannelID, want bool) {
	t.Helper()
	ctrl.mu.Lock()
	_, got := ctrl.datagramChannels[channelID]
	ctrl.mu.Unlock()
	if got != want {
		t.Fatalf("datagram channel %s present = %v, want %v", channelID.String(), got, want)
	}
}

func errUnexpectedTestMessage(want string) error {
	return &unexpectedTestMessageError{want: want}
}

type unexpectedTestMessageError struct {
	want string
}

func (e *unexpectedTestMessageError) Error() string {
	return "expected " + e.want
}

type failingControlWriter struct{}

func (failingControlWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type blockingControlWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	writes  int
}

func newBlockingControlWriter() *blockingControlWriter {
	return &blockingControlWriter{started: make(chan struct{}), release: make(chan struct{})}
}

func (w *blockingControlWriter) Write([]byte) (int, error) {
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	w.once.Do(func() { close(w.started) })
	<-w.release
	return 0, errors.New("write failed")
}

func (w *blockingControlWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes
}

type observedWriteConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *observedWriteConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(p)
}

type blockingCloseControlConn struct {
	stubConn
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingCloseControlConn) Close() error {
	c.once.Do(func() { close(c.started) })
	<-c.release
	return nil
}

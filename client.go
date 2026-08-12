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
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type Client struct {
	Transport       Dialer
	TLSClientConfig *tls.Config
	EngineURL       *string
	Token           *string
	NoToken         *bool
	ZeroRTT         *bool
	transportMu     sync.Mutex
	apiMu           sync.Mutex
	apiTransport    *http.Transport
	closeOnce       sync.Once
	closeErr        error
	closed          atomic.Bool
	ownsTransport   bool
}

type Config struct {
	EnableHeartbeat   *bool
	HeartbeatInterval *time.Duration
	CloseTimeout      *time.Duration
}

type ControlChannel interface {
	CreateTunnel(ctx context.Context, props TunnelProperties) (Tunnel, error)
	Close() error
	Done() <-chan error
	Err() error
	ServerDetails() *ServerDetails
}

type ClientDetails struct {
	Agent           *string
	Channel         *string
	Version         *string
	OS              *string
	Arch            *string
	Token           *string
	ProtocolVersion *string
}

type ServerDetails struct {
	Agent    *string
	Channel  *string
	Version  *string
	Plan     *string
	Provider *string
	Region   *string
	Update   *string
}

func (c *Client) getEngine() (*string, error) {
	if c == nil || c.closed.Load() {
		return nil, net.ErrClosed
	}
	engine := c.EngineURL
	if engine == nil {
		return nil, errors.New("engine URL is required")
	}
	return engine, nil
}

func (c *Client) getClientDetails(engine *string, token *string) (*ClientDetails, error) {
	if token != nil && *token != "" && tlsConfigHasClientCertificate(c.TLSClientConfig) {
		return nil, errors.New("token and mTLS authentication cannot be used together")
	}
	if engine == nil {
		if _, err := c.getEngine(); err != nil {
			return nil, err
		}
	}
	if token == nil {
		if c.Token != nil && tlsConfigHasClientCertificate(c.TLSClientConfig) {
			return nil, errors.New("token and mTLS authentication cannot be used together")
		}
		noToken := c.NoToken
		if noToken == nil {
			noToken = BoolPtr(false)
		}
		if !*noToken {
			if c.Token != nil {
				token = c.Token
			} else {
				return nil, errors.New("token is required but not configured")
			}
		}
	}
	return getClientDetails(token)
}

func (c *Client) getProxyClientDetails(engine *string, secret *string) (*ClientDetails, error) {
	if engine == nil {
		if _, err := c.getEngine(); err != nil {
			return nil, err
		}
	}
	return getClientDetails(secret)
}

func tlsConfigHasClientCertificate(cfg *tls.Config) bool {
	return cfg != nil && (len(cfg.Certificates) > 0 || cfg.GetClientCertificate != nil)
}

func toServerDetails(details *pb.ServerDetails) *ServerDetails {
	if details == nil {
		return nil
	}
	return &ServerDetails{
		Agent:    stringPtrFromPbValue(details.Agent),
		Channel:  stringPtrFromPbValue(details.Channel),
		Version:  stringPtrFromPbValue(details.Version),
		Plan:     stringPtrFromPbValue(details.Plan),
		Provider: stringPtrFromPbValue(details.Provider),
		Region:   stringPtrFromPbValue(details.Region),
		Update:   stringPtrFromPbValue(details.Update),
	}
}

func isNilDialer(d Dialer) bool {
	if d == nil {
		return true
	}
	v := reflect.ValueOf(d)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

func (c *Client) dialEngine(ctx context.Context, engine *string, nextProtos *[]string) (net.Conn, error) {
	return c.dialEngineWithTransport(ctx, engine, nextProtos, nil)
}

func (c *Client) DialEngineHTTP1(ctx context.Context, addr string) (net.Conn, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("engine address is required")
	}
	nextProtos := []string{"http/1.1"}
	return c.dialEngineWithTransport(ctx, &addr, &nextProtos, c.apiDialer())
}

func (c *Client) dialEngineWithTransport(ctx context.Context, engine *string, nextProtos *[]string, override Dialer) (net.Conn, error) {
	return c.dialEngineWithTransportConfig(ctx, engine, nextProtos, override, c.TLSClientConfig)
}

func (c *Client) dialEngineWithTransportConfig(ctx context.Context, engine *string, nextProtos *[]string, override Dialer, clientTLSConfig *tls.Config) (net.Conn, error) {
	if c == nil || c.closed.Load() {
		return nil, net.ErrClosed
	}
	var err error
	if engine == nil {
		engine, err = c.getEngine()
		if err != nil {
			return nil, err
		}
	}
	transport := override
	if isNilDialer(transport) {
		transport = c.defaultTunnelTransport()
	}
	tlsCfg := clientTLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{}
	} else {
		tlsCfg = tlsCfg.Clone()
	}
	if tlsCfg.ServerName == "" {
		host, _, err := splitHostPort(*engine)
		if err != nil || host == nil {
			return nil, errors.New("failed to extract host from address")
		}
		tlsCfg.ServerName = *host
	}
	if tlsCfg.NextProtos == nil {
		if nextProtos != nil {
			tlsCfg.NextProtos = *nextProtos
		} else {
			tlsCfg.NextProtos = []string{"rstrm/1"}
		}
	}
	return dialWithECH(ctx, transport, *engine, tlsCfg)
}

// Close releases API pools and transports owned by the client. Transports supplied
// through ClientOptions remain caller-owned unless OwnTransport is true. It is safe
// to call concurrently and repeatedly. A closed client cannot be reused.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.CloseIdleConnections()
		c.transportMu.Lock()
		transport := c.Transport
		owned := c.ownsTransport
		c.transportMu.Unlock()
		if closer, ok := transport.(io.Closer); owned && ok {
			c.closeErr = closer.Close()
		}
	})
	return c.closeErr
}

func (c *Client) defaultTunnelTransport() Dialer {
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	if c.closed.Load() {
		return closedClientDialer{}
	}
	if isNilDialer(c.Transport) {
		c.Transport = &AutoTransport{}
		c.ownsTransport = true
	}
	return c.Transport
}

type closedClientDialer struct{}

func (closedClientDialer) Dial(context.Context, string, *tls.Config) (net.Conn, error) {
	return nil, net.ErrClosed
}

func selectedTunnelTransport(transport Dialer) Dialer {
	if auto, ok := transport.(*AutoTransport); ok && auto != nil {
		return auto.SelectedTransport()
	}
	return transport
}

func datagramTransport(transport Dialer) (datagramChannelRegistry, DatagramProvider, bool) {
	selected := selectedTunnelTransport(transport)
	registry, registryOK := selected.(datagramChannelRegistry)
	provider, providerOK := selected.(DatagramProvider)
	return registry, provider, registryOK && providerOK
}

type dialType string

const (
	dialTypeProxyReq                  dialType = "proxy_req"
	dialTypeStreamReq                 dialType = "stream_req"
	proxyConnectionDeliveryTimeout             = 5 * time.Second
	proxyConnectionDialAttemptTimeout          = 10 * time.Second
	proxyConnectionDialAttempts                = 2
	proxyConnectionWorkerLimit                 = 64
	proxyConnectionQueueLimit                  = 256
	proxyConnectionResponseQueueLimit          = 256
	defaultCloseTimeout                        = 5 * time.Second
)

func (c *Client) dial(ctx context.Context, dialType dialType, raddr Addr, token *string) (net.Conn, error) {
	return c.dialEndpoint(ctx, dialType, nil, raddr, token, nil)
}

func (c *Client) dialEndpoint(ctx context.Context, dialType dialType, endpoint *string, raddr Addr, token *string, transport Dialer) (net.Conn, error) {
	engine := endpoint
	var err error
	if engine == nil {
		engine, err = c.getEngine()
	}
	if err != nil {
		return nil, err
	}
	clientTLSConfig := c.TLSClientConfig
	if endpoint != nil && clientTLSConfig != nil {
		clientTLSConfig = clientTLSConfig.Clone()
		clientTLSConfig.ServerName = ""
	}
	conn, err := c.dialEngineWithTransportConfig(ctx, engine, nil, transport, clientTLSConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to dial engine: %w", err)
	}
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	zeroRTT := c.ZeroRTT
	if zeroRTT == nil {
		zeroRTT = BoolPtr(true) // default to true
	}
	var clientDetails *ClientDetails
	var cause error
	if dialType == dialTypeProxyReq {
		clientDetails, cause = c.getProxyClientDetails(engine, token)
	} else {
		clientDetails, cause = c.getClientDetails(engine, token)
	}
	if cause != nil {
		err = fmt.Errorf("failed to get client details: %w", cause)
	}
	if err == nil {
		if dialType == dialTypeProxyReq {
			msg := &pb.Message{
				Payload: &pb.Message_ProxyReq{
					ProxyReq: &pb.ProxyReq{
						ClientDetails: toClientDetailsPb(clientDetails),
						StreamId:      raddr.IdOrName,
						ZeroRtt:       boolPbValueOrNil(zeroRTT),
					},
				},
			}
			if cause := writePbMessage(w, msg); cause != nil {
				err = fmt.Errorf("failed to send ProxyReq: %w", cause)
			}
		} else {
			msg := &pb.Message{
				Payload: &pb.Message_StreamReq{
					StreamReq: &pb.StreamReq{
						ClientDetails: toClientDetailsPb(clientDetails),
						TunnelIdName:  raddr.IdOrName,
						ZeroRtt:       boolPbValueOrNil(zeroRTT),
					},
				},
			}
			if cause := writePbMessage(w, msg); cause != nil {
				err = fmt.Errorf("failed to send StreamReq: %w", cause)
			}
		}
	}
	if err == nil && !*zeroRTT {
		resp, cause := readPbMessage(r)
		if cause != nil {
			err = fmt.Errorf("failed to read response: %w", cause)
		} else {
			if dialType == dialTypeProxyReq {
				proxyRsp, ok := resp.Payload.(*pb.Message_ProxyRsp)
				if !ok {
					err = fmt.Errorf("server did not return a ProxyRsp")
				} else {
					if proxyRsp.ProxyRsp.Error != nil {
						err = newEngineError(proxyRsp.ProxyRsp.Error)
					}
				}
			} else {
				streamRsp, ok := resp.Payload.(*pb.Message_StreamRsp)
				if !ok {
					err = fmt.Errorf("server did not return a StreamRsp")
				} else {
					switch rspPayload := streamRsp.StreamRsp.Payload.(type) {
					case *pb.StreamRsp_Error:
						err = newEngineError(rspPayload.Error)
					case *pb.StreamRsp_StreamId:
					default:
						err = fmt.Errorf("unexpected StreamRsp payload")
					}
				}
			}
		}
	}
	if err != nil {
		conn.Close()
		conn = nil
	}
	return conn, err
}

func (c *Client) Dial(ctx context.Context, raddr Addr) (net.Conn, error) {
	return c.dial(ctx, dialTypeStreamReq, raddr, nil)
}

func (c *Client) PacketDial(ctx context.Context, raddr Addr) (net.PacketConn, error) {
	if conn, ok, err := c.packetDialDatagramChannel(ctx, raddr); ok || err != nil {
		return conn, err
	}
	conn, err := c.Dial(ctx, raddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial stream: %w", err)
	}
	return PacketConnFromConn(conn, &raddr, PacketModeFramed), nil
}

func tunnelAllowsQUICDatagrams(props TunnelProperties) bool {
	return props.DatagramGuaranteedDelivery == nil || !*props.DatagramGuaranteedDelivery
}

func (c *Client) packetDialDatagramChannel(ctx context.Context, raddr Addr) (net.PacketConn, bool, error) {
	registry, provider, supported := datagramTransport(c.defaultTunnelTransport())
	auto, isAuto := c.Transport.(*AutoTransport)
	if !supported && (!isAuto || auto.SelectedTransport() != nil) {
		return nil, false, nil
	}
	engine, err := c.getEngine()
	if err != nil {
		return nil, true, err
	}
	conn, err := c.dialEngine(ctx, engine, nil)
	if err != nil {
		return nil, true, fmt.Errorf("failed to dial engine: %w", err)
	}
	if !supported {
		registry, provider, supported = datagramTransport(c.Transport)
		if !supported {
			_ = conn.Close()
			return nil, false, nil
		}
	}
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	clientDetails, err := c.getClientDetails(engine, nil)
	if err != nil {
		_ = conn.Close()
		return nil, true, fmt.Errorf("failed to get client details: %w", err)
	}
	msg := &pb.Message{Payload: &pb.Message_StreamReq{StreamReq: &pb.StreamReq{ClientDetails: toClientDetailsPb(clientDetails), TunnelIdName: raddr.IdOrName, ZeroRtt: boolPbValueOrNil(BoolPtr(false)), DatagramChannel: boolPbValueOrNil(BoolPtr(true))}}}
	if err := writePbMessage(w, msg); err != nil {
		_ = conn.Close()
		return nil, true, fmt.Errorf("failed to send StreamReq: %w", err)
	}
	resp, err := readPbMessage(r)
	if err != nil {
		_ = conn.Close()
		return nil, true, fmt.Errorf("failed to read response: %w", err)
	}
	streamRsp, ok := resp.Payload.(*pb.Message_StreamRsp)
	if !ok {
		_ = conn.Close()
		return nil, true, fmt.Errorf("server did not return a StreamRsp")
	}
	if streamRsp.StreamRsp == nil {
		_ = conn.Close()
		return nil, true, fmt.Errorf("server returned empty StreamRsp")
	}
	payload, ok := streamRsp.StreamRsp.Payload.(*pb.StreamRsp_StreamId)
	if !ok {
		_ = conn.Close()
		return nil, false, nil
	}
	streamID := payload.StreamId
	channelID, err := datagramChannelIDFromStreamID(streamID)
	if err != nil {
		_ = conn.Close()
		return nil, true, err
	}
	chCtx, chCancel := context.WithCancel(context.WithoutCancel(ctx))
	laddr := &Addr{IdOrName: streamID}
	ch := &quicDatagramChannel{channelID: channelID, provider: provider, laddr: laddr, raddr: &raddr, recvCh: make(chan []byte, 64), ctx: chCtx, cancel: chCancel, readDeadline: newPacketDeadline(), writeDeadline: newPacketDeadline()}
	ch.onClose = func(ch *quicDatagramChannel) {
		registry.unregisterDatagramChannel(channelID, ch)
		_ = conn.Close()
	}
	if !registry.registerDatagramChannel(channelID, ch) {
		_ = conn.Close()
		ch.Close()
		return nil, true, fmt.Errorf("datagram channel ID collision")
	}
	ch.watchDone = make(chan struct{})
	go watchDatagramChannelMessages(r, streamID, ch)
	return ch, true, nil
}

func watchDatagramChannelMessages(r *bufio.Reader, streamID string, ch *quicDatagramChannel) {
	defer close(ch.watchDone)
	for {
		msg, err := readPbMessage(r)
		if err != nil {
			ch.initiateClose()
			return
		}
		closeMsg := msg.GetDatagramChannelClose()
		if closeMsg == nil {
			continue
		}
		if closeMsg.StreamId != streamID {
			continue
		}
		ch.initiateClose()
		return
	}
}

type pendingOpenTunnelReq struct {
	respCh    chan *pb.OpenTunnelRsp
	readyCh   chan struct{}
	readyOnce sync.Once
}

type contextMutex struct {
	once sync.Once
	ch   chan struct{}
}

type controlChannelWriteError struct {
	err error
}

func (e *controlChannelWriteError) Error() string {
	return e.err.Error()
}

func (e *controlChannelWriteError) Unwrap() error {
	return e.err
}

func (m *contextMutex) Lock(ctx context.Context) error {
	m.once.Do(func() {
		m.ch = make(chan struct{}, 1)
		m.ch <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-m.ch:
		return nil
	}
}

func (m *contextMutex) Unlock() {
	m.ch <- struct{}{}
}

func (p *pendingOpenTunnelReq) closeReady() {
	p.readyOnce.Do(func() { close(p.readyCh) })
}

type controlChannelImpl struct {
	logger             *slog.Logger
	client             *Client
	clientID           string
	enableHeartbeat    bool
	heartbeatInterval  time.Duration
	conn               net.Conn
	w                  *bufio.Writer
	r                  *bufio.Reader
	serverDetails      *ServerDetails
	doneCh             chan error
	closedCh           chan struct{}
	pendingTunnels     map[string]*pendingOpenTunnelReq
	tunnels            map[string]*bytestreamTunnelImpl
	proxyTransports    map[string]Dialer
	proxyRequests      chan proxyConnectionRequest
	proxyResponses     chan proxyConnectionResponse
	proxyWorkerLimit   int
	proxyQueueLimit    int
	proxyResponseLimit int
	closing            bool
	closed             bool
	mu                 sync.Mutex
	writeLock          contextMutex
	err                error
	// QUIC datagram support — non-nil when the transport implements DatagramProvider.
	datagramProvider DatagramProvider
	datagramTunnels  map[string]*quicDatagramListener           // tunnelID -> listener
	datagramChannels map[datagramChannelID]*quicDatagramChannel // channelID -> active channel
	lifecycleCtx     context.Context
	lifecycleCancel  context.CancelFunc
	closeTimeout     time.Duration
	lifecycleWG      sync.WaitGroup
}

type proxyConnectionRequest struct {
	req              *pb.ProxyConnReq
	tunnel           *bytestreamTunnelImpl
	ctx              context.Context
	endpoint         *string
	secret           *string
	transport        Dialer
	datagramListener *quicDatagramListener
	datagramDirect   bool
}

type proxyConnectionResponse struct {
	streamID string
	err      error
	tunnel   *bytestreamTunnelImpl
}

type controlChannelCleanup struct {
	err              error
	pendingTunnels   []*pendingOpenTunnelReq
	tunnels          []*bytestreamTunnelImpl
	datagramTunnels  []*quicDatagramListener
	datagramChannels []*quicDatagramChannel
	proxyTransports  []Dialer
	lifecycleCancel  context.CancelFunc
	conn             net.Conn
	doneCh           chan error
	closedCh         chan struct{}
	lifecycleWG      *sync.WaitGroup
}

func (c *Client) Connect(ctx context.Context, cfg *Config) (ControlChannel, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, err
	}
	conn, err := c.dialEngine(ctx, engine, nil)
	var ch *controlChannelImpl = nil
	if err != nil {
		return nil, fmt.Errorf("failed to dial engine: %w", err)
	}
	ClientDetails, cause := c.getClientDetails(engine, nil)
	if cause != nil {
		err = fmt.Errorf("failed to get client details: %w", cause)
	} else {
		w := bufio.NewWriter(conn)
		r := bufio.NewReader(conn)
		if cfg == nil {
			cfg = &Config{} // default config
		}
		enableHeartbeat := true
		if cfg.EnableHeartbeat != nil {
			enableHeartbeat = *cfg.EnableHeartbeat
		}
		heartbeatInterval := time.Second * 5
		if cfg.HeartbeatInterval != nil {
			heartbeatInterval = *cfg.HeartbeatInterval
		}
		closeTimeout := defaultCloseTimeout
		if cfg.CloseTimeout != nil && *cfg.CloseTimeout > 0 {
			closeTimeout = *cfg.CloseTimeout
		}
		ch = &controlChannelImpl{
			logger:            slog.With("component", "control-channel"),
			client:            c,
			enableHeartbeat:   enableHeartbeat,
			heartbeatInterval: heartbeatInterval,
			closeTimeout:      closeTimeout,
			conn:              conn,
			w:                 w,
			r:                 r,
			doneCh:            make(chan error, 1),
			closedCh:          make(chan struct{}),
			pendingTunnels:    make(map[string]*pendingOpenTunnelReq),
			tunnels:           make(map[string]*bytestreamTunnelImpl),
			proxyTransports:   make(map[string]Dialer),
		}
		msg := &pb.Message{
			Payload: &pb.Message_OpenControlChannelReq{
				OpenControlChannelReq: &pb.OpenControlChannelReq{
					ClientDetails: toClientDetailsPb(ClientDetails),
				},
			},
		}
		if cause := writePbMessage(ch.w, msg); cause != nil {
			err = fmt.Errorf("failed to send OpenControlChannelReq: %w", cause)
		}
	}
	if err == nil {
		resp, cause := readPbMessage(ch.r)
		if cause != nil {
			err = fmt.Errorf("failed to read response: %w", cause)
		} else {
			openControlChannelRsp, ok := resp.Payload.(*pb.Message_OpenControlChannelRsp)
			if !ok {
				err = fmt.Errorf("server did not return a OpenControlChannelRsp")
			} else {
				switch rspPayload := openControlChannelRsp.OpenControlChannelRsp.Payload.(type) {
				case *pb.OpenControlChannelRsp_Error:
					err = newEngineError(rspPayload.Error)
				case *pb.OpenControlChannelRsp_Ok_:
					if rspPayload.Ok == nil {
						err = errors.New("server returned empty OpenControlChannelRsp payload")
					} else {
						ch.clientID = rspPayload.Ok.ClientId
						ch.serverDetails = toServerDetails(rspPayload.Ok.ServerDetails)
					}
				default:
					err = fmt.Errorf("unexpected OpenControlChannelRsp payload")
				}
			}
		}
	}
	if err == nil {
		ch.lifecycleCtx, ch.lifecycleCancel = context.WithCancel(context.Background())
		// Enable QUIC datagram mode if the transport supports it.
		if _, dp, ok := datagramTransport(c.Transport); ok {
			ch.datagramProvider = dp
			ch.datagramTunnels = make(map[string]*quicDatagramListener)
			ch.datagramChannels = make(map[datagramChannelID]*quicDatagramChannel)
		}
		loopCount := 1
		startDatagramLoop := ch.datagramProvider != nil
		if _, ok := ch.datagramProvider.(datagramChannelRegistry); ok {
			startDatagramLoop = false
		}
		if startDatagramLoop {
			loopCount++
		}
		if ch.enableHeartbeat && ch.heartbeatInterval > 0 {
			loopCount++
		}
		ch.lifecycleWG.Add(loopCount)
		go ch.runLifecycleLoop(ch.readLoop)
		if startDatagramLoop {
			go ch.runLifecycleLoop(ch.datagramReadLoop)
		}
		if ch.enableHeartbeat && ch.heartbeatInterval > 0 {
			go ch.runLifecycleLoop(ch.heartbeatLoop)
		}
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ch, nil
}

func (c *controlChannelImpl) runLifecycleLoop(loop func()) {
	defer c.lifecycleWG.Done()
	loop()
}

func (c *controlChannelImpl) CreateTunnel(ctx context.Context, props TunnelProperties) (Tunnel, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("control channel is closed")
	}
	if c.closing {
		c.mu.Unlock()
		return nil, errors.New("control channel is closing")
	}
	var err error
	props, err = normalizeCreateTunnelProperties(props)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	requestID := uuid.New().String()
	pending := &pendingOpenTunnelReq{
		respCh:  make(chan *pb.OpenTunnelRsp, 1),
		readyCh: make(chan struct{}),
	}
	c.pendingTunnels[requestID] = pending
	c.mu.Unlock()
	msg := &pb.Message{
		Payload: &pb.Message_OpenTunnelReq{
			OpenTunnelReq: &pb.OpenTunnelReq{
				RequestId:        requestID,
				TunnelProperties: toTunnelPropertiesPb(props),
			},
		},
	}
	if err := c.writePbMessageContext(ctx, msg); err != nil {
		c.mu.Lock()
		if c.pendingTunnels[requestID] == pending {
			delete(c.pendingTunnels, requestID)
		}
		pending.closeReady()
		c.mu.Unlock()
		failedErr := fmt.Errorf("failed to send OpenTunnelReq: %w", err)
		var writeErr *controlChannelWriteError
		if errors.As(err, &writeErr) {
			c.onError(failedErr)
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, failedErr
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pendingTunnels, requestID)
		pending.closeReady()
		c.mu.Unlock()
		return nil, ctx.Err()
	case openTunnelRsp, ok := <-pending.respCh:
		defer pending.closeReady()
		if !ok {
			return nil, errors.New("control channel closed unexpectedly")
		}
		switch payload := openTunnelRsp.Payload.(type) {
		case *pb.OpenTunnelRsp_Error:
			return nil, newEngineError(payload.Error)
		case *pb.OpenTunnelRsp_TunnelProperties:
			rProps := toTunnelProperties(payload.TunnelProperties)
			if rProps.ID == nil {
				return nil, errors.New("engine did not return a tunnel ID")
			}
			tunnelID := *rProps.ID
			tunnelCtx, tunnelCancel := context.WithCancel(c.lifecycleCtx)
			tunnel := &bytestreamTunnelImpl{
				props:    rProps,
				ctrl:     c,
				tunnelID: tunnelID,
				closedCh: make(chan struct{}),
				conns:    make(chan net.Conn, 10),
				ctx:      tunnelCtx,
				cancel:   tunnelCancel,
			}
			var listener *quicDatagramListener
			isDatagram := rProps.Type != nil && *rProps.Type == TunnelTypeDatagram
			if isDatagram && c.datagramProvider != nil && tunnelAllowsQUICDatagrams(rProps) {
				dlCtx, dlCancel := context.WithCancel(tunnelCtx)
				listener = &quicDatagramListener{
					conns:  make(chan net.PacketConn, 10),
					ctx:    dlCtx,
					cancel: dlCancel,
					laddr:  &Addr{IdOrName: tunnelID},
				}
			}
			c.mu.Lock()
			if c.closed || c.closing {
				c.mu.Unlock()
				tunnelCancel()
				if listener != nil {
					_ = listener.Close()
				}
				return nil, errors.New("control channel closed while opening tunnel")
			}
			c.tunnels[tunnelID] = tunnel
			if listener != nil {
				c.datagramTunnels[tunnelID] = listener
			}
			c.mu.Unlock()
			if listener != nil {
				return &datagramTunnelImpl{inner: tunnel, pl: listener}, nil
			}
			if isDatagram {
				return &datagramTunnelImpl{inner: tunnel, pl: PacketListenerFromListener(tunnel)}, nil
			}
			return tunnel, nil
		default:
			return nil, errors.New("unexpected OpenTunnelRsp payload")
		}
	}
}

func (c *controlChannelImpl) Close() error {
	c.mu.Lock()
	if c.closed {
		closedCh := c.ensureClosedSignalLocked()
		c.mu.Unlock()
		<-closedCh
		return c.Err()
	}
	closedCh := c.ensureClosedSignalLocked()
	sendClose := !c.closing
	if !c.closing {
		c.closing = true
	}
	c.mu.Unlock()
	ctx, cancel := c.newCloseContext()
	defer cancel()
	if sendClose {
		msg := &pb.Message{Payload: &pb.Message_CloseControlChannelReq{CloseControlChannelReq: &pb.CloseControlChannelReq{}}}
		if err := c.writePbMessageContext(ctx, msg); err != nil {
			closeErr := fmt.Errorf("failed to send CloseControlChannelReq: %w", err)
			c.onError(closeErr)
			<-closedCh
			return c.Err()
		}
	}
	select {
	case <-closedCh:
		return c.Err()
	case <-ctx.Done():
		select {
		case <-closedCh:
			return c.Err()
		default:
		}
		closeErr := fmt.Errorf("timed out waiting for control channel to close: %w", context.Cause(ctx))
		c.onError(closeErr)
		<-closedCh
		return c.Err()
	}
}

func (c *controlChannelImpl) Done() <-chan error {
	return c.doneCh
}

func (c *controlChannelImpl) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *controlChannelImpl) ServerDetails() *ServerDetails {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.serverDetails == nil {
		return nil
	}
	return &ServerDetails{
		Agent:    clonePtr(c.serverDetails.Agent),
		Channel:  clonePtr(c.serverDetails.Channel),
		Version:  clonePtr(c.serverDetails.Version),
		Plan:     clonePtr(c.serverDetails.Plan),
		Provider: clonePtr(c.serverDetails.Provider),
		Region:   clonePtr(c.serverDetails.Region),
		Update:   clonePtr(c.serverDetails.Update),
	}
}

func (c *controlChannelImpl) readLoop() {
	var err error = nil
	c.mu.Lock()
	for {
		c.mu.Unlock()
		msg, cause := readPbMessage(c.r)
		c.mu.Lock()
		if cause != nil {
			err = fmt.Errorf("failed to read message: %w", cause)
		}
		if c.closed || err != nil {
			break
		} else {
			close, waitCh, cleanup, cause := c.handleMessage(msg)
			if cause != nil {
				err = fmt.Errorf("failed to handle message: %w", cause)
			}
			if cleanup != nil {
				c.mu.Unlock()
				cleanup()
				c.mu.Lock()
			}
			if close || err != nil {
				break
			}
			if waitCh != nil {
				c.mu.Unlock()
				select {
				case <-waitCh:
				case <-c.closedCh:
				}
				c.mu.Lock()
				if c.closed {
					break
				}
			}
		}
	}
	c.mu.Unlock()
	c.onError(err)
}

func (c *controlChannelImpl) heartbeatLoop() {
	t := time.NewTicker(c.heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-c.closedCh:
			return
		case <-t.C:
			msg := &pb.Message{Payload: &pb.Message_Heartbeat{}}
			if err := c.writePbMessage(msg); err != nil {
				c.onError(fmt.Errorf("failed to send heartbeat: %w", err))
				return
			}
		}
	}
}

func (c *controlChannelImpl) handleMessage(msg *pb.Message) (bool, <-chan struct{}, func(), error) {
	if c.closed {
		return true, nil, nil, nil
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_OpenTunnelRsp:
		return false, c.handleOpenTunnelRsp(payload.OpenTunnelRsp), nil, nil
	case *pb.Message_CloseTunnelRsp:
		return false, nil, c.handleCloseTunnelRsp(payload.CloseTunnelRsp), nil
	case *pb.Message_ProxyConnReq:
		return false, nil, nil, c.handleProxyConnReq(payload.ProxyConnReq)
	case *pb.Message_DatagramChannelClose:
		return false, nil, c.handleDatagramChannelClose(payload.DatagramChannelClose), nil
	case *pb.Message_CloseControlChannelRsp:
		return true, nil, nil, nil
	default:
		return false, nil, nil, nil
	}
}

func (c *controlChannelImpl) handleDatagramChannelClose(msg *pb.DatagramChannelClose) func() {
	if msg == nil {
		return nil
	}
	channelID, err := datagramChannelIDFromStreamID(msg.StreamId)
	if err != nil {
		c.logger.Warn("invalid datagram channel close", "stream_id", msg.StreamId, "error", err)
		return nil
	}
	ch := c.datagramChannels[channelID]
	if ch == nil {
		return nil
	}
	delete(c.datagramChannels, channelID)
	return func() { _ = ch.Close() }
}

func (c *controlChannelImpl) handleOpenTunnelRsp(rsp *pb.OpenTunnelRsp) <-chan struct{} {
	requestId := rsp.RequestId
	pending, found := c.pendingTunnels[requestId]
	if found {
		delete(c.pendingTunnels, requestId)
		pending.respCh <- rsp
		return pending.readyCh
	} else {
		c.logger.Warn("unexpected OpenTunnelRsp", "request_id", requestId)
		return nil
	}
}

func (c *controlChannelImpl) handleCloseTunnelRsp(rsp *pb.CloseTunnelRsp) func() {
	tunnelId := rsp.TunnelId
	tunnel, found := c.tunnels[tunnelId]
	if found {
		delete(c.tunnels, tunnelId)
		tunnelCleanup := tunnel.onCloseLocked()
		listener := c.datagramTunnels[tunnelId]
		if listener != nil {
			delete(c.datagramTunnels, tunnelId)
		}
		channels := c.detachDatagramChannelsForTunnelLocked(tunnelId)
		return func() {
			tunnelCleanup.run()
			if listener != nil {
				_ = listener.Close()
			}
			for _, channel := range channels {
				_ = channel.Close()
			}
		}
	} else {
		c.logger.Warn("unexpected CloseTunnelRsp", "tunnel_id", tunnelId)
	}
	return nil
}

func (c *controlChannelImpl) handleProxyConnReq(req *pb.ProxyConnReq) error {
	if req == nil {
		return errors.New("received empty ProxyConnReq")
	}
	if strings.TrimSpace(req.StreamId) == "" {
		return errors.New("received ProxyConnReq without a stream ID")
	}
	if err := c.startProxyRuntimeLocked(); err != nil {
		return err
	}
	tunnelId := req.TunnelId
	tunnel, found := c.tunnels[tunnelId]
	if !found {
		c.logger.Warn("unexpected ProxyConnReq", "tunnel_id", tunnelId, "stream_id", req.StreamId)
		return c.rejectProxyConnReqLocked(req.StreamId, nil, errors.New("proxy connection references an unknown tunnel"))
	}
	proxyCtx := tunnel.ctx
	if proxyCtx == nil {
		err := errors.New("tunnel lifecycle context is not initialized")
		c.logger.Error("rejected proxy connection request", "tunnel_id", tunnelId, "stream_id", req.StreamId, "error", err)
		return c.rejectProxyConnReqLocked(req.StreamId, tunnel, err)
	}
	proxyEndpoint := stringPtrFromPbValue(req.ProxyEndpoint)
	proxySecret := stringPtrFromPbValue(req.Secret)
	if proxyEndpoint != nil && (strings.TrimSpace(*proxyEndpoint) == "" || proxySecret == nil || strings.TrimSpace(*proxySecret) == "") {
		return c.rejectProxyConnReqLocked(req.StreamId, tunnel, errors.New("redirected proxy connection requires a non-empty endpoint and stream credential"))
	}
	task := proxyConnectionRequest{
		req:              req,
		tunnel:           tunnel,
		ctx:              proxyCtx,
		endpoint:         proxyEndpoint,
		secret:           proxySecret,
		transport:        c.proxyTransportLocked(proxyEndpoint),
		datagramListener: c.datagramTunnels[tunnelId],
		datagramDirect:   proxyEndpoint == nil && c.datagramProvider != nil && tunnel.props.Type != nil && *tunnel.props.Type == TunnelTypeDatagram && tunnelAllowsQUICDatagrams(tunnel.props),
	}
	select {
	case c.proxyRequests <- task:
		tunnel.addProxyResponseLocked()
		return nil
	default:
		return c.rejectProxyConnReqLocked(req.StreamId, tunnel, errors.New("proxy connection request queue is full"))
	}
}

func (c *controlChannelImpl) startProxyRuntimeLocked() error {
	if c.proxyRequests != nil {
		return nil
	}
	if c.lifecycleCtx == nil {
		return errors.New("control channel lifecycle context is not initialized")
	}
	workerLimit := c.proxyWorkerLimit
	if workerLimit <= 0 {
		workerLimit = proxyConnectionWorkerLimit
	}
	queueLimit := c.proxyQueueLimit
	if queueLimit <= 0 {
		queueLimit = proxyConnectionQueueLimit
	}
	c.proxyRequests = make(chan proxyConnectionRequest, queueLimit)
	responseLimit := c.proxyResponseLimit
	if responseLimit <= 0 {
		responseLimit = proxyConnectionResponseQueueLimit
	}
	c.proxyResponses = make(chan proxyConnectionResponse, responseLimit)
	c.lifecycleWG.Add(workerLimit + 1)
	for range workerLimit {
		go c.runLifecycleLoop(c.proxyConnectionWorkerLoop)
	}
	go c.runLifecycleLoop(c.proxyConnectionResponseLoop)
	return nil
}

func (c *controlChannelImpl) rejectProxyConnReqLocked(streamID string, tunnel *bytestreamTunnelImpl, responseErr error) error {
	select {
	case c.proxyResponses <- proxyConnectionResponse{streamID: streamID, err: responseErr, tunnel: tunnel}:
		if tunnel != nil {
			tunnel.addProxyResponseLocked()
		}
		return nil
	default:
		return errors.New("proxy connection response queue is full")
	}
}

func (c *controlChannelImpl) proxyConnectionWorkerLoop() {
	for {
		if c.lifecycleCtx.Err() != nil {
			return
		}
		select {
		case <-c.lifecycleCtx.Done():
			return
		case task := <-c.proxyRequests:
			if c.lifecycleCtx.Err() != nil {
				return
			}
			responseErr := c.processProxyConnectionRequest(task)
			if !c.queueProxyConnRsp(task.req.StreamId, task.tunnel, responseErr) {
				return
			}
		}
	}
}

func (c *controlChannelImpl) processProxyConnectionRequest(task proxyConnectionRequest) error {
	if task.datagramDirect {
		if !c.handleDatagramProxyConnReq(task.req, task.tunnel) {
			return errors.New("failed to establish datagram proxy connection")
		}
		return nil
	}
	laddr := Addr{IdOrName: task.req.StreamId, SourceIP: NetIPFromPbValue(task.req.SourceIp)}
	raddr := Addr{IdOrName: task.req.TunnelId}
	conn, err := c.dialProxyConnection(task.ctx, task.endpoint, laddr, task.secret, task.transport)
	if err != nil {
		c.logger.Error("failed to dial proxy connection", "error", err)
		return err
	}
	proxyConn := &bytestreamConn{conn: conn, laddr: laddr, raddr: raddr}
	if task.datagramListener != nil {
		packetConn := PacketConnFromConn(proxyConn, &laddr, PacketModeFramed)
		err = c.deliverDatagramProxyConnection(task.ctx, task.tunnel, task.datagramListener, packetConn)
	} else {
		err = c.deliverProxyConnection(task.ctx, task.tunnel, proxyConn)
	}
	if err != nil {
		_ = conn.Close()
	}
	return err
}

func (c *controlChannelImpl) queueProxyConnRsp(streamID string, tunnel *bytestreamTunnelImpl, responseErr error) bool {
	select {
	case c.proxyResponses <- proxyConnectionResponse{streamID: streamID, err: responseErr, tunnel: tunnel}:
		return true
	case <-c.lifecycleCtx.Done():
		return false
	}
}

func (c *controlChannelImpl) proxyConnectionResponseLoop() {
	for {
		if c.lifecycleCtx.Err() != nil {
			return
		}
		select {
		case <-c.lifecycleCtx.Done():
			return
		case response := <-c.proxyResponses:
			if c.lifecycleCtx.Err() != nil {
				return
			}
			if err := c.writeProxyConnRsp(response.streamID, response.err); err != nil {
				c.onError(fmt.Errorf("failed to send ProxyConnRsp: %w", err))
				return
			}
			c.mu.Lock()
			if response.tunnel != nil {
				response.tunnel.completeProxyResponseLocked()
			}
			c.mu.Unlock()
		}
	}
}

func (c *controlChannelImpl) dialProxyConnection(ctx context.Context, endpoint *string, laddr Addr, secret *string, transport Dialer) (net.Conn, error) {
	return c.dialProxyConnectionWithPolicy(ctx, endpoint, laddr, secret, transport, proxyConnectionDialAttemptTimeout, proxyConnectionDialAttempts)
}

func (c *controlChannelImpl) dialProxyConnectionWithPolicy(ctx context.Context, endpoint *string, laddr Addr, secret *string, transport Dialer, attemptTimeout time.Duration, attempts int) (net.Conn, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		conn, err := c.client.dialEndpoint(attemptCtx, dialTypeProxyReq, endpoint, laddr, secret, transport)
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == attempts || !isRetryableProxyDialError(err) {
			break
		}
		if c.logger != nil {
			c.logger.Warn("retrying proxy connection dial", "stream_id", laddr.IdOrName, "attempt", attempt+1, "error", err)
		}
	}
	return nil, lastErr
}

func isRetryableProxyDialError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func (c *controlChannelImpl) deliverDatagramProxyConnection(ctx context.Context, tunnel *bytestreamTunnelImpl, listener *quicDatagramListener, conn net.PacketConn) error {
	c.mu.Lock()
	if tunnel.closing || tunnel.closed {
		c.mu.Unlock()
		return errors.New("tunnel is closing or closed")
	}
	closedCh := tunnel.ensureClosedSignalLocked()
	c.mu.Unlock()
	timer := time.NewTimer(proxyConnectionDeliveryTimeout)
	defer timer.Stop()
	select {
	case listener.conns <- conn:
		c.mu.Lock()
		closed := tunnel.closed
		c.mu.Unlock()
		if closed || listener.ctx.Err() != nil {
			_ = conn.Close()
			return errors.New("tunnel is closing or closed")
		}
		return nil
	case <-ctx.Done():
		return errors.New("tunnel is closing or closed")
	case <-closedCh:
		return errors.New("tunnel is closing or closed")
	case <-listener.ctx.Done():
		return errors.New("datagram listener is closed")
	case <-timer.C:
		return errors.New("tunnel connection queue remained full")
	}
}

func (c *controlChannelImpl) deliverProxyConnection(ctx context.Context, tunnel *bytestreamTunnelImpl, conn net.Conn) error {
	c.mu.Lock()
	if tunnel.closing || tunnel.closed {
		c.mu.Unlock()
		return errors.New("tunnel is closing or closed")
	}
	closedCh := tunnel.ensureClosedSignalLocked()
	c.mu.Unlock()
	timer := time.NewTimer(proxyConnectionDeliveryTimeout)
	defer timer.Stop()
	select {
	case tunnel.conns <- conn:
		c.mu.Lock()
		closed := tunnel.closed
		c.mu.Unlock()
		if closed {
			_ = conn.Close()
			return errors.New("tunnel is closing or closed")
		}
		return nil
	case <-ctx.Done():
		return errors.New("tunnel is closing or closed")
	case <-closedCh:
		return errors.New("tunnel is closing or closed")
	case <-timer.C:
		return errors.New("tunnel connection queue remained full")
	}
}

func (c *controlChannelImpl) proxyTransportLocked(endpoint *string) Dialer {
	if endpoint == nil {
		return nil
	}
	key := strings.TrimSpace(*endpoint)
	if transport := c.proxyTransports[key]; transport != nil {
		return transport
	}
	selected := selectedTunnelTransport(c.client.defaultTunnelTransport())
	quicTransport, ok := selected.(*QUICTransport)
	if !ok || quicTransport == nil {
		return selected
	}
	if c.proxyTransports == nil {
		c.proxyTransports = make(map[string]Dialer)
	}
	transport := cloneQUICTransport(quicTransport)
	c.proxyTransports[key] = transport
	return transport
}

func (c *controlChannelImpl) sendProxyConnRsp(streamID string, responseErr error) {
	if err := c.writeProxyConnRsp(streamID, responseErr); err != nil {
		c.onError(fmt.Errorf("failed to send ProxyConnRsp: %w", err))
	}
}

func (c *controlChannelImpl) writeProxyConnRsp(streamID string, responseErr error) error {
	var response *pb.Error
	if responseErr != nil {
		response = &pb.Error{Code: pb.ErrorCode_ERROR_CODE_SERVICE_UNAVAILABLE, Message: wrapperspb.String(responseErr.Error())}
	}
	msg := &pb.Message{
		Payload: &pb.Message_ProxyConnRsp{
			ProxyConnRsp: &pb.ProxyConnRsp{
				StreamId: streamID,
				Error:    response,
			},
		},
	}
	return c.writePbMessage(msg)
}

// handleDatagramProxyConnReq creates a quicDatagramChannel for a datagram
// tunnel connection request and delivers it to the appropriate listener.
func (c *controlChannelImpl) handleDatagramProxyConnReq(req *pb.ProxyConnReq, tunnel *bytestreamTunnelImpl) bool {
	if tunnel.ctx == nil {
		c.logger.Error("rejected datagram proxy connection request", "tunnel_id", tunnel.tunnelID, "stream_id", req.StreamId, "error", "tunnel lifecycle context is not initialized")
		return false
	}
	channelID, err := datagramChannelIDFromStreamID(req.StreamId)
	if err != nil {
		c.logger.Warn("invalid datagram stream ID", "tunnelID", tunnel.tunnelID, "streamID", req.StreamId, "error", err)
		return false
	}
	laddr := &Addr{IdOrName: tunnel.tunnelID}
	raddr := &Addr{IdOrName: req.StreamId, SourceIP: NetIPFromPbValue(req.SourceIp)}
	registry, _ := c.datagramProvider.(datagramChannelRegistry)
	chCtx, chCancel := context.WithCancel(tunnel.ctx)
	ch := &quicDatagramChannel{
		channelID:     channelID,
		provider:      c.datagramProvider,
		laddr:         laddr,
		raddr:         raddr,
		recvCh:        make(chan []byte, 64),
		ctx:           chCtx,
		cancel:        chCancel,
		readDeadline:  newPacketDeadline(),
		writeDeadline: newPacketDeadline(),
		onClose: func(ch *quicDatagramChannel) {
			if registry != nil {
				registry.unregisterDatagramChannel(channelID, ch)
			}
			c.unregisterDatagramChannel(channelID, ch)
		},
	}
	c.mu.Lock()
	// Guard against a race with onError which sets datagramChannels to nil.
	if c.closed {
		c.mu.Unlock()
		ch.Close()
		return false
	}
	if existing := c.datagramChannels[channelID]; existing != nil {
		c.mu.Unlock()
		c.logger.Warn("datagram channel ID collision", "tunnelID", tunnel.tunnelID, "streamID", req.StreamId, "channelID", channelID)
		ch.Close()
		return false
	}
	c.datagramChannels[channelID] = ch
	listener := c.datagramTunnels[tunnel.tunnelID]
	c.mu.Unlock()
	if registry != nil && !registry.registerDatagramChannel(channelID, ch) {
		c.unregisterDatagramChannel(channelID, ch)
		c.logger.Warn("datagram channel ID collision", "tunnelID", tunnel.tunnelID, "streamID", req.StreamId, "channelID", channelID)
		ch.Close()
		return false
	}
	if listener != nil {
		select {
		case listener.conns <- ch:
			return true
		default:
			c.logger.Warn("datagram tunnel conns channel full", "tunnelID", tunnel.tunnelID)
			ch.Close()
		}
	} else {
		c.logger.Warn("no datagram listener for tunnel", "tunnelID", tunnel.tunnelID)
		ch.Close()
	}
	return false
}

// datagramReadLoop reads QUIC datagrams, strips the stream-derived channel ID
// prefix, and routes the payload to the appropriate quicDatagramChannel.
func (c *controlChannelImpl) datagramReadLoop() {
	for {
		data, err := c.datagramProvider.ReceiveDatagram(c.lifecycleCtx)
		if err != nil {
			if c.lifecycleCtx.Err() == nil {
				c.logger.Error("datagram read loop terminated unexpectedly", "error", err)
			}
			return
		}
		if len(data) < datagramChannelIDSize {
			continue
		}
		var channelID datagramChannelID
		copy(channelID[:], data[:datagramChannelIDSize])
		payload := data[datagramChannelIDSize:]
		c.mu.Lock()
		ch := c.datagramChannels[channelID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch.recvCh <- payload:
			default: // drop silently if the channel buffer is full
			}
		}
	}
}

func (c *controlChannelImpl) unregisterDatagramChannel(channelID datagramChannelID, ch *quicDatagramChannel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.datagramChannels == nil {
		return
	}
	if c.datagramChannels[channelID] == ch {
		delete(c.datagramChannels, channelID)
	}
}

func (c *controlChannelImpl) detachDatagramChannelsForTunnelLocked(tunnelID string) []*quicDatagramChannel {
	var channels []*quicDatagramChannel
	for channelID, ch := range c.datagramChannels {
		if ch.laddr != nil && ch.laddr.String() == tunnelID {
			delete(c.datagramChannels, channelID)
			channels = append(channels, ch)
		}
	}
	return channels
}

func (c *controlChannelImpl) writePbMessage(msg *pb.Message) error {
	c.mu.Lock()
	ctx := c.lifecycleCtx
	c.mu.Unlock()
	if ctx == nil {
		return errors.New("control channel lifecycle context is not initialized")
	}
	return c.writePbMessageContext(ctx, msg)
}

func (c *controlChannelImpl) newCloseContext() (context.Context, context.CancelFunc) {
	c.mu.Lock()
	timeout := c.closeTimeout
	c.mu.Unlock()
	if timeout <= 0 {
		timeout = defaultCloseTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

func (c *controlChannelImpl) writePbMessageContext(ctx context.Context, msg *pb.Message) error {
	if ctx == nil {
		return errors.New("write context is required")
	}
	if err := c.writeLock.Lock(ctx); err != nil {
		return err
	}
	defer c.writeLock.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	var stop func() bool
	var interruptDone chan struct{}
	deadline, hasDeadline := ctx.Deadline()
	if conn != nil {
		if hasDeadline {
			if err := conn.SetWriteDeadline(deadline); err != nil {
				return &controlChannelWriteError{err: fmt.Errorf("failed to set control channel write deadline: %w", err)}
			}
		}
		interruptDone = make(chan struct{})
		stop = context.AfterFunc(ctx, func() {
			_ = conn.SetWriteDeadline(time.Now())
			close(interruptDone)
		})
	}
	err := writePbMessage(c.w, msg)
	if stop != nil {
		if !stop() {
			<-interruptDone
		}
		if clearErr := conn.SetWriteDeadline(time.Time{}); err == nil && clearErr != nil && !errors.Is(clearErr, net.ErrClosed) && !errors.Is(clearErr, io.ErrClosedPipe) {
			err = fmt.Errorf("failed to clear control channel write deadline: %w", clearErr)
		}
	}
	if cause := context.Cause(ctx); cause != nil {
		return &controlChannelWriteError{err: cause}
	}
	if err != nil && hasDeadline && !time.Now().Before(deadline) {
		<-ctx.Done()
		return &controlChannelWriteError{err: context.Cause(ctx)}
	}
	if err != nil {
		return &controlChannelWriteError{err: err}
	}
	return nil
}

func (c *controlChannelImpl) onError(err error) {
	c.mu.Lock()
	cleanup := c.detachCleanupLocked(err)
	c.mu.Unlock()
	if cleanup != nil {
		cleanup.run()
	}
}

func (c *controlChannelImpl) detachCleanupLocked(err error) *controlChannelCleanup {
	if c.closed {
		return nil
	}
	c.closed = true
	c.err = err
	cleanup := &controlChannelCleanup{
		err:             err,
		lifecycleCancel: c.lifecycleCancel,
		conn:            c.conn,
		doneCh:          c.doneCh,
		closedCh:        c.ensureClosedSignalLocked(),
		lifecycleWG:     &c.lifecycleWG,
	}
	for _, p := range c.pendingTunnels {
		cleanup.pendingTunnels = append(cleanup.pendingTunnels, p)
	}
	for _, t := range c.tunnels {
		cleanup.tunnels = append(cleanup.tunnels, t)
	}
	c.pendingTunnels = nil
	c.tunnels = nil
	for _, l := range c.datagramTunnels {
		cleanup.datagramTunnels = append(cleanup.datagramTunnels, l)
	}
	c.datagramTunnels = nil
	for _, ch := range c.datagramChannels {
		cleanup.datagramChannels = append(cleanup.datagramChannels, ch)
	}
	c.datagramChannels = nil
	for _, transport := range c.proxyTransports {
		cleanup.proxyTransports = append(cleanup.proxyTransports, transport)
	}
	c.proxyTransports = nil
	c.lifecycleCancel = nil
	return cleanup
}

func (c *controlChannelCleanup) run() {
	for _, p := range c.pendingTunnels {
		close(p.respCh)
		p.closeReady()
	}
	for _, t := range c.tunnels {
		tunnelErr := c.err
		if c.err != nil {
			tunnelErr = fmt.Errorf("control channel closed: %w", c.err)
		}
		t.onError(tunnelErr)
	}
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
	}
	for _, listener := range c.datagramTunnels {
		_ = listener.Close()
	}
	for _, channel := range c.datagramChannels {
		_ = channel.Close()
	}
	for _, transport := range c.proxyTransports {
		_ = closeAutoTransport(transport)
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	go c.finish()
}

func (c *controlChannelCleanup) finish() {
	if c.lifecycleWG != nil {
		c.lifecycleWG.Wait()
	}
	c.doneCh <- c.err
	close(c.doneCh)
	close(c.closedCh)
}

func (c *controlChannelImpl) ensureClosedSignalLocked() chan struct{} {
	if c.closedCh == nil {
		c.closedCh = make(chan struct{})
	}
	return c.closedCh
}

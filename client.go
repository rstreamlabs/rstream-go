// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
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
}

type Config struct {
	EnableHeartbeat   *bool
	HeartbeatInterval *time.Duration
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
	engine := c.EngineURL
	if engine == nil {
		return nil, errors.New("engine URL is required")
	}
	return engine, nil
}

func (c *Client) getClientDetails(engine *string, token *string) (*ClientDetails, error) {
	var err error
	if token != nil && *token != "" && tlsConfigHasClientCertificate(c.TLSClientConfig) {
		return nil, errors.New("token and mTLS authentication cannot be used together")
	}
	if engine == nil {
		engine, err = c.getEngine()
		if err != nil {
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
		resolved, err := c.getEngine()
		if err != nil {
			return nil, err
		}
		engine = resolved
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

func (c *Client) defaultTunnelTransport() Dialer {
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	if isNilDialer(c.Transport) {
		c.Transport = &AutoTransport{}
	}
	return c.Transport
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
	dialTypeProxyReq               dialType = "proxy_req"
	dialTypeStreamReq              dialType = "stream_req"
	proxyConnectionDeliveryTimeout          = 5 * time.Second
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
	chCtx, chCancel := context.WithCancel(context.Background())
	laddr := &Addr{IdOrName: streamID}
	ch := &quicDatagramChannel{channelID: channelID, provider: provider, laddr: laddr, raddr: &raddr, recvCh: make(chan []byte, 64), ctx: chCtx, cancel: chCancel, readDeadline: newDatagramDeadline(), writeDeadline: newDatagramDeadline()}
	ch.onClose = func(ch *quicDatagramChannel) {
		registry.unregisterDatagramChannel(channelID, ch)
		_ = conn.Close()
	}
	if !registry.registerDatagramChannel(channelID, ch) {
		_ = conn.Close()
		ch.Close()
		return nil, true, fmt.Errorf("datagram channel ID collision")
	}
	go watchDatagramChannelMessages(r, streamID, ch)
	return ch, true, nil
}

func watchDatagramChannelMessages(r *bufio.Reader, streamID string, ch *quicDatagramChannel) {
	for {
		msg, err := readPbMessage(r)
		if err != nil {
			ch.Close()
			return
		}
		closeMsg := msg.GetDatagramChannelClose()
		if closeMsg == nil {
			continue
		}
		if closeMsg.StreamId != streamID {
			continue
		}
		ch.Close()
		return
	}
}

type pendingOpenTunnelReq struct {
	respCh    chan *pb.OpenTunnelRsp
	readyCh   chan struct{}
	readyOnce sync.Once
}

func (p *pendingOpenTunnelReq) closeReady() {
	p.readyOnce.Do(func() { close(p.readyCh) })
}

type controlChannelImpl struct {
	logger            *slog.Logger
	client            *Client
	clientID          string
	enableHeartbeat   bool
	heartbeatInterval time.Duration
	conn              net.Conn
	w                 *bufio.Writer
	r                 *bufio.Reader
	serverDetails     *ServerDetails
	doneCh            chan error
	pendingTunnels    map[string]*pendingOpenTunnelReq
	tunnels           map[string]*bytestreamTunnelImpl
	proxyTransports   map[string]Dialer
	closing           bool
	closed            bool
	mu                sync.Mutex
	writeMu           sync.Mutex
	err               error
	// QUIC datagram support — non-nil when the transport implements DatagramProvider.
	datagramProvider DatagramProvider
	datagramTunnels  map[string]*quicDatagramListener           // tunnelID -> listener
	datagramChannels map[datagramChannelID]*quicDatagramChannel // channelID -> active channel
	lifecycleCtx     context.Context
	lifecycleCancel  context.CancelFunc
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
		ch = &controlChannelImpl{
			logger:            slog.With("component", "control-channel"),
			client:            c,
			enableHeartbeat:   enableHeartbeat,
			heartbeatInterval: heartbeatInterval,
			conn:              conn,
			w:                 w,
			r:                 r,
			doneCh:            make(chan error, 1),
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
			if _, ok := dp.(datagramChannelRegistry); !ok {
				go ch.datagramReadLoop()
			}
		}
		go ch.readLoop()
		if ch.enableHeartbeat && ch.heartbeatInterval > 0 {
			go ch.heartbeatLoop()
		}
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ch, nil
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
	go func() {
		msg := &pb.Message{
			Payload: &pb.Message_OpenTunnelReq{
				OpenTunnelReq: &pb.OpenTunnelReq{
					RequestId:        requestID,
					TunnelProperties: toTunnelPropertiesPb(props),
				},
			},
		}
		if err := c.writePbMessage(msg); err != nil {
			c.mu.Lock()
			c.onError(fmt.Errorf("failed to send OpenTunnelReq: %w", err))
			c.mu.Unlock()
		}
	}()
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
			} else {
				tunnelID := *rProps.ID
				tunnelCtx, tunnelCancel := context.WithCancel(c.lifecycleCtx)
				tunnel := &bytestreamTunnelImpl{
					props:    rProps,
					ctrl:     c,
					tunnelID: tunnelID,
					closeCh:  make(chan error, 1),
					conns:    make(chan net.Conn, 10),
					ctx:      tunnelCtx,
					cancel:   tunnelCancel,
				}
				c.mu.Lock()
				c.tunnels[tunnelID] = tunnel
				c.mu.Unlock()
				if rProps.Type != nil && *rProps.Type == TunnelTypeDatagram {
					if c.datagramProvider != nil && tunnelAllowsQUICDatagrams(rProps) {
						dlCtx, dlCancel := context.WithCancel(tunnelCtx)
						listener := &quicDatagramListener{
							conns:  make(chan net.PacketConn, 10),
							ctx:    dlCtx,
							cancel: dlCancel,
							laddr:  &Addr{IdOrName: tunnelID},
						}
						c.mu.Lock()
						c.datagramTunnels[tunnelID] = listener
						c.mu.Unlock()
						return &datagramTunnelImpl{inner: tunnel, pl: listener}, nil
					}
					return &datagramTunnelImpl{
						inner: tunnel,
						pl:    PacketListenerFromListener(tunnel),
					}, nil
				} else {
					return tunnel, nil
				}
			}
		default:
			return nil, errors.New("unexpected OpenTunnelRsp payload")
		}
	}
}

func (c *controlChannelImpl) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.err
	}
	if !c.closing {
		c.closing = true
		go func() {
			msg := &pb.Message{
				Payload: &pb.Message_CloseControlChannelReq{
					CloseControlChannelReq: &pb.CloseControlChannelReq{},
				},
			}
			if err := c.writePbMessage(msg); err != nil {
				c.mu.Lock()
				c.onError(fmt.Errorf("failed to send CloseControlChannelReq: %w", err))
				c.mu.Unlock()
			}
		}()
	}
	c.mu.Unlock()
	return <-c.doneCh
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
	tmp := *c.serverDetails
	return &tmp
}

func (c *controlChannelImpl) readLoop() {
	var err error = nil
	c.mu.Lock()
	defer c.mu.Unlock()
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
			cause, close, waitCh := c.handleMessage(msg)
			if cause != nil {
				err = fmt.Errorf("failed to handle message: %w", cause)
			}
			if close || err != nil {
				break
			}
			if waitCh != nil {
				c.mu.Unlock()
				select {
				case <-waitCh:
				case <-c.doneCh:
				}
				c.mu.Lock()
				if c.closed {
					break
				}
			}
		}
	}
	if err == nil {
		c.onClose()
	} else {
		c.onError(err)
	}
}

func (c *controlChannelImpl) heartbeatLoop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := time.NewTicker(c.heartbeatInterval)
	defer t.Stop()
	for {
		wait := func() bool {
			select {
			case <-c.doneCh:
				return false
			case <-t.C:
				return true
			}
		}
		c.mu.Unlock()
		timeout := wait()
		c.mu.Lock()
		if c.closed {
			break
		}
		if timeout {
			go func() {
				msg := &pb.Message{
					Payload: &pb.Message_Heartbeat{},
				}
				if err := c.writePbMessage(msg); err != nil {
					c.mu.Lock()
					c.onError(fmt.Errorf("failed to send heartbeat: %w", err))
					c.mu.Unlock()
				}
			}()
		} else {
			break
		}
	}
}

func (c *controlChannelImpl) handleMessage(msg *pb.Message) (error, bool, <-chan struct{}) {
	if c.closed {
		return nil, true, nil
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_OpenTunnelRsp:
		return nil, false, c.handleOpenTunnelRsp(payload.OpenTunnelRsp)
	case *pb.Message_CloseTunnelRsp:
		c.handleCloseTunnelRsp(payload.CloseTunnelRsp)
		return nil, false, nil
	case *pb.Message_ProxyConnReq:
		c.handleProxyConnReq(payload.ProxyConnReq)
		return nil, false, nil
	case *pb.Message_DatagramChannelClose:
		c.handleDatagramChannelClose(payload.DatagramChannelClose)
		return nil, false, nil
	case *pb.Message_CloseControlChannelRsp:
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func (c *controlChannelImpl) handleDatagramChannelClose(msg *pb.DatagramChannelClose) {
	if msg == nil {
		return
	}
	channelID, err := datagramChannelIDFromStreamID(msg.StreamId)
	if err != nil {
		c.logger.Warn("invalid datagram channel close", "stream_id", msg.StreamId, "error", err)
		return
	}
	ch := c.datagramChannels[channelID]
	if ch == nil {
		return
	}
	c.closeDatagramChannelLocked(channelID, ch)
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

func (c *controlChannelImpl) handleCloseTunnelRsp(rsp *pb.CloseTunnelRsp) {
	tunnelId := rsp.TunnelId
	tunnel, found := c.tunnels[tunnelId]
	if found {
		delete(c.tunnels, tunnelId)
		tunnel.onClose()
		// Clean up the datagram listener for this tunnel if one was registered.
		if l, ok := c.datagramTunnels[tunnelId]; ok {
			delete(c.datagramTunnels, tunnelId)
			l.Close()
		}
		c.closeDatagramChannelsForTunnelLocked(tunnelId)
	} else {
		c.logger.Warn("unexpected CloseTunnelRsp", "tunnel_id", tunnelId)
	}
}

func (c *controlChannelImpl) handleProxyConnReq(req *pb.ProxyConnReq) {
	tunnelId := req.TunnelId
	tunnel, found := c.tunnels[tunnelId]
	if !found {
		c.logger.Warn("unexpected ProxyConnReq", "tunnel_id", tunnelId, "stream_id", req.StreamId)
		return
	}
	proxyEndpoint := stringPtrFromPbValue(req.ProxyEndpoint)
	proxySecret := stringPtrFromPbValue(req.Secret)
	if proxyEndpoint != nil && (strings.TrimSpace(*proxyEndpoint) == "" || proxySecret == nil || strings.TrimSpace(*proxySecret) == "") {
		go c.sendProxyConnRsp(req.StreamId, errors.New("redirected proxy connection requires a non-empty endpoint and stream credential"))
		return
	}
	if proxyEndpoint == nil && c.datagramProvider != nil && tunnel.props.Type != nil && *tunnel.props.Type == TunnelTypeDatagram && tunnelAllowsQUICDatagrams(tunnel.props) {
		go func() {
			if !c.handleDatagramProxyConnReq(req, tunnel) {
				return
			}
			c.sendProxyConnRsp(req.StreamId, nil)
		}()
		return
	}
	proxyTransport := c.proxyTransportLocked(proxyEndpoint)
	proxyCtx := tunnel.ctx
	if proxyCtx == nil {
		proxyCtx = c.lifecycleCtx
	}
	if proxyCtx == nil {
		proxyCtx = context.Background()
	}
	go func() {
		laddr := Addr{IdOrName: req.StreamId, SourceIP: NetIPFromPbValue(req.SourceIp)}
		raddr := Addr{IdOrName: tunnelId}
		conn, err := c.client.dialEndpoint(proxyCtx, dialTypeProxyReq, proxyEndpoint, laddr, proxySecret, proxyTransport)
		if err != nil {
			c.logger.Error("failed to dial proxy connection", "error", err)
			c.sendProxyConnRsp(req.StreamId, err)
			return
		}
		if err := c.deliverProxyConnection(proxyCtx, tunnel, &bytestreamConn{conn: conn, laddr: laddr, raddr: raddr}); err != nil {
			_ = conn.Close()
			c.sendProxyConnRsp(req.StreamId, err)
			return
		}
		c.sendProxyConnRsp(req.StreamId, nil)
	}()
}

func (c *controlChannelImpl) deliverProxyConnection(ctx context.Context, tunnel *bytestreamTunnelImpl, conn net.Conn) error {
	c.mu.Lock()
	if tunnel.closing || tunnel.closed {
		c.mu.Unlock()
		return errors.New("tunnel is closing or closed")
	}
	c.mu.Unlock()
	timer := time.NewTimer(proxyConnectionDeliveryTimeout)
	defer timer.Stop()
	select {
	case tunnel.conns <- conn:
		return nil
	case <-ctx.Done():
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
	if err := c.writePbMessage(msg); err != nil {
		c.mu.Lock()
		c.onError(fmt.Errorf("failed to send ProxyConnRsp: %w", err))
		c.mu.Unlock()
	}
}

// handleDatagramProxyConnReq creates a quicDatagramChannel for a datagram
// tunnel connection request and delivers it to the appropriate listener.
func (c *controlChannelImpl) handleDatagramProxyConnReq(req *pb.ProxyConnReq, tunnel *bytestreamTunnelImpl) bool {
	channelID, err := datagramChannelIDFromStreamID(req.StreamId)
	if err != nil {
		c.logger.Warn("invalid datagram stream ID", "tunnelID", tunnel.tunnelID, "streamID", req.StreamId, "error", err)
		return false
	}
	laddr := &Addr{IdOrName: tunnel.tunnelID}
	raddr := &Addr{IdOrName: req.StreamId, SourceIP: NetIPFromPbValue(req.SourceIp)}
	registry, _ := c.datagramProvider.(datagramChannelRegistry)
	chCtx, chCancel := context.WithCancel(c.lifecycleCtx)
	ch := &quicDatagramChannel{
		channelID:     channelID,
		provider:      c.datagramProvider,
		laddr:         laddr,
		raddr:         raddr,
		recvCh:        make(chan []byte, 64),
		ctx:           chCtx,
		cancel:        chCancel,
		readDeadline:  newDatagramDeadline(),
		writeDeadline: newDatagramDeadline(),
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

func (c *controlChannelImpl) closeDatagramChannelLocked(channelID datagramChannelID, ch *quicDatagramChannel) {
	delete(c.datagramChannels, channelID)
	ch.onClose = nil
	if registry, ok := c.datagramProvider.(datagramChannelRegistry); ok {
		registry.unregisterDatagramChannel(channelID, ch)
	}
	ch.Close()
}

func (c *controlChannelImpl) closeDatagramChannelsForTunnelLocked(tunnelID string) {
	for channelID, ch := range c.datagramChannels {
		if ch.laddr != nil && ch.laddr.String() == tunnelID {
			c.closeDatagramChannelLocked(channelID, ch)
		}
	}
}

func (c *controlChannelImpl) writePbMessage(msg *pb.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writePbMessage(c.w, msg)
}

func (c *controlChannelImpl) onClose() {
	if c.closed {
		return
	}
	c.onError(nil)
}

func (c *controlChannelImpl) onError(err error) {
	if c.closed {
		return
	}
	c.closed = true
	for _, p := range c.pendingTunnels {
		close(p.respCh)
		p.closeReady()
	}
	for _, t := range c.tunnels {
		t.onError(fmt.Errorf("control channel closed: %w", err))
	}
	c.pendingTunnels = nil
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
	}
	for _, l := range c.datagramTunnels {
		l.Close()
	}
	c.datagramTunnels = nil
	for channelID, ch := range c.datagramChannels {
		c.closeDatagramChannelLocked(channelID, ch)
	}
	c.datagramChannels = nil
	for _, transport := range c.proxyTransports {
		_ = closeAutoTransport(transport)
	}
	c.proxyTransports = nil
	c.conn.Close()
	c.err = err
	c.doneCh <- err
	close(c.doneCh)
}

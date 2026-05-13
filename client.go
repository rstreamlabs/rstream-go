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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rstreamlabs/rstream-go/pb"
)

type Client struct {
	Transport       Dialer
	TLSClientConfig *tls.Config
	EngineURL       *string
	Token           *string
	NoToken         *bool
	ZeroRTT         *bool
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

func (c *Client) dialEngineWithTransport(ctx context.Context, engine *string, nextProtos *[]string, override Dialer) (net.Conn, error) {
	var err error
	if engine == nil {
		engine, err = c.getEngine()
		if err != nil {
			return nil, err
		}
	}
	transport := override
	if isNilDialer(transport) {
		transport = c.Transport
	}
	if isNilDialer(transport) {
		transport = &Transport{} // default transport
	}
	tlsCfg := c.TLSClientConfig
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

type dialType string

const (
	dialTypeProxyReq  dialType = "proxy_req"
	dialTypeStreamReq dialType = "stream_req"
)

func (c *Client) dial(ctx context.Context, dialType dialType, raddr Addr, token *string) (net.Conn, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, err
	}
	conn, err := c.dialEngine(ctx, engine, nil)
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
						err = fmt.Errorf("engine error %d: %s", proxyRsp.ProxyRsp.Error.Code, proxyRsp.ProxyRsp.Error.Message.GetValue())
					}
				}
			} else {
				streamRsp, ok := resp.Payload.(*pb.Message_StreamRsp)
				if !ok {
					err = fmt.Errorf("server did not return a StreamRsp")
				} else {
					switch rspPayload := streamRsp.StreamRsp.Payload.(type) {
					case *pb.StreamRsp_Error:
						err = fmt.Errorf("engine error %d: %s", rspPayload.Error.Code, rspPayload.Error.Message.GetValue())
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
	conn, err := c.Dial(ctx, raddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial stream: %w", err)
	}
	return PacketConnFromConn(conn, &raddr, PacketModeFramed), nil
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
	closing           bool
	closed            bool
	mu                sync.Mutex
	writeMu           sync.Mutex
	err               error
	// QUIC datagram support — non-nil when the transport implements DatagramProvider.
	datagramProvider DatagramProvider
	datagramTunnels  map[string]*quicDatagramListener           // tunnelID -> listener
	datagramChannels map[datagramChannelID]*quicDatagramChannel // channelID -> active channel
	datagramCtx      context.Context
	datagramCancel   context.CancelFunc
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
					err = fmt.Errorf("engine error %d: %s", rspPayload.Error.Code, rspPayload.Error.Message.GetValue())
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
		// Enable QUIC datagram mode if the transport supports it.
		if dp, ok := c.Transport.(DatagramProvider); ok {
			ch.datagramProvider = dp
			ch.datagramTunnels = make(map[string]*quicDatagramListener)
			ch.datagramChannels = make(map[datagramChannelID]*quicDatagramChannel)
			datagramCtx, datagramCancel := context.WithCancel(context.Background())
			ch.datagramCtx = datagramCtx
			ch.datagramCancel = datagramCancel
			go ch.datagramReadLoop()
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
			return nil, fmt.Errorf("engine error %d: %s", payload.Error.Code, payload.Error.Message.GetValue())
		case *pb.OpenTunnelRsp_TunnelProperties:
			rProps := toTunnelProperties(payload.TunnelProperties)
			if rProps.ID == nil {
				return nil, errors.New("engine did not return a tunnel ID")
			} else {
				tunnelID := *rProps.ID
				tunnel := &bytestreamTunnelImpl{
					props:    rProps,
					ctrl:     c,
					tunnelID: tunnelID,
					closeCh:  make(chan error, 1),
					conns:    make(chan net.Conn, 10),
				}
				c.mu.Lock()
				c.tunnels[tunnelID] = tunnel
				c.mu.Unlock()
				if rProps.Type != nil && *rProps.Type == TunnelTypeDatagram {
					if c.datagramProvider != nil {
						// QUIC datagram mode: use a quicDatagramListener instead of
						// wrapping the bytestream tunnel in a PacketListener.
						dlCtx, dlCancel := context.WithCancel(c.datagramCtx)
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
	case *pb.Message_CloseControlChannelRsp:
		return nil, true, nil
	default:
		return nil, false, nil
	}
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
	// QUIC datagram mode: create a quicDatagramChannel instead of dialing a new stream.
	if c.datagramProvider != nil && tunnel.props.Type != nil && *tunnel.props.Type == TunnelTypeDatagram {
		go func() {
			if !c.handleDatagramProxyConnReq(req, tunnel) {
				return
			}
			c.sendProxyConnRsp(req.StreamId)
		}()
		return
	}
	// Existing stream-based flow.
	go func() {
		laddr := Addr{IdOrName: req.StreamId, SourceIP: NetIPFromPbValue(req.SourceIp)}
		raddr := Addr{IdOrName: tunnelId}
		conn, err := c.client.dial(context.Background(), dialTypeProxyReq, laddr, stringPtrFromPbValue(req.Secret))
		if err != nil {
			c.logger.Error("failed to dial proxy connection", "error", err)
		} else {
			c.mu.Lock()
			defer c.mu.Unlock()
			if tunnel.closing || tunnel.closed {
				c.logger.Error("tunnel is closing or closed, closing proxy connection", "tunnelID", tunnelId)
				conn.Close()
			} else {
				select {
				case tunnel.conns <- &bytestreamConn{conn: conn, laddr: laddr, raddr: raddr}:
					return
				default:
					c.logger.Warn("tunnel conns channel is full, closing proxy connection", "tunnelID", tunnelId)
					conn.Close()
				}
			}
		}
	}()
	go func() {
		c.sendProxyConnRsp(req.StreamId)
	}()
}

func (c *controlChannelImpl) sendProxyConnRsp(streamID string) {
	msg := &pb.Message{
		Payload: &pb.Message_ProxyConnRsp{
			ProxyConnRsp: &pb.ProxyConnRsp{
				StreamId: streamID,
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
	laddr := &Addr{IdOrName: req.StreamId, SourceIP: NetIPFromPbValue(req.SourceIp)}
	raddr := &Addr{IdOrName: tunnel.tunnelID}
	chCtx, chCancel := context.WithCancel(c.datagramCtx)
	ch := &quicDatagramChannel{
		channelID: channelID,
		provider:  c.datagramProvider,
		laddr:     laddr,
		raddr:     raddr,
		recvCh:    make(chan []byte, 64),
		ctx:       chCtx,
		cancel:    chCancel,
		onClose: func(ch *quicDatagramChannel) {
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
		data, err := c.datagramProvider.ReceiveDatagram(c.datagramCtx)
		if err != nil {
			if c.datagramCtx.Err() == nil {
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
	ch.Close()
}

func (c *controlChannelImpl) closeDatagramChannelsForTunnelLocked(tunnelID string) {
	for channelID, ch := range c.datagramChannels {
		if ch.raddr != nil && ch.raddr.String() == tunnelID {
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
	// Clean up QUIC datagram resources.
	if c.datagramCancel != nil {
		c.datagramCancel()
	}
	for _, l := range c.datagramTunnels {
		l.Close()
	}
	c.datagramTunnels = nil
	for channelID, ch := range c.datagramChannels {
		c.closeDatagramChannelLocked(channelID, ch)
	}
	c.datagramChannels = nil
	c.conn.Close()
	c.err = err
	c.doneCh <- err
	close(c.doneCh)
}

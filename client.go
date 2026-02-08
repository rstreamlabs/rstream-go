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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rstreamlabs/rstream-go/pb"
)

type Client struct {
	ConfigFilePath  *string
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

type clientDetails struct {
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
		url, err := getDefaultEngine() // default engine URL
		if err != nil {
			return nil, fmt.Errorf("failed to get default engine URL: %w", err)
		}
		engine = &url
	}
	return engine, nil
}

func (c *Client) getClientDetails(engine *string, token *string) (*clientDetails, error) {
	var err error
	if engine == nil {
		engine, err = c.getEngine()
		if err != nil {
			return nil, err
		}
	}
	if token == nil {
		noToken := c.NoToken
		if noToken == nil {
			noToken = BoolPtr(false) // default to false
		}
		if !*noToken {
			if c.Token != nil {
				token = c.Token
			} else {
				t, err := getDefaultAuthToken(c.ConfigFilePath, engine)
				if err != nil {
					return nil, fmt.Errorf("failed to get authentication token: %w", err)
				}
				token = t
			}
		}
	}
	return getClientDetails(token)
}

func toServerDetails(details *pb.OpenControlChannelRsp_Ok_ServerDetails) *ServerDetails {
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

func (c *Client) dialEngine(ctx context.Context, engine *string, nextProtos *[]string) (net.Conn, error) {
	var err error
	if engine == nil {
		engine, err = c.getEngine()
		if err != nil {
			return nil, err
		}
	}
	transport := c.Transport
	if transport == nil {
		transport = &Transport{} // default transport
	}
	tlsCfg := c.TLSClientConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{}
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
	return transport.Dial(ctx, *engine, tlsCfg)
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
	clientDetails, cause := c.getClientDetails(engine, token)
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
	respCh chan *pb.OpenTunnelRsp
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
	clientDetails, cause := c.getClientDetails(engine, nil)
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
					ClientDetails: toClientDetailsPb(clientDetails),
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
		go ch.readLoop()
		if ch.enableHeartbeat && ch.heartbeatInterval > 0 {
			go ch.heartbeatLoop()
		}
	}
	if err != nil {
		conn.Close()
	}
	return ch, err
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
		respCh: make(chan *pb.OpenTunnelRsp, 1),
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
		c.mu.Unlock()
		return nil, ctx.Err()
	case openTunnelRsp, ok := <-pending.respCh:
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
			cause, close := c.handleMessage(msg)
			if cause != nil {
				err = fmt.Errorf("failed to handle message: %w", cause)
			}
			if close || err != nil {
				break
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
	for {
		t := time.NewTicker(c.heartbeatInterval)
		defer t.Stop()
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

func (c *controlChannelImpl) handleMessage(msg *pb.Message) (error, bool) {
	if c.closed {
		return nil, true
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_OpenTunnelRsp:
		c.handleOpenTunnelRsp(payload.OpenTunnelRsp)
		return nil, false
	case *pb.Message_CloseTunnelRsp:
		c.handleCloseTunnelRsp(payload.CloseTunnelRsp)
		return nil, false
	case *pb.Message_ProxyConnReq:
		c.handleProxyConnReq(payload.ProxyConnReq)
		return nil, false
	case *pb.Message_CloseControlChannelRsp:
		return nil, true
	default:
		return nil, false
	}
}

func (c *controlChannelImpl) handleOpenTunnelRsp(rsp *pb.OpenTunnelRsp) {
	requestId := rsp.RequestId
	pending, found := c.pendingTunnels[requestId]
	if found {
		delete(c.pendingTunnels, requestId)
		pending.respCh <- rsp
	} else {
		c.logger.Warn("unexpected OpenTunnelRsp", "rsp", rsp)
	}
}

func (c *controlChannelImpl) handleCloseTunnelRsp(rsp *pb.CloseTunnelRsp) {
	tunnelId := rsp.TunnelId
	tunnel, found := c.tunnels[tunnelId]
	if found {
		delete(c.tunnels, tunnelId)
		tunnel.onClose()
	} else {
		c.logger.Warn("unexpected CloseTunnelRsp", "rsp", rsp)
	}
}

func (c *controlChannelImpl) handleProxyConnReq(req *pb.ProxyConnReq) {
	tunnelId := req.TunnelId
	tunnel, found := c.tunnels[tunnelId]
	if found {
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
			msg := &pb.Message{
				Payload: &pb.Message_ProxyConnRsp{
					ProxyConnRsp: &pb.ProxyConnRsp{
						StreamId: req.StreamId,
					},
				},
			}
			if err := c.writePbMessage(msg); err != nil {
				c.mu.Lock()
				c.onError(fmt.Errorf("failed to send ProxyConnRsp: %w", err))
				c.mu.Unlock()
			}
		}()
	} else {
		c.logger.Warn("unexpected ProxyConnReq", "req", req)
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
	}
	for _, t := range c.tunnels {
		t.onError(fmt.Errorf("control channel closed: %w", err))
	}
	c.pendingTunnels = nil
	c.conn.Close()
	c.err = err
	c.doneCh <- err
	close(c.doneCh)
}

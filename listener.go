// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

type ListenerInfo struct {
	Status            *string
	ForwardingAddress *string
	TunnelProperties  *TunnelProperties
}

type Listener struct {
	Client                *Client
	Config                *Config
	TunnelProperties      *TunnelProperties
	AutoReconnect         *bool
	ReconnectTimeout      *time.Duration
	AutoRecreateTunnel    *bool
	RecreateTunnelTimeout *time.Duration
	AcceptQueueSize       *int
	OnListenerInfo        func(ListenerInfo)

	mu        sync.Mutex
	started   bool
	closed    bool
	connected bool
	ctrl      ControlChannel
	tunnel    net.Listener
	runErr    error
	connCh    chan net.Conn
	doneCh    chan struct{}
	logger    *log.Logger
}

type acceptResult struct {
	conn net.Conn
	err  error
}

const (
	defaultAcceptQueueSize       = 50
	defaultAutoReconnect         = true
	defaultAutoRecreateTunnel    = true
	defaultReconnectTimeout      = 5 * time.Second
	defaultRecreateTunnelTimeout = 5 * time.Second
)

func (l *Listener) Accept() (net.Conn, error) {
	err := l.start()
	if err != nil {
		return nil, err
	}
	c, ok := <-l.connCh
	if !ok {
		return nil, l.finalErr()
	}
	return c, nil
}

func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.started || l.closed {
		return nil
	}
	l.closed = true
	if l.connCh != nil {
		close(l.connCh)
		l.connCh = nil
	}
	close(l.doneCh)
	if l.ctrl != nil {
		_ = l.ctrl.Close()
	}
	return nil
}

func (l *Listener) Addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tunnel == nil {
		return nil
	} else {
		return l.tunnel.Addr()
	}
}

func (l *Listener) start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return net.ErrClosed
	} else if l.started {
		return nil
	}
	l.started = true
	if l.Client == nil {
		l.Client = &Client{} // use default client
	}
	acceptQueueSize := defaultAcceptQueueSize
	if l.AcceptQueueSize != nil {
		acceptQueueSize = *l.AcceptQueueSize
	}
	if acceptQueueSize > 0 {
		l.connCh = make(chan net.Conn, acceptQueueSize)
	} else {
		l.connCh = make(chan net.Conn)
	}
	l.doneCh = make(chan struct{})
	l.logger = log.New(os.Stdout, "[listener] ", log.LstdFlags)
	go l.run()
	return nil
}

func (l *Listener) run() {
	defer l.Close()
	l.mu.Lock()
	var runErr error = nil
	for {
		if l.closed || l.runErr != nil || runErr != nil {
			break
		}
		var ctrl ControlChannel = l.ctrl
		if ctrl == nil {
			l.logger.Println("connecting to the engine server...")
			l.mu.Unlock()
			tmp, err := l.Client.Connect(context.Background(), l.Config) // TODO : Use proper context
			l.mu.Lock()
			if l.closed || err != nil {
				if l.closed == false && err != nil {
					err = fmt.Errorf("failed to connect to the engine server: %w", err)
					l.logger.Println(err)
					l.updateInfo(StringPtr("connection failed ("+err.Error()+")"), nil)
					if l.autoReconnect() {
						l.mu.Unlock()
						l.sleep(ctrl, l.reconnectTimeout())
						l.mu.Lock()
					} else {
						runErr = err
					}
				}
				continue
			} else {
				l.logger.Println("connected to the engine server")
				l.connected = true
				ctrl = tmp
				l.ctrl = ctrl
				l.updateInfo(StringPtr("connected"), nil)
				go func(ctrl ControlChannel) {
					err := <-ctrl.Done()
					l.mu.Lock()
					defer l.mu.Unlock()
					if l.closed {
						return
					}
					if l.connected {
						err = fmt.Errorf("connection to the engine server closed unexpectedly: %w", err)
						l.logger.Println(err)
						l.connected = false
						l.ctrl = nil
						l.updateInfo(StringPtr("disconnected ("+err.Error()+")"), nil)
						if !l.autoReconnect() {
							if l.runErr == nil {
								l.runErr = err
							}
						}
					}
				}(ctrl)
			}
		}
		var tunnel net.Listener = l.tunnel
		if tunnel == nil {
			l.logger.Println("creating a new tunnel...")
			l.mu.Unlock()
			tmp, err := ctrl.CreateTunnel(context.Background(), *l.TunnelProperties) // TODO : Use proper context
			l.mu.Lock()
			var netListener net.Listener = nil
			if tmp != nil && err == nil {
				ptr, ok := tmp.(interface{ net.Listener })
				if !ok {
					err = errors.New("tunnel does not implement net.Listener")
				} else {
					netListener = ptr
				}
			}
			if l.closed || err != nil {
				if l.closed == false && l.connected && err != nil {
					err = fmt.Errorf("failed to create a new tunnel: %w", err)
					l.logger.Println(err)
					if l.autoRecreateTunnel() {
						l.mu.Unlock()
						l.sleep(ctrl, l.recreateTunnelTimeout())
						l.mu.Lock()
					} else {
						runErr = err
					}
				}
				continue
			} else {
				l.logger.Println("tunnel created")
				tunnel = netListener
				l.tunnel = tunnel
				l.updateInfo(StringPtr("online"), tmp)
			}
		}
		l.mu.Unlock()
		err := l.accept(ctrl, tunnel)
		l.mu.Lock()
		l.tunnel = nil
		if l.closed || err != nil {
			if l.closed == false && l.connected && err != nil {
				err = fmt.Errorf("tunnel ended unexpectedly: %w", err)
				l.logger.Println(err)
				l.updateInfo(StringPtr("connected"), nil)
				if l.autoRecreateTunnel() {
					l.mu.Unlock()
					l.sleep(ctrl, l.recreateTunnelTimeout())
					l.mu.Lock()
				} else {
					runErr = err
				}
			}
			continue
		}
	}
	if l.runErr == nil && runErr != nil {
		l.runErr = runErr
	}
	l.mu.Unlock()
}

func (l *Listener) accept(ctrl ControlChannel, t net.Listener) error {
	defer t.Close()
	results := make(chan acceptResult)
	go func() {
		defer close(results)
		for {
			conn, err := t.Accept()
			results <- acceptResult{conn, err}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-l.doneCh:
			return net.ErrClosed
		case clientErr := <-ctrl.Done():
			return clientErr
		case ar, ok := <-results:
			if !ok {
				return errors.New("tunnel ended unexpectedly")
			}
			if ar.err != nil {
				return ar.err
			}
			select {
			case <-l.doneCh:
				_ = ar.conn.Close()
				return net.ErrClosed
			case clientErr := <-ctrl.Done():
				_ = ar.conn.Close()
				return clientErr
			case l.connCh <- ar.conn:
			}
		}
	}
}

func (l *Listener) finalErr() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.runErr != nil {
		return l.runErr
	}
	return net.ErrClosed
}

func (l *Listener) sleep(ctrl ControlChannel, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	var ctrlDone <-chan error
	if ctrl != nil {
		ctrlDone = ctrl.Done()
	}
	select {
	case <-timer.C:
		return true
	case <-l.doneCh:
		return false
	case <-ctrlDone:
		return false
	}
}

func (l *Listener) updateInfo(status *string, t Tunnel) {
	if l.OnListenerInfo == nil {
		return
	}
	var forwarding *string
	var props *TunnelProperties
	if t != nil {
		p, perr := t.Properties()
		if perr == nil {
			props = &p
		}
		f, ferr := t.ForwardingAddress()
		if ferr == nil {
			forwarding = &f
		}
	}
	info := ListenerInfo{
		Status:            status,
		ForwardingAddress: forwarding,
		TunnelProperties:  props,
	}
	l.OnListenerInfo(info)
}

func (l *Listener) autoReconnect() bool {
	if l.AutoReconnect == nil {
		return defaultAutoReconnect
	} else {
		return *l.AutoReconnect
	}
}

func (l *Listener) reconnectTimeout() time.Duration {
	if l.ReconnectTimeout == nil {
		return defaultReconnectTimeout
	} else {
		return *l.ReconnectTimeout
	}
}

func (l *Listener) autoRecreateTunnel() bool {
	if l.AutoRecreateTunnel == nil {
		return defaultAutoRecreateTunnel
	} else {
		return *l.AutoRecreateTunnel
	}
}

func (l *Listener) recreateTunnelTimeout() time.Duration {
	if l.RecreateTunnelTimeout == nil {
		return defaultRecreateTunnelTimeout
	} else {
		return *l.RecreateTunnelTimeout
	}
}

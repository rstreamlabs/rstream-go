// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/rstreamlabs/rstream-go/pb"
)

type TunnelType string

const (
	TunnelBytestream TunnelType = "bytestream"
	TunnelDatagram   TunnelType = "datagram"
)

type Protocol string

const (
	ProtocolTLS  Protocol = "tls"  // bytestream
	ProtocolDTLS Protocol = "dtls" // datagram
	ProtocolQUIC Protocol = "quic" // datagram
	ProtocolHTTP Protocol = "http" // bytestream (HTTP/1.1, HTTP/2) or datagram (HTTP/3)
)

type TLSMode string

const (
	TLSModePassthrough TLSMode = "passthrough" // For TLS tunnels only
	TLSModeTerminated  TLSMode = "terminated"
)

type HTTPVersion string

const (
	HTTP1_1 HTTPVersion = "http/1.1" // HTTP/1.1 (cleartext)
	HTTP2   HTTPVersion = "h2c"      // HTTP/2 (cleartext)
	HTTP3   HTTPVersion = "h3"       // HTTP/3
)

type TunnelProperties struct {
	// Basic tunnel properties
	ID           *string    `json:"id,omitempty"`
	Name         *string    `json:"name,omitempty"`
	CreationDate *time.Time `json:"creation_date,omitempty"`

	// Tunnel options
	Type     *TunnelType       `json:"type,omitempty"`
	Publish  *bool             `json:"publish,omitempty"`
	Protocol *Protocol         `json:"protocol,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Host     *string           `json:"host,omitempty"`
	Path     *string           `json:"path,omitempty"`

	// Security options
	GeoIP      []string `json:"geo_ip,omitempty"`
	TrustedIPs []string `json:"trusted_ips,omitempty"`
	Domain     *string  `json:"domain,omitempty"`

	// TLS options
	TLSMode       *TLSMode `json:"tls_mode,omitempty"`
	TLSALPNs      []string `json:"tls_alpns,omitempty"`
	TLSMinVersion *string  `json:"tls_min_version,omitempty"`
	TLSCiphers    []string `json:"tls_ciphers,omitempty"`
	MTLS          *bool    `json:"mtls,omitempty"`
	MTLSCACertPEM *string  `json:"mtls_ca_cert_pem,omitempty"`

	// HTTP tunnel options (only for HTTP tunnels)
	HTTPVersion    *HTTPVersion `json:"http_version,omitempty"`
	HTTPUseTLS     *bool        `json:"http_use_tls,omitempty"`
	TokenAuth      *bool        `json:"token_auth,omitempty"`
	SSO            *bool        `json:"sso,omitempty"`
	SSOProviders   []string     `json:"sso_providers,omitempty"`
	EmailWhitelist []string     `json:"email_whitelist,omitempty"`
	EmailBlacklist []string     `json:"email_blacklist,omitempty"`
	Challenge      *bool        `json:"challenge,omitempty"`
}

type ListTunnelsFilters struct {
	Status   *string            `json:"status,omitempty"`
	ClientID *string            `json:"client_id,omitempty"`
	Protocol *string            `json:"protocol,omitempty"`
	Publish  *bool              `json:"publish,omitempty"`
	Labels   map[string]*string `json:"labels,omitempty"`
}

type ListTunnelsParams struct {
	Limit   *int                `json:"limit,omitempty"`
	Filters *ListTunnelsFilters `json:"filters,omitempty"`
}

type ListTunnelsResponse = []TunnelProperties

type Tunnel interface {
	ForwardingAddress() (string, error)
	Properties() (TunnelProperties, error)
	Close() error
}

type BytestreamTunnel interface {
	Tunnel
	net.Listener
}

type bytestreamTunnelImpl struct {
	props    TunnelProperties
	ctrl     *controlChannelImpl
	tunnelID string
	closeCh  chan error
	closing  bool
	closed   bool
	err      error
	conns    chan net.Conn
}

func (t *bytestreamTunnelImpl) ForwardingAddress() (string, error) {
	return FormatForwardingAddr(t.props)
}

func (t *bytestreamTunnelImpl) Properties() (TunnelProperties, error) {
	return t.props, nil
}

func (t *bytestreamTunnelImpl) Accept() (net.Conn, error) {
	t.ctrl.mu.Lock()
	if t.closed {
		t.ctrl.mu.Unlock()
		return nil, net.ErrClosed
	}
	t.ctrl.mu.Unlock()
	select {
	case conn := <-t.conns:
		return conn, nil
	case err := <-t.closeCh:
		if err == nil {
			return nil, net.ErrClosed
		}
		return nil, err
	}
}

func (t *bytestreamTunnelImpl) Addr() net.Addr {
	return &Addr{
		IdOrName: t.tunnelID,
	}
}

func (t *bytestreamTunnelImpl) Close() error {
	t.ctrl.mu.Lock()
	if t.closed {
		t.ctrl.mu.Unlock()
		return nil
	}
	if t.closing == false {
		t.closing = true
		go func() {
			msg := &pb.Message{
				Payload: &pb.Message_CloseTunnelReq{
					CloseTunnelReq: &pb.CloseTunnelReq{
						TunnelId: t.tunnelID,
					},
				},
			}
			if err := t.ctrl.writePbMessage(msg); err != nil {
				t.ctrl.mu.Lock()
				t.ctrl.onError(fmt.Errorf("failed to send CloseTunnelReq: %w", err))
				t.ctrl.mu.Unlock()
			}
		}()
	}
	t.ctrl.mu.Unlock()
	select {
	case err := <-t.closeCh:
		return err
	case <-t.ctrl.doneCh:
		return errors.New("control channel closed")
	}
}

func (t *bytestreamTunnelImpl) onClose() {
	if t.closed {
		return
	}
	t.onError(nil)
}

func (t *bytestreamTunnelImpl) onError(err error) {
	if t.closed {
		return
	}
	t.closed = true
	t.err = err
	t.closeCh <- err
	close(t.closeCh)
}

type bytestreamConn struct {
	conn  net.Conn
	laddr Addr
	raddr Addr
}

func (bc *bytestreamConn) Read(p []byte) (int, error) {
	return bc.conn.Read(p)
}

func (bc *bytestreamConn) Write(p []byte) (int, error) {
	return bc.conn.Write(p)
}

func (bc *bytestreamConn) Close() error {
	return bc.conn.Close()
}

func (bc *bytestreamConn) LocalAddr() net.Addr {
	return &bc.laddr
}

func (bc *bytestreamConn) RemoteAddr() net.Addr {
	return &bc.raddr
}

func (bc *bytestreamConn) SetDeadline(t time.Time) error {
	return bc.conn.SetDeadline(t)
}

func (bc *bytestreamConn) SetReadDeadline(t time.Time) error {
	return bc.conn.SetReadDeadline(t)
}

func (bc *bytestreamConn) SetWriteDeadline(t time.Time) error {
	return bc.conn.SetWriteDeadline(t)
}

type DatagramTunnel interface {
	Tunnel
	PacketListener
}

type datagramTunnelImpl struct {
	inner Tunnel
	pl    PacketListener
}

func (t *datagramTunnelImpl) ForwardingAddress() (string, error) {
	return t.inner.ForwardingAddress()
}

func (t *datagramTunnelImpl) Properties() (TunnelProperties, error) {
	return t.inner.Properties()
}

func (t *datagramTunnelImpl) Close() error {
	return t.inner.Close()
}

func (t *datagramTunnelImpl) Accept() (net.PacketConn, net.Addr, error) {
	return t.pl.Accept()
}

func (t *datagramTunnelImpl) Addr() net.Addr {
	return t.pl.Addr()
}

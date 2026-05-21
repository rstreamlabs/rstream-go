// See LICENSE file in the project root for license information.

package rstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	socks5Version       = byte(0x05)
	socks5NoAuth        = byte(0x00)
	socks5UserPassAuth  = byte(0x02)
	socks5NoAcceptable  = byte(0xff)
	socks5CmdConnect    = byte(0x01)
	socks5CmdUDP        = byte(0x03)
	socks5AddrIPv4      = byte(0x01)
	socks5AddrDomain    = byte(0x03)
	socks5AddrIPv6      = byte(0x04)
	socks5StatusSuccess = byte(0x00)
	socks5MaxUDPPacket  = 65535
)

type socks5ProxyConfig struct {
	address  string
	username string
	password string
	auth     bool
}

type socks5Address struct {
	host string
	port int
}

type socks5Addr struct {
	network string
	address string
}

func (a socks5Addr) Network() string {
	return a.network
}

func (a socks5Addr) String() string {
	return a.address
}

func proxyValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (d *Transport) dialSOCKS5(ctx context.Context, dialer *net.Dialer, network, proxyRaw, target string, dnsOpts dnsResolverConfig) (net.Conn, error) {
	cfg, err := d.socks5ProxyConfig(proxyRaw)
	if err != nil {
		return nil, err
	}
	proxyAddr := cfg.address
	if dnsOpts.enabled() {
		proxyAddr, err = resolveDialAddress(ctx, cfg.address, dnsOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve SOCKS5 proxy address: %w", err)
		}
	}
	conn, err := dialer.DialContext(ctx, network, proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SOCKS5 proxy: %w", err)
	}
	targetAddr, err := socks5TargetAddress(ctx, target, dnsOpts)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := socks5Handshake(ctx, conn, cfg, socks5CmdConnect, targetAddr); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (d *Transport) socks5ProxyConfig(proxyRaw string) (socks5ProxyConfig, error) {
	return socks5ProxyConfigFromRaw(proxyRaw, d.ProxyUsername, d.ProxyPassword)
}

func socks5ProxyConfigFromRaw(raw string, username, password *string) (socks5ProxyConfig, error) {
	if raw == "" {
		return socks5ProxyConfig{}, errors.New("SOCKS5 proxy URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "socks5://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return socks5ProxyConfig{}, fmt.Errorf("failed to parse SOCKS5 proxy URL: %w", err)
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return socks5ProxyConfig{}, fmt.Errorf("unsupported SOCKS5 proxy scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return socks5ProxyConfig{}, errors.New("SOCKS5 proxy URL must include a host")
	}
	port := u.Port()
	if port == "" {
		port = "1080"
	}
	cfg := socks5ProxyConfig{
		address: net.JoinHostPort(host, port),
	}
	if username != nil || password != nil {
		if username == nil || password == nil {
			return socks5ProxyConfig{}, errors.New("SOCKS5 proxy username and password must be configured together")
		}
		cfg.username = *username
		cfg.password = *password
		cfg.auth = true
	}
	if u.User != nil && !cfg.auth {
		cfg.username = u.User.Username()
		cfg.password, _ = u.User.Password()
		cfg.auth = true
	}
	if cfg.auth && (len(cfg.username) > 255 || len(cfg.password) > 255) {
		return socks5ProxyConfig{}, errors.New("SOCKS5 proxy credentials must be at most 255 bytes")
	}
	return cfg, nil
}

func socks5TargetAddress(ctx context.Context, target string, dnsOpts dnsResolverConfig) (socks5Address, error) {
	addr := target
	var err error
	if dnsOpts.enabled() {
		addr, err = resolveDialAddress(ctx, target, dnsOpts)
		if err != nil {
			return socks5Address{}, fmt.Errorf("failed to resolve SOCKS5 target address: %w", err)
		}
	}
	host, portRaw, err := net.SplitHostPort(addr)
	if err != nil {
		return socks5Address{}, fmt.Errorf("failed to split SOCKS5 target address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return socks5Address{}, fmt.Errorf("invalid SOCKS5 target port %q", portRaw)
	}
	return socks5Address{host: host, port: port}, nil
}

func socks5Handshake(ctx context.Context, conn net.Conn, cfg socks5ProxyConfig, command byte, target socks5Address) error {
	return withContextConnDeadline(ctx, conn, func() error {
		if err := socks5Negotiate(conn, cfg); err != nil {
			return err
		}
		req, err := socks5BuildRequest(command, target)
		if err != nil {
			return err
		}
		if _, err := conn.Write(req); err != nil {
			return fmt.Errorf("failed to write SOCKS5 request: %w", err)
		}
		status, _, err := socks5ReadReply(conn)
		if err != nil {
			return err
		}
		if status != socks5StatusSuccess {
			return fmt.Errorf("SOCKS5 proxy request failed: %s", socks5StatusText(status))
		}
		return nil
	})
}

func socks5Negotiate(conn net.Conn, cfg socks5ProxyConfig) error {
	methods := []byte{socks5NoAuth}
	if cfg.auth {
		methods = append(methods, socks5UserPassAuth)
	}
	if _, err := conn.Write(append([]byte{socks5Version, byte(len(methods))}, methods...)); err != nil {
		return fmt.Errorf("failed to write SOCKS5 greeting: %w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("failed to read SOCKS5 greeting response: %w", err)
	}
	if reply[0] != socks5Version {
		return fmt.Errorf("unexpected SOCKS5 greeting version %d", reply[0])
	}
	switch reply[1] {
	case socks5NoAuth:
	case socks5UserPassAuth:
		if !cfg.auth {
			return errors.New("SOCKS5 proxy requested username/password authentication but no credentials were configured")
		}
		if err := socks5UserPass(conn, cfg.username, cfg.password); err != nil {
			return err
		}
	case socks5NoAcceptable:
		return errors.New("SOCKS5 proxy rejected all authentication methods")
	default:
		return fmt.Errorf("SOCKS5 proxy selected unsupported authentication method 0x%02x", reply[1])
	}
	return nil
}

func socks5UserPass(conn net.Conn, username, password string) error {
	req := []byte{0x01, byte(len(username))}
	req = append(req, username...)
	req = append(req, byte(len(password)))
	req = append(req, password...)
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("failed to write SOCKS5 username/password request: %w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("failed to read SOCKS5 username/password response: %w", err)
	}
	if reply[0] != 0x01 || reply[1] != 0x00 {
		return errors.New("SOCKS5 username/password authentication failed")
	}
	return nil
}

func socks5BuildRequest(command byte, target socks5Address) ([]byte, error) {
	addr, err := socks5EncodeAddress(target)
	if err != nil {
		return nil, err
	}
	req := []byte{socks5Version, command, 0x00}
	req = append(req, addr...)
	return req, nil
}

func socks5EncodeAddress(target socks5Address) ([]byte, error) {
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, uint16(target.port))
	if ip := net.ParseIP(target.host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out := []byte{socks5AddrIPv4}
			out = append(out, v4...)
			return append(out, port...), nil
		}
		v6 := ip.To16()
		if v6 == nil {
			return nil, fmt.Errorf("invalid SOCKS5 IP address %q", target.host)
		}
		out := []byte{socks5AddrIPv6}
		out = append(out, v6...)
		return append(out, port...), nil
	}
	if len(target.host) == 0 || len(target.host) > 255 {
		return nil, fmt.Errorf("invalid SOCKS5 domain length for %q", target.host)
	}
	out := []byte{socks5AddrDomain, byte(len(target.host))}
	out = append(out, target.host...)
	return append(out, port...), nil
}

func socks5ReadReply(conn net.Conn) (byte, socks5Address, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return 0, socks5Address{}, fmt.Errorf("failed to read SOCKS5 reply header: %w", err)
	}
	if head[0] != socks5Version {
		return 0, socks5Address{}, fmt.Errorf("unexpected SOCKS5 reply version %d", head[0])
	}
	if head[2] != 0x00 {
		return 0, socks5Address{}, fmt.Errorf("unexpected SOCKS5 reply reserved byte 0x%02x", head[2])
	}
	addr, err := socks5ReadAddress(conn, head[3])
	if err != nil {
		return 0, socks5Address{}, err
	}
	return head[1], addr, nil
}

func socks5ReadAddress(r io.Reader, atyp byte) (socks5Address, error) {
	var host string
	switch atyp {
	case socks5AddrIPv4:
		raw := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(r, raw); err != nil {
			return socks5Address{}, fmt.Errorf("failed to read SOCKS5 IPv4 address: %w", err)
		}
		host = net.IP(raw).String()
	case socks5AddrIPv6:
		raw := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(r, raw); err != nil {
			return socks5Address{}, fmt.Errorf("failed to read SOCKS5 IPv6 address: %w", err)
		}
		host = net.IP(raw).String()
	case socks5AddrDomain:
		size := []byte{0}
		if _, err := io.ReadFull(r, size); err != nil {
			return socks5Address{}, fmt.Errorf("failed to read SOCKS5 domain length: %w", err)
		}
		raw := make([]byte, int(size[0]))
		if _, err := io.ReadFull(r, raw); err != nil {
			return socks5Address{}, fmt.Errorf("failed to read SOCKS5 domain: %w", err)
		}
		host = string(raw)
	default:
		return socks5Address{}, fmt.Errorf("unsupported SOCKS5 address type 0x%02x", atyp)
	}
	portRaw := make([]byte, 2)
	if _, err := io.ReadFull(r, portRaw); err != nil {
		return socks5Address{}, fmt.Errorf("failed to read SOCKS5 port: %w", err)
	}
	return socks5Address{host: host, port: int(binary.BigEndian.Uint16(portRaw))}, nil
}

func socks5StatusText(status byte) string {
	switch status {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("status 0x%02x", status)
	}
}

type socks5UDPConn struct {
	control net.Conn
	udp     *net.UDPConn
	relay   *net.UDPAddr
	target  socks5Address
	mu      sync.Mutex
	closed  bool
}

var _ net.PacketConn = (*socks5UDPConn)(nil)

func newSOCKS5UDPConn(ctx context.Context, tcpDialer *net.Dialer, udpNetwork, tcpNetwork string, localAddr *net.UDPAddr, proxyRaw, target string, dnsOpts dnsResolverConfig, username, password *string) (*socks5UDPConn, net.Addr, error) {
	cfg, err := socks5ProxyConfigFromRaw(proxyRaw, username, password)
	if err != nil {
		return nil, nil, err
	}
	proxyAddr := cfg.address
	if dnsOpts.enabled() {
		proxyAddr, err = resolveDialAddress(ctx, cfg.address, dnsOpts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve SOCKS5 proxy address: %w", err)
		}
	}
	control, err := tcpDialer.DialContext(ctx, tcpNetwork, proxyAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial SOCKS5 proxy: %w", err)
	}
	targetAddr, err := socks5TargetAddress(ctx, target, dnsOpts)
	if err != nil {
		_ = control.Close()
		return nil, nil, err
	}
	var relayAddr socks5Address
	if err := withContextConnDeadline(ctx, control, func() error {
		if err := socks5Negotiate(control, cfg); err != nil {
			return err
		}
		req, err := socks5BuildRequest(socks5CmdUDP, socks5Address{host: "0.0.0.0", port: 0})
		if err != nil {
			return err
		}
		if _, err := control.Write(req); err != nil {
			return fmt.Errorf("failed to write SOCKS5 UDP associate request: %w", err)
		}
		status, addr, err := socks5ReadReply(control)
		if err != nil {
			return err
		}
		if status != socks5StatusSuccess {
			return fmt.Errorf("SOCKS5 UDP associate failed: %s", socks5StatusText(status))
		}
		relayAddr = addr
		return nil
	}); err != nil {
		_ = control.Close()
		return nil, nil, err
	}
	udpConn, err := net.ListenUDP(udpNetwork, localAddr)
	if err != nil {
		_ = control.Close()
		return nil, nil, fmt.Errorf("failed to create UDP socket for SOCKS5 proxy: %w", err)
	}
	relay, err := socks5RelayUDPAddr(relayAddr, control.RemoteAddr())
	if err != nil {
		_ = udpConn.Close()
		_ = control.Close()
		return nil, nil, err
	}
	conn := &socks5UDPConn{
		control: control,
		udp:     udpConn,
		relay:   relay,
		target:  targetAddr,
	}
	return conn, socks5Addr{network: "udp", address: net.JoinHostPort(targetAddr.host, strconv.Itoa(targetAddr.port))}, nil
}

func socks5RelayUDPAddr(addr socks5Address, controlRemote net.Addr) (*net.UDPAddr, error) {
	host := addr.host
	if host == "" || host == "0.0.0.0" || host == "::" {
		if tcpAddr, ok := controlRemote.(*net.TCPAddr); ok {
			host = tcpAddr.IP.String()
		}
	}
	if host == "" || addr.port == 0 {
		return nil, fmt.Errorf("SOCKS5 proxy returned invalid UDP relay address %q:%d", addr.host, addr.port)
	}
	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(addr.port)))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve SOCKS5 UDP relay address: %w", err)
	}
	return udpAddr, nil
}

func (c *socks5UDPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	buf := make([]byte, socks5MaxUDPPacket)
	for {
		n, addr, err := c.udp.ReadFromUDP(buf)
		if err != nil {
			return 0, nil, err
		}
		if !socks5SameUDPAddr(addr, c.relay) {
			continue
		}
		payload, source, err := socks5DecodeUDPDatagram(buf[:n])
		if err != nil {
			continue
		}
		return copy(b, payload), socks5Addr{network: "udp", address: net.JoinHostPort(source.host, strconv.Itoa(source.port))}, nil
	}
}

func (c *socks5UDPConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	target := c.target
	if addr != nil && addr.String() != "" {
		if parsed, err := socks5TargetAddress(context.Background(), addr.String(), dnsResolverConfig{}); err == nil {
			target = parsed
		}
	}
	packet, err := socks5BuildUDPDatagram(target, p)
	if err != nil {
		return 0, err
	}
	if _, err := c.udp.WriteToUDP(packet, c.relay); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *socks5UDPConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	err := c.udp.Close()
	if controlErr := c.control.Close(); controlErr != nil && err == nil {
		err = controlErr
	}
	return err
}

func (c *socks5UDPConn) LocalAddr() net.Addr {
	return c.udp.LocalAddr()
}

func (c *socks5UDPConn) SetDeadline(t time.Time) error {
	return c.udp.SetDeadline(t)
}

func (c *socks5UDPConn) SetReadDeadline(t time.Time) error {
	return c.udp.SetReadDeadline(t)
}

func (c *socks5UDPConn) SetWriteDeadline(t time.Time) error {
	return c.udp.SetWriteDeadline(t)
}

func (c *socks5UDPConn) SetReadBuffer(bytes int) error {
	return c.udp.SetReadBuffer(bytes)
}

func (c *socks5UDPConn) SetWriteBuffer(bytes int) error {
	return c.udp.SetWriteBuffer(bytes)
}

func socks5BuildUDPDatagram(target socks5Address, payload []byte) ([]byte, error) {
	addr, err := socks5EncodeAddress(target)
	if err != nil {
		return nil, err
	}
	packet := []byte{0x00, 0x00, 0x00}
	packet = append(packet, addr...)
	packet = append(packet, payload...)
	return packet, nil
}

func socks5DecodeUDPDatagram(packet []byte) ([]byte, socks5Address, error) {
	if len(packet) < 4 {
		return nil, socks5Address{}, errors.New("short SOCKS5 UDP datagram")
	}
	if packet[0] != 0x00 || packet[1] != 0x00 {
		return nil, socks5Address{}, errors.New("invalid SOCKS5 UDP reserved field")
	}
	if packet[2] != 0x00 {
		return nil, socks5Address{}, errors.New("fragmented SOCKS5 UDP datagrams are not supported")
	}
	reader := bytes.NewReader(packet[4:])
	addr, err := socks5ReadAddress(reader, packet[3])
	if err != nil {
		return nil, socks5Address{}, err
	}
	payloadOffset := 4 + len(packet[4:]) - reader.Len()
	return packet[payloadOffset:], addr, nil
}

func socks5SameUDPAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"
)

const defaultMasqueUDPTemplatePath = "/.well-known/masque/udp/{target_host}/{target_port}/"
const masqueProxyInitialPacketSize = 1500

func (t *QUICTransport) directPacketConn(network string, localAddr *net.UDPAddr, dialAddr string) (net.PacketConn, net.Addr, error) {
	pconn, err := net.ListenUDP(network, localAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}
	udpAddr, err := net.ResolveUDPAddr(network, dialAddr)
	if err != nil {
		_ = pconn.Close()
		return nil, nil, fmt.Errorf("failed to resolve UDP address %q: %w", dialAddr, err)
	}
	return pconn, udpAddr, nil
}

func (t *QUICTransport) connectHTTPProxy(ctx context.Context, proxyRaw, target string, _ *tls.Config, _ *quic.Config, dnsOpts dnsResolverConfig, network string, localAddr *net.UDPAddr) (net.PacketConn, io.Closer, net.Addr, error) {
	if t.ProxyUsername != nil || t.ProxyPassword != nil {
		return nil, nil, nil, errors.New("QUIC transport uses MASQUE CONNECT-UDP and does not support proxy username/password fields")
	}
	if len(t.ProxyHTTPHeaders) > 0 {
		return nil, nil, nil, errors.New("QUIC transport uses MASQUE CONNECT-UDP and does not support custom HTTP proxy headers")
	}
	tpl, tlsProxyCfg, err := t.masqueProxyTemplate(ctx, proxyRaw, dnsOpts)
	if err != nil {
		return nil, nil, nil, err
	}
	targetAddr := target
	if dnsOpts.enabled() {
		targetAddr, err = resolveDialAddress(ctx, target, dnsOpts)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to resolve MASQUE target address: %w", err)
		}
	}
	remoteAddr := socks5Addr{network: "udp", address: targetAddr}
	pconn, closer, err := dialMASQUEUDPProxy(ctx, tpl, tlsProxyCfg, network, localAddr, targetAddr, remoteAddr, dnsOpts)
	if err != nil {
		return nil, nil, nil, err
	}
	return pconn, closer, remoteAddr, nil
}

func (t *QUICTransport) masqueProxyTemplate(_ context.Context, proxyRaw string, _ dnsResolverConfig) (*uritemplate.Template, *tls.Config, error) {
	proxyURL, err := url.Parse(proxyRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse MASQUE proxy URL: %w", err)
	}
	if proxyURL.Scheme != "https" {
		return nil, nil, fmt.Errorf("QUIC transport over HTTP proxy requires an HTTPS MASQUE proxy, got %q", proxyURL.Scheme)
	}
	host := proxyURL.Hostname()
	if host == "" {
		return nil, nil, errors.New("MASQUE proxy URL must include a host")
	}
	if proxyURL.User != nil {
		return nil, nil, errors.New("QUIC transport uses MASQUE CONNECT-UDP and does not support proxy URL credentials")
	}
	port := proxyURL.Port()
	if port == "" {
		port = "443"
	}
	proxyURL.Host = net.JoinHostPort(host, port)
	path := proxyURL.Path
	switch {
	case path == "" || path == "/":
		path = defaultMasqueUDPTemplatePath
	case strings.Contains(path, "{target_host}") && strings.Contains(path, "{target_port}"):
	default:
		return nil, nil, errors.New("MASQUE proxy URL path must be empty or include {target_host} and {target_port}")
	}
	templateRaw := proxyURL.Scheme + "://" + proxyURL.Host + path
	if proxyURL.RawQuery != "" {
		templateRaw += "?" + proxyURL.RawQuery
	}
	tpl, err := uritemplate.New(templateRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse MASQUE URI template: %w", err)
	}
	tlsProxyCfg := t.masqueTLSConfig(host)
	return tpl, tlsProxyCfg, nil
}

func (t *QUICTransport) masqueTLSConfig(serverName string) *tls.Config {
	var cfg *tls.Config
	if t.TLSProxyConfig != nil {
		cfg = t.TLSProxyConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	if !hasNextProto(cfg.NextProtos, http3.NextProtoH3) {
		cfg.NextProtos = append([]string{http3.NextProtoH3}, cfg.NextProtos...)
	}
	if cfg.ServerName == "" && !cfg.InsecureSkipVerify && net.ParseIP(serverName) == nil {
		cfg.ServerName = serverName
	}
	return cfg
}

func hasNextProto(protos []string, proto string) bool {
	for _, candidate := range protos {
		if candidate == proto {
			return true
		}
	}
	return false
}

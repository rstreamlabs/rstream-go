// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

// Default transport implementation
type Transport struct {
	LocalAddr            *string
	NetworkInterface     *string
	ForceIPv4            *bool
	ForceIPv6            *bool
	DNSOverride          *string
	DNSOverTLS           *bool
	DNSServerName        *string
	DNSSECEnabled        *bool
	MPTCPEnabled         *bool
	ProxyHTTP            *string
	ProxySOCKS5          *string
	ProxyUsername        *string
	ProxyPassword        *string
	ProxyHTTPHeaders     map[string]string
	TLSProxyConfig       *tls.Config
	ProxyFromEnvironment *bool
}

func (d *Transport) Dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	var localAddr net.Addr = nil
	if d.LocalAddr != nil {
		ip := net.ParseIP(*d.LocalAddr)
		if ip == nil {
			return nil, errors.New("failed to parse local address")
		}
		localAddr = &net.TCPAddr{IP: ip}
	} else if d.NetworkInterface != nil {
		ip, err := selectInterfaceIP(*d.NetworkInterface, boolValue(d.ForceIPv4), boolValue(d.ForceIPv6))
		if err != nil {
			return nil, err
		}
		if ip == nil {
			return nil, errors.New("no matching IP for selected interface")
		}
		localAddr = &net.TCPAddr{IP: ip}
	}
	network := "tcp"
	if d.ForceIPv4 != nil && *d.ForceIPv4 {
		network = "tcp4"
	} else if d.ForceIPv6 != nil && *d.ForceIPv6 {
		network = "tcp6"
	}
	dialer := &net.Dialer{
		LocalAddr: localAddr,
	}
	if d.MPTCPEnabled != nil && *d.MPTCPEnabled {
		dialer.SetMultipathTCP(true)
	}
	var conn net.Conn = nil
	var err error = nil
	dnsOpts := dnsResolverOptionsFromTransport(d)
	proxyHTTP, proxySOCKS5, err := effectiveProxyURLs(proxyValue(d.ProxyHTTP), proxyValue(d.ProxySOCKS5), d.ProxyFromEnvironment, addr)
	if err != nil {
		return nil, err
	}
	switch {
	case proxyHTTP != "" && proxySOCKS5 != "":
		err = errors.New("only one proxy transport can be configured")
	case proxyHTTP == "" && proxySOCKS5 == "" && d.TLSProxyConfig != nil:
		err = errors.New("TLS proxy configuration requires an HTTP or environment proxy")
	case proxySOCKS5 != "":
		if len(d.ProxyHTTPHeaders) > 0 {
			err = errors.New("proxy HTTP headers cannot be used with SOCKS5 proxy")
		} else if d.TLSProxyConfig != nil {
			err = errors.New("TLS proxy configuration cannot be used with SOCKS5 proxy")
		} else {
			conn, err = d.dialSOCKS5(ctx, dialer, network, proxySOCKS5, addr, dnsOpts)
		}
	case proxyHTTP != "":
		var proxyURL *url.URL
		proxyURL, err = url.Parse(proxyHTTP)
		if err != nil {
			err = fmt.Errorf("failed to parse HTTP proxy URL: %w", err)
		}
		if err == nil && proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
			err = fmt.Errorf("unsupported HTTP proxy scheme %q", proxyURL.Scheme)
		}
		proxyDialAddr := ""
		if err == nil {
			proxyDialAddr, err = httpProxyDialAddress(proxyURL)
		}
		if err == nil && dnsOpts.enabled() {
			proxyDialAddr, err = resolveDialAddress(ctx, proxyDialAddr, dnsOpts)
		}
		if err == nil {
			conn, err = dialer.DialContext(ctx, network, proxyDialAddr)
			if err != nil {
				err = fmt.Errorf("failed to dial proxy: %w", err)
			}
		}
		if err == nil {
			var tlsProxyCfg *tls.Config
			if d.TLSProxyConfig != nil {
				tlsProxyCfg = d.TLSProxyConfig.Clone()
			}
			if tlsProxyCfg == nil && proxyURL.Scheme == "https" {
				tlsProxyCfg = &tls.Config{}
			} else if tlsProxyCfg != nil && proxyURL.Scheme == "http" {
				err = errors.New("cannot use TLS with HTTP proxy")
			}
			if err == nil && tlsProxyCfg != nil {
				if tlsProxyCfg.InsecureSkipVerify == false && tlsProxyCfg.ServerName == "" {
					tlsProxyCfg.ServerName = proxyURL.Hostname()
				}
				tlsConn := tls.Client(conn, tlsProxyCfg)
				err = withContextConnDeadline(ctx, tlsConn, tlsConn.Handshake)
				if err != nil {
					err = fmt.Errorf("failed to handshake with proxy: %w", err)
				} else {
					conn = tlsConn
				}
			}
		}
		if err == nil {
			username, password, ok, authErr := proxyCredentials(proxyURL, d.ProxyUsername, d.ProxyPassword)
			if authErr != nil {
				err = authErr
			}
			targetAddr := addr
			if err == nil && dnsOpts.enabled() {
				targetAddr, err = resolveDialAddress(ctx, addr, dnsOpts)
				if err != nil {
					err = fmt.Errorf("failed to resolve HTTP proxy target address: %w", err)
				}
			}
			var req *http.Request
			if err == nil {
				req = &http.Request{
					Method: http.MethodConnect,
					URL:    &url.URL{Scheme: "https", Host: targetAddr},
					Host:   targetAddr,
					Header: make(http.Header),
				}
			}
			if err == nil && ok {
				req.SetBasicAuth(username, password)
				req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
				req.Header.Del("Authorization")
			}
			if err == nil {
				for k, v := range d.ProxyHTTPHeaders {
					req.Header.Set(k, v)
				}
				err = withContextConnDeadline(ctx, conn, func() error {
					if writeErr := req.Write(conn); writeErr != nil {
						return fmt.Errorf("failed to write CONNECT request: %w", writeErr)
					}
					resp, readErr := http.ReadResponse(bufio.NewReader(conn), req)
					if readErr != nil {
						return fmt.Errorf("failed to read CONNECT response: %w", readErr)
					}
					if resp.StatusCode != http.StatusOK {
						return fmt.Errorf("failed to CONNECT: %s", resp.Status)
					}
					return nil
				})
			}
		}
	default:
		dialAddr := addr
		if dnsOpts.enabled() {
			dialAddr, err = resolveDialAddress(ctx, addr, dnsOpts)
		}
		if err == nil {
			conn, err = dialer.DialContext(ctx, network, dialAddr)
		}
		if err != nil {
			err = fmt.Errorf("failed to dial: %w", err)
		}
	}
	if err == nil && tlsCfg != nil {
		tlsConn := tls.Client(conn, tlsCfg)
		err = withContextConnDeadline(ctx, tlsConn, tlsConn.Handshake)
		if err != nil {
			err = fmt.Errorf("failed to handshake: %w", err)
		} else {
			conn = tlsConn
		}
	}
	if err != nil && conn != nil {
		conn.Close()
		conn = nil
	}
	return conn, err
}

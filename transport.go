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
	LocalAddr        *string
	NetworkInterface *string
	ForceIPv4        *bool
	ForceIPv6        *bool
	DNSOverride      *string
	MPTCPEnabled     *bool
	ProxyHTTP        *string
	ProxyUsername    *string
	ProxyPassword    *string
	ProxyHTTPHeaders map[string]string
	TLSProxyConfig   *tls.Config
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
		iface, err := net.InterfaceByName(*d.NetworkInterface)
		if err != nil {
			return nil, fmt.Errorf("failed to get interface by name: %w", err)
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			return nil, errors.New("no usable address found for interface")
		}
		var ip net.IP
		for _, addr := range addrs {
			var ipNet *net.IPNet
			switch v := addr.(type) {
			case *net.IPNet:
				ipNet = v
			case *net.IPAddr:
				ipNet = &net.IPNet{IP: v.IP, Mask: v.IP.DefaultMask()}
			}
			if ipNet != nil {
				if d.ForceIPv4 != nil && *d.ForceIPv4 && ipNet.IP.To4() != nil {
					ip = ipNet.IP
					break
				}
				if d.ForceIPv6 != nil && *d.ForceIPv6 && ipNet.IP.To16() != nil && ipNet.IP.To4() == nil {
					ip = ipNet.IP
					break
				}
				if d.ForceIPv4 == nil && d.ForceIPv6 == nil {
					ip = ipNet.IP
					break
				}
			}
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
	var resolver *net.Resolver = nil
	if d.DNSOverride != nil {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				protocol := "udp"
				if d.ForceIPv4 != nil && *d.ForceIPv4 {
					protocol = "udp4"
				} else if d.ForceIPv6 != nil && *d.ForceIPv6 {
					protocol = "udp6"
				}
				return (&net.Dialer{LocalAddr: localAddr}).DialContext(ctx, protocol, *d.DNSOverride)
			},
		}
	}
	dialer := &net.Dialer{
		LocalAddr: localAddr,
		Resolver:  resolver,
	}
	if d.MPTCPEnabled != nil && *d.MPTCPEnabled {
		dialer.SetMultipathTCP(true)
	}
	var conn net.Conn = nil
	var err error = nil
	if d.ProxyHTTP != nil {
		proxyURL, err := url.Parse(*d.ProxyHTTP)
		if err != nil {
			err = fmt.Errorf("failed to parse HTTP proxy URL: %w", err)
		}
		if err == nil {
			conn, err = dialer.DialContext(ctx, network, proxyURL.Host)
			if err != nil {
				err = fmt.Errorf("failed to dial proxy: %w", err)
			}
		}
		if err == nil {
			tlsProxyCfg := d.TLSProxyConfig
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
				err = tlsConn.Handshake()
				if err != nil {
					err = fmt.Errorf("failed to handshake with proxy: %w", err)
				} else {
					conn = tlsConn
				}
			}
		}
		if err == nil {
			req := &http.Request{
				Method: http.MethodConnect,
				URL:    proxyURL,
				Host:   addr,
				Header: make(http.Header),
			}
			if d.ProxyUsername != nil && d.ProxyPassword != nil {
				req.SetBasicAuth(*d.ProxyUsername, *d.ProxyPassword)
			}
			for k, v := range d.ProxyHTTPHeaders {
				req.Header.Set(k, v)
			}
			err = req.Write(conn)
			if err != nil {
				err = fmt.Errorf("failed to write CONNECT request: %w", err)
			} else {
				resp, err := http.ReadResponse(bufio.NewReader(conn), req)
				if err != nil {
					err = fmt.Errorf("failed to read CONNECT response: %w", err)
				} else if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("failed to CONNECT: %s", resp.Status)
				}
			}
		}
	} else {
		conn, err = dialer.DialContext(ctx, network, addr)
		if err != nil {
			err = fmt.Errorf("failed to dial: %w", err)
		}
	}
	if err == nil && tlsCfg != nil {
		tlsConn := tls.Client(conn, tlsCfg)
		err = tlsConn.Handshake()
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

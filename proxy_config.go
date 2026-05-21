// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

func effectiveProxyURLs(proxyHTTP, proxySOCKS5 string, fromEnvironment *bool, target string) (string, string, error) {
	if proxyHTTP != "" || proxySOCKS5 != "" || !boolValue(fromEnvironment) {
		return proxyHTTP, proxySOCKS5, nil
	}
	proxyURL, err := proxyURLFromEnvironment(target)
	if err != nil || proxyURL == nil {
		return "", "", err
	}
	switch proxyURL.Scheme {
	case "http", "https":
		return proxyURL.String(), "", nil
	case "socks5", "socks5h":
		return "", proxyURL.String(), nil
	default:
		return "", "", fmt.Errorf("unsupported proxy URL scheme %q from environment", proxyURL.Scheme)
	}
}

func proxyURLFromEnvironment(target string) (*url.URL, error) {
	reqURL := &url.URL{Scheme: "https", Host: target}
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(reqURL)
	if err != nil || proxyURL != nil {
		return proxyURL, err
	}
	allProxy := getEnvAny("ALL_PROXY", "all_proxy")
	if allProxy == "" {
		return nil, nil
	}
	noProxy := getEnvAny("NO_PROXY", "no_proxy")
	return (&httpproxy.Config{
		HTTPProxy:  allProxy,
		HTTPSProxy: allProxy,
		NoProxy:    noProxy,
	}).ProxyFunc()(reqURL)
}

func getEnvAny(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func proxyCredentials(proxyURL *url.URL, username, password *string) (string, string, bool, error) {
	if username != nil || password != nil {
		if username == nil || password == nil {
			return "", "", false, errors.New("proxy username and password must be configured together")
		}
		return *username, *password, true, nil
	}
	if proxyURL == nil || proxyURL.User == nil {
		return "", "", false, nil
	}
	pass, _ := proxyURL.User.Password()
	return proxyURL.User.Username(), pass, true, nil
}

func httpProxyDialAddress(proxyURL *url.URL) (string, error) {
	host := proxyURL.Hostname()
	if host == "" {
		return "", errors.New("HTTP proxy URL must include a host")
	}
	port := proxyURL.Port()
	if port == "" {
		switch proxyURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", fmt.Errorf("unsupported HTTP proxy scheme %q", proxyURL.Scheme)
		}
	}
	return net.JoinHostPort(host, port), nil
}

func selectInterfaceIP(name string, forceIPv4, forceIPv6 bool) (net.IP, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get interface by name: %w", err)
	}
	addrs, err := iface.Addrs()
	if err != nil || len(addrs) == 0 {
		return nil, errors.New("no usable address found for interface")
	}
	for _, addr := range addrs {
		ip := interfaceAddrIP(addr)
		if ip == nil {
			continue
		}
		if forceIPv4 && ip.To4() != nil {
			return ip, nil
		}
		if forceIPv6 && ip.To16() != nil && ip.To4() == nil {
			return ip, nil
		}
		if !forceIPv4 && !forceIPv6 {
			return ip, nil
		}
	}
	return nil, nil
}

func interfaceAddrIP(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

func withContextConnDeadline(ctx context.Context, conn net.Conn, fn func() error) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}
	return fn()
}

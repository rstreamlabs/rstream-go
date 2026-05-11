// See LICENSE file in the project root for license information.

// http-matrix-client is the client side of the end-to-end HTTP coverage matrix.
// It iterates upstream variants (h1, h2c, h3), makes a GET /ping request to the
// server over the corresponding rstream tunnel, and reports PASS or FAIL.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
)

const pingPayload = "pong\n"

// hostFromAddr extracts "host:443" from a forwarding address like
// "https://foo.rstream.io" → "foo.rstream.io:443".
func hostFromAddr(addr string) (string, error) {
	addr = strings.TrimRight(addr, " /")
	u, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("parse addr %q: %w", addr, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("no host in addr %q", addr)
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(host, port), nil
}

func hostPortFromPublishedHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("empty published host")
	}
	if strings.Contains(host, "://") {
		return hostFromAddr(host)
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		return net.JoinHostPort(h, p), nil
	} else {
		var addrErr *net.AddrError
		if !errors.As(err, &addrErr) || addrErr.Err != "missing port in address" {
			return "", fmt.Errorf("invalid published host %q: %w", host, err)
		}
	}
	return net.JoinHostPort(host, "443"), nil
}

// findTunnelHost resolves a tunnel's forwarding host via the rstream API.
func findTunnelHost(client *rstream.Client, name string) (string, error) {
	tunnels, err := client.ListTunnels(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("list tunnels: %w", err)
	}
	for _, t := range *tunnels {
		if t.Name != nil && *t.Name == name && t.Host != nil {
			return *t.Host, nil
		}
	}
	return "", fmt.Errorf("tunnel %q not found or has no host", name)
}

func runH1(ctx context.Context, rstreamClient *rstream.Client, rawAddr string) error {
	var targetURL string
	var transport http.RoundTripper
	if rawAddr != "" {
		host, port, err := net.SplitHostPort(rawAddr)
		if err != nil {
			return fmt.Errorf("split host:port: %w", err)
		}
		addr := net.JoinHostPort(host, port)
		targetURL = "https://" + addr + "/ping"
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", rawAddr)
			},
		}
	} else {
		targetURL = "http://http-matrix-h1/ping"
		transport = &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("split host: %w", err)
				}
				return rstreamClient.Dial(ctx, rstream.Addr{IdOrName: host})
			},
		}
	}
	httpClient := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if string(body) != pingPayload {
		return fmt.Errorf("body mismatch: got %q, want %q", string(body), pingPayload)
	}
	return nil
}

func runH2C(ctx context.Context, rstreamClient *rstream.Client, rawAddr string) error {
	var targetURL string
	var transport http.RoundTripper
	if rawAddr != "" {
		host, port, err := net.SplitHostPort(rawAddr)
		if err != nil {
			return fmt.Errorf("split host:port: %w", err)
		}
		addr := net.JoinHostPort(host, port)
		targetURL = "https://" + addr + "/ping"
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", rawAddr)
			},
		}
	} else {
		targetURL = "http://http-matrix-h2c/ping"
		transport = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("split host: %w", err)
				}
				return rstreamClient.Dial(ctx, rstream.Addr{IdOrName: host})
			},
		}
	}
	httpClient := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if string(body) != pingPayload {
		return fmt.Errorf("body mismatch: got %q, want %q", string(body), pingPayload)
	}
	return nil
}

func runH3(ctx context.Context, rstreamClient *rstream.Client, rawAddr string) error {
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	var targetURL string
	var transport http.RoundTripper
	if rawAddr != "" {
		host, port, err := net.SplitHostPort(rawAddr)
		if err != nil {
			return fmt.Errorf("split host:port: %w", err)
		}
		udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, port))
		if err != nil {
			return fmt.Errorf("resolve udp: %w", err)
		}
		targetURL = "https://" + net.JoinHostPort(host, port) + "/ping"
		transport = &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"h3"},
			},
			Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
				pc, err := net.ListenPacket("udp", "0.0.0.0:0")
				if err != nil {
					return nil, err
				}
				return quic.DialEarly(ctx, pc, udpAddr, tlsCfg, cfg)
			},
		}
	} else {
		targetURL = "https://http-matrix-h3/ping"
		transport = &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"h3"},
			},
			Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("split host: %w", err)
				}
				raddr := rstream.Addr{IdOrName: host}
				pc, err := rstreamClient.PacketDial(ctx, raddr)
				if err != nil {
					return nil, err
				}
				return quic.DialEarly(ctx, pc, &raddr, tlsCfg, cfg)
			},
		}
	}
	httpClient := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if string(body) != pingPayload {
		return fmt.Errorf("body mismatch: got %q, want %q", string(body), pingPayload)
	}
	return nil
}

type testCase struct {
	name string
	run  func(ctx context.Context) error
}

func main() {
	upstream := flag.String("upstream", "all", "upstream variant: h1, h2c, h3, all")
	addr := flag.String("addr", "", "server forwarding address (host:port) from server output")
	tunnelName := flag.String("tunnel", "", "tunnel name to look up via API (for published mode)")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case timeout")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	var rawAddr string
	if *addr != "" {
		rawAddr = *addr
	} else if *tunnelName != "" {
		host, err := findTunnelHost(client, *tunnelName)
		if err != nil {
			log.Fatalf("find tunnel: %v", err)
		}
		rawAddr, err = hostPortFromPublishedHost(host)
		if err != nil {
			log.Fatalf("published host: %v", err)
		}
	}
	allCases := []testCase{
		{
			name: "h1",
			run: func(ctx context.Context) error {
				return runH1(ctx, client, rawAddr)
			},
		},
		{
			name: "h2c",
			run: func(ctx context.Context) error {
				return runH2C(ctx, client, rawAddr)
			},
		},
		{
			name: "h3",
			run: func(ctx context.Context) error {
				return runH3(ctx, client, rawAddr)
			},
		},
	}
	var cases []testCase
	if *upstream == "all" {
		cases = allCases
	} else {
		for _, tc := range allCases {
			if tc.name == *upstream {
				cases = append(cases, tc)
				break
			}
		}
		if len(cases) == 0 {
			log.Fatalf("unknown upstream %q (known: h1, h2c, h3, all)", *upstream)
		}
	}
	var failed []string
	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		start := time.Now()
		runErr := tc.run(ctx)
		elapsed := time.Since(start)
		cancel()
		if runErr != nil {
			failed = append(failed, tc.name)
			fmt.Printf("FAIL %-8s (%.2fs): %v\n", tc.name, elapsed.Seconds(), runErr)
		} else {
			fmt.Printf("PASS %-8s (%.2fs)\n", tc.name, elapsed.Seconds())
		}
	}
	fmt.Printf("---- summary: %d passed, %d failed out of %d ----\n", len(cases)-len(failed), len(failed), len(cases))
	if len(failed) > 0 {
		os.Exit(1)
	}
}

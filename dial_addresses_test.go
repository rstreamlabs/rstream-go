// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func TestDialTriesAlternateDestination(t *testing.T) {
	for _, protocol := range []string{"tcp", "quic"} {
		for _, destination := range []string{"::1", "127.0.0.1"} {
			t.Run(protocol+"/"+destination, func(t *testing.T) {
				resolver := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
					reply := new(dns.Msg)
					reply.SetReply(request)
					q := request.Question[0]
					if q.Qtype == dns.TypeA {
						reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("127.0.0.2")})
						if destination == "127.0.0.1" {
							reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP(destination)})
						}
					} else if q.Qtype == dns.TypeAAAA && destination == "::1" {
						reply.Answer = append(reply.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET}, AAAA: net.ParseIP(destination)})
					}
					_ = w.WriteMsg(reply)
				}))
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				var conn net.Conn
				var err error
				if protocol == "quic" {
					server := newTestQUICEchoServerAt(t, net.JoinHostPort(destination, "0"))
					defer server.close()
					_, port, _ := net.SplitHostPort(server.addr)
					transport := &QUICTransport{DNSOverride: StringPtr(resolver)}
					defer transport.Close()
					conn, err = transport.Dial(ctx, net.JoinHostPort("target.test", port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
				} else {
					listener, listenErr := net.Listen("tcp", net.JoinHostPort(destination, "0"))
					if listenErr != nil {
						t.Fatal(listenErr)
					}
					defer listener.Close()
					done := make(chan struct{})
					go func() {
						defer close(done)
						accepted, acceptErr := listener.Accept()
						if acceptErr != nil {
							return
						}
						defer accepted.Close()
						_ = accepted.SetDeadline(time.Now().Add(3 * time.Second))
						_, _ = io.Copy(accepted, accepted)
					}()
					defer func() {
						_ = listener.Close()
						select {
						case <-done:
						case <-time.After(4 * time.Second):
							t.Error("echo server did not stop")
						}
					}()
					_, port, _ := net.SplitHostPort(listener.Addr().String())
					conn, err = (&Transport{DNSOverride: StringPtr(resolver)}).Dial(ctx, net.JoinHostPort("target.test", port), nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				if _, err := conn.Write([]byte("fallback")); err != nil {
					t.Fatal(err)
				}
				data := make([]byte, 8)
				if _, err := io.ReadFull(conn, data); err != nil {
					t.Fatal(err)
				}
				if string(data) != "fallback" {
					t.Fatalf("echo = %q", data)
				}
			})
		}
	}
}

func TestDialRouteRaceBoundsAndCleanup(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var active, peak, closed atomic.Int32
			entered := make(chan struct{}, 8)
			release := make(chan struct{})
			routes := dialRoutes{targets: []string{"one", "two", "three", "four"}, proxies: []string{""}}
			finished := make(chan error, 1)
			go func() {
				value, err := raceDialRoutes(ctx, routes, func(ctx context.Context, route dialRoute) (string, error) {
					count := active.Add(1)
					defer active.Add(-1)
					for old := peak.Load(); count > old; old = peak.Load() {
						if peak.CompareAndSwap(old, count) {
							break
						}
					}
					entered <- struct{}{}
					if mode == "failure" {
						return "", errors.New("refused")
					}
					select {
					case <-release:
						return route.target, nil
					case <-ctx.Done():
						// A transport can finish just as cancellation arrives. Its successful
						// late result still belongs to the racer and must be closed.
						return route.target, nil
					}
				}, func(string) { closed.Add(1) })
				if err == nil {
					closed.Add(1)
					if value == "" {
						err = errors.New("missing winner")
					}
				}
				finished <- err
			}()
			if mode != "failure" {
				for range 2 {
					select {
					case <-entered:
					case <-ctx.Done():
						t.Fatal("second attempt did not start")
					}
				}
				select {
				case <-entered:
					t.Fatal("more than two active attempts")
				default:
				}
				if mode == "cancel" {
					cancel()
				} else {
					close(release)
				}
			}
			select {
			case err := <-finished:
				if mode == "success" && err != nil {
					t.Fatal(err)
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
				if mode == "failure" && err == nil {
					t.Fatal("missing failure")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("racer did not join its attempts")
			}
			if active.Load() != 0 || peak.Load() > 2 {
				t.Fatalf("active=%d peak=%d", active.Load(), peak.Load())
			}
			if mode != "failure" && closed.Load() != 2 {
				t.Fatalf("closed=%d want both owned results", closed.Load())
			}
		})
	}
}

func TestDialProxyCancellationEndsBlockedHandshake(t *testing.T) {
	for _, protocol := range []string{"http", "socks5"} {
		t.Run(protocol, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan struct{})
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				close(accepted)
				_, _ = io.Copy(io.Discard, conn)
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transport := &Transport{}
			raw := protocol + "://" + listener.Addr().String()
			if protocol == "http" {
				transport.ProxyHTTP = &raw
			} else {
				transport.ProxySOCKS5 = &raw
			}
			done := make(chan error, 1)
			go func() {
				conn, err := transport.Dial(ctx, "remote.invalid:443", nil)
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case <-accepted:
			case <-time.After(time.Second):
				t.Fatal("proxy was not contacted")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("blocked handshake ignored cancellation")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("proxy connection leaked")
			}
		})
	}
}

func TestDialAlternatesProxyAddresses(t *testing.T) {
	resolver := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(request)
		q := request.Question[0]
		if q.Qtype == dns.TypeA {
			for _, ip := range []string{"127.0.0.2", "127.0.0.1"} {
				reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP(ip)})
			}
		}
		_ = w.WriteMsg(reply)
	}))
	for _, kind := range []string{"http", "https", "socks5-tcp", "socks5-udp", "masque"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			var conn net.Conn
			var err error
			if kind == "masque" || kind == "socks5-udp" {
				server := newTestQUICEchoServer(t)
				defer server.close()
				transport := &QUICTransport{DNSOverride: StringPtr(resolver)}
				defer transport.Close()
				if kind == "masque" {
					proxy := newTestMASQUEProxyWithOptions(t, testMASQUEProxyOptions{publicHostname: "proxy.test"})
					defer func() { _ = transport.Close(); proxy.close() }()
					transport.ProxyHTTP = &proxy.url
					transport.TLSProxyConfig = &tls.Config{InsecureSkipVerify: true}
				} else {
					proxy := newTestSOCKS5UDPProxy(t)
					defer func() { _ = transport.Close(); proxy.close() }()
					_, port, _ := net.SplitHostPort(proxy.addr)
					transport.ProxySOCKS5 = StringPtr("socks5://" + net.JoinHostPort("proxy.test", port))
				}
				conn, err = transport.Dial(ctx, server.addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
			} else {
				transport := &Transport{DNSOverride: StringPtr(resolver)}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
				defer server.Close()
				if kind == "socks5-tcp" {
					proxy := newForwardingSOCKS5Proxy(t)
					defer proxy.close()
					_, port, _ := net.SplitHostPort(proxy.addr)
					transport.ProxySOCKS5 = StringPtr("socks5://" + net.JoinHostPort("proxy.test", port))
				} else {
					proxy := newForwardingHTTPProxy(t)
					if kind == "https" {
						proxy.close()
						proxy = newForwardingHTTPSProxy(t)
						transport.TLSProxyConfig = &tls.Config{InsecureSkipVerify: true}
					}
					defer proxy.close()
					u, parseErr := url.Parse(proxy.url)
					if parseErr != nil {
						t.Fatal(parseErr)
					}
					u.Host = net.JoinHostPort("proxy.test", u.Port())
					transport.ProxyHTTP = StringPtr(u.String())
				}
				conn, err = transport.Dial(ctx, strings.TrimPrefix(server.URL, "https://"), &tls.Config{InsecureSkipVerify: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			if kind == "masque" || kind == "socks5-udp" {
				assertQUICEchoConn(t, conn)
			} else {
				assertTLSHTTPConn(t, conn)
			}
		})
	}
}

func TestDialAlternatesProxyTargets(t *testing.T) {
	resolver := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(request)
		q := request.Question[0]
		if q.Qtype == dns.TypeA {
			reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(127, 0, 0, 1)}}
		} else if q.Qtype == dns.TypeAAAA {
			reply.Answer = []dns.RR{&dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET}, AAAA: net.IPv6loopback}}
		}
		_ = w.WriteMsg(reply)
	}))
	for _, kind := range []string{"http", "https", "masque"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if kind == "masque" {
				server := newTestQUICEchoServerAt(t, "[::1]:0")
				defer server.close()
				proxy := newTestMASQUEProxy(t)
				defer proxy.close()
				transport := &QUICTransport{DNSOverride: StringPtr(resolver), ProxyHTTP: &proxy.url, TLSProxyConfig: &tls.Config{InsecureSkipVerify: true}}
				defer transport.Close()
				_, port, _ := net.SplitHostPort(server.addr)
				conn, err := transport.Dial(ctx, net.JoinHostPort("target.test", port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				assertQUICEchoConn(t, conn)
				return
			}
			listener, err := net.Listen("tcp6", "[::1]:0")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
			_ = server.Listener.Close()
			server.Listener = listener
			server.StartTLS()
			defer server.Close()
			proxy := newForwardingHTTPProxy(t)
			if kind == "https" {
				proxy.close()
				proxy = newForwardingHTTPSProxy(t)
			}
			defer proxy.close()
			transport := &Transport{DNSOverride: StringPtr(resolver), ProxyHTTP: &proxy.url}
			if kind == "https" {
				transport.TLSProxyConfig = &tls.Config{InsecureSkipVerify: true}
			}
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			conn, err := transport.Dial(ctx, net.JoinHostPort("target.test", port), &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			assertTLSHTTPConn(t, conn)
		})
	}
}

func TestDialMASQUECancellationEndsBlockedResponse(t *testing.T) {
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	accepted := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)
	server := &http3.Server{
		TLSConfig:       testQUICServerTLSConfig(t, http3.NextProtoH3),
		QUICConfig:      &quic.Config{EnableDatagrams: true},
		EnableDatagrams: true,
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			accepted <- struct{}{}
			<-r.Context().Done()
			closed <- struct{}{}
		}),
	}
	serving := make(chan struct{})
	go func() { defer close(serving); _ = server.Serve(packet) }()
	defer func() { _ = server.Close(); <-serving }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := &QUICTransport{ProxyHTTP: StringPtr("https://" + packet.LocalAddr().String()), TLSProxyConfig: &tls.Config{InsecureSkipVerify: true}}
	defer transport.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := transport.Dial(ctx, "127.0.0.1:443", &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
		if conn != nil {
			_ = conn.Close()
		}
		done <- err
	}()
	defer func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("dial did not exit after fixture shutdown")
		}
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("MASQUE request did not reach proxy")
	}
	cancel()
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("MASQUE response ignored cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("MASQUE connection leaked")
	}
}

func TestDialPreservesTLSIdentityAcrossAddresses(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	resolver := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(request)
		q := request.Question[0]
		if q.Qtype == dns.TypeA {
			for _, ip := range []string{"127.0.0.2", "127.0.0.1"} {
				reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP(ip)})
			}
		}
		_ = w.WriteMsg(reply)
	}))
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	for _, name := range []string{"example.com", "wrong.invalid"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			cfg := &tls.Config{RootCAs: roots, ServerName: name}
			conn, err := (&Transport{DNSOverride: StringPtr(resolver)}).Dial(ctx, net.JoinHostPort("example.com", port), cfg)
			if name == "wrong.invalid" {
				if conn != nil {
					_ = conn.Close()
				}
				if err == nil {
					t.Fatal("wrong TLS identity accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			assertTLSHTTPConn(t, conn)
			if cfg.ServerName != name || cfg.InsecureSkipVerify {
				t.Fatal("TLS configuration mutated")
			}
		})
	}
}

func TestDialIPv6AlternatesRespectLocalBinding(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	interfaceName := ""
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			t.Fatal(err)
		}
		for _, addr := range addresses {
			if ip := interfaceAddrIP(addr); ip != nil && ip.Equal(net.IPv6loopback) {
				interfaceName = iface.Name
			}
		}
	}
	if interfaceName == "" {
		t.Fatal("IPv6 loopback interface is required")
	}
	resolver := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(request)
		q := request.Question[0]
		if q.Qtype != dns.TypeAAAA {
			t.Error("forced IPv6 queried IPv4")
		} else {
			for _, ip := range []string{"::2", "::1"} {
				reply.Answer = append(reply.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET}, AAAA: net.ParseIP(ip)})
			}
		}
		_ = w.WriteMsg(reply)
	}))
	for _, binding := range []string{"local", "interface"} {
		t.Run(binding, func(t *testing.T) {
			server := newTestQUICEchoServerAt(t, "[::1]:0")
			defer server.close()
			_, port, _ := net.SplitHostPort(server.addr)
			transport := &QUICTransport{DNSOverride: StringPtr(resolver), ForceIPv6: BoolPtr(true)}
			if binding == "local" {
				transport.LocalAddr = StringPtr("::1")
			} else {
				transport.NetworkInterface = &interfaceName
			}
			defer transport.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			conn, err := transport.Dial(ctx, net.JoinHostPort("target.test", port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			host, _, err := net.SplitHostPort(conn.LocalAddr().String())
			if err != nil || host != "::1" {
				t.Fatalf("local=%v err=%v", conn.LocalAddr(), err)
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			assertQUICEchoConn(t, conn)
		})
	}
}

func TestQUICReconnectResolvesChangedAddressFamily(t *testing.T) {
	first := newTestQUICEchoServer(t)
	defer first.close()
	_, port, _ := net.SplitHostPort(first.addr)
	second := newTestQUICEchoServerAt(t, net.JoinHostPort("::1", port))
	defer second.close()
	var ipv6 atomic.Bool
	var lookups atomic.Int32
	resolver := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		lookups.Add(1)
		reply := new(dns.Msg)
		reply.SetReply(request)
		q := request.Question[0]
		if !ipv6.Load() && q.Qtype == dns.TypeA {
			reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(127, 0, 0, 1)}}
		}
		if ipv6.Load() && q.Qtype == dns.TypeAAAA {
			reply.Answer = []dns.RR{&dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET}, AAAA: net.IPv6loopback}}
		}
		_ = w.WriteMsg(reply)
	}))
	transport := &QUICTransport{DNSOverride: StringPtr(resolver)}
	defer transport.Close()
	for _, host := range []string{"127.0.0.1", "::1"} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		conn, err := transport.Dial(ctx, net.JoinHostPort("target.test", port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		assertQUICEchoConn(t, conn)
		remote, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil || remote != host {
			t.Errorf("remote=%v err=%v", conn.RemoteAddr(), err)
		}
		_ = conn.Close()
		if err := transport.Close(); err != nil {
			t.Fatal(err)
		}
		cancel()
		ipv6.Store(true)
	}
	if lookups.Load() != 4 {
		t.Fatalf("DNS queries=%d want a fresh A/AAAA pair per connection", lookups.Load())
	}
}

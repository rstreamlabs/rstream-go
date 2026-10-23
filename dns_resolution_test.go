// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestDNSResolverConfigFromTransports(t *testing.T) {
	tlsEnabled := true
	dnssec := true
	forceIPv6 := true
	tcpCfg := dnsResolverOptionsFromTransport(&Transport{
		DNSOverride:   StringPtr("1.1.1.1:853"),
		DNSOverTLS:    &tlsEnabled,
		DNSServerName: StringPtr("cloudflare-dns.com"),
		DNSSECEnabled: &dnssec,
		ForceIPv6:     &forceIPv6,
	})
	if !tcpCfg.enabled() || tcpCfg.override != "1.1.1.1:853" || !tcpCfg.overTLS || !tcpCfg.dnssecEnabled || !tcpCfg.forceIPv6 || tcpCfg.serverName != "cloudflare-dns.com" {
		t.Fatalf("unexpected tcp dns config: %#v", tcpCfg)
	}
	quicCfg := dnsResolverOptionsFromQUICTransport(&QUICTransport{ForceIPv4: BoolPtr(true)})
	if !quicCfg.forceIPv4 || !quicCfg.enabled() {
		t.Fatalf("unexpected quic dns config: %#v", quicCfg)
	}
	if (dnsResolverConfig{}).enabled() {
		t.Fatalf("empty resolver config should be disabled")
	}
}

func TestECHResolverOptionsFromAutoTransport(t *testing.T) {
	auto := &AutoTransport{QUIC: &QUICTransport{DNSOverride: StringPtr("1.1.1.1:853"), ForceIPv4: BoolPtr(true)}}
	opts := echResolverOptions(auto)
	if opts.DNSOverride != "1.1.1.1:853" || !opts.ForceIPv4 {
		t.Fatalf("echResolverOptions(auto) = %#v", opts)
	}
}

func TestResolveDialAddressUsesConfiguredResolver(t *testing.T) {
	resolverAddr := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		question := req.Question[0]
		if question.Name == dns.Fqdn("engine.example.com") && question.Qtype == dns.TypeA {
			resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.77")}}
		} else {
			resp.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(resp)
	}))
	got, err := resolveDialAddress(context.Background(), "engine.example.com:8443", dnsResolverConfig{override: resolverAddr, forceIPv4: true})
	if err != nil {
		t.Fatalf("resolveDialAddress() error = %v", err)
	}
	if got != "192.0.2.77:8443" {
		t.Fatalf("resolveDialAddress() = %q, want 192.0.2.77:8443", got)
	}
}

func TestResolveDialAddressAcceptsMatchingIPLiteral(t *testing.T) {
	got, err := resolveDialAddress(context.Background(), "127.0.0.1:443", dnsResolverConfig{forceIPv4: true})
	if err != nil {
		t.Fatalf("resolveDialAddress(IPv4) error = %v", err)
	}
	if got != "127.0.0.1:443" {
		t.Fatalf("resolveDialAddress(IPv4) = %q", got)
	}
	got, err = resolveDialAddress(context.Background(), "[::1]:443", dnsResolverConfig{forceIPv6: true})
	if err != nil {
		t.Fatalf("resolveDialAddress(IPv6) error = %v", err)
	}
	if got != "[::1]:443" {
		t.Fatalf("resolveDialAddress(IPv6) = %q", got)
	}
}

func TestResolveDialAddressRejectsMismatchedIPFamily(t *testing.T) {
	_, err := resolveDialAddress(context.Background(), "127.0.0.1:443", dnsResolverConfig{forceIPv6: true})
	if err == nil || !strings.Contains(err.Error(), "is not IPv6") {
		t.Fatalf("resolveDialAddress(IPv4 as IPv6) error = %v", err)
	}
	_, err = resolveDialAddress(context.Background(), "[::1]:443", dnsResolverConfig{forceIPv4: true})
	if err == nil || !strings.Contains(err.Error(), "is not IPv4") {
		t.Fatalf("resolveDialAddress(IPv6 as IPv4) error = %v", err)
	}
}

func TestResolveDialAddressRejectsInvalidAndEmptyLookups(t *testing.T) {
	if _, err := resolveDialAddress(context.Background(), "engine.example.com", dnsResolverConfig{}); err == nil || !strings.Contains(err.Error(), "failed to split host:port") {
		t.Fatalf("resolveDialAddress(invalid) error = %v, want split host error", err)
	}
	resolverAddr := startDNSResolutionTestServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Rcode = dns.RcodeNameError
		_ = w.WriteMsg(resp)
	}))
	_, err := resolveDialAddress(context.Background(), "missing.example.com:443", dnsResolverConfig{override: resolverAddr, forceIPv4: true})
	if err == nil || !strings.Contains(err.Error(), "DNS lookup returned no addresses") {
		t.Fatalf("resolveDialAddress(empty) error = %v, want no addresses error", err)
	}
}

func startDNSResolutionTestServer(t *testing.T, handler dns.Handler) string {
	t.Helper()
	var tcpListener net.Listener
	var udpConn net.PacketConn
	var err error
	for range 16 {
		tcpListener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen(tcp) error = %v", err)
		}
		udpConn, err = net.ListenPacket("udp", tcpListener.Addr().String())
		if err == nil {
			break
		}
		_ = tcpListener.Close()
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("ListenPacket(udp) error = %v", err)
		}
	}
	if udpConn == nil {
		t.Fatalf("ListenPacket(udp) exhausted retries: %v", err)
	}
	tcpServer := &dns.Server{Listener: tcpListener, Handler: handler}
	udpServer := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() {
		_ = tcpServer.ActivateAndServe()
	}()
	go func() {
		_ = udpServer.ActivateAndServe()
	}()
	t.Cleanup(func() {
		_ = udpServer.Shutdown()
		_ = tcpServer.Shutdown()
	})
	return tcpListener.Addr().String()
}

func dnsAnswerAllA(ip string) dns.HandlerFunc {
	return func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		question := req.Question[0]
		if question.Qtype == dns.TypeA {
			resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP(ip)}}
		} else {
			resp.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(resp)
	}
}

func TestDestinationFamilyDoesNotSelectDNSFamily(t *testing.T) {
	for _, targetIPv6 := range []bool{false, true} {
		for _, truncate := range []bool{false, true} {
			t.Run(fmt.Sprintf("ipv6-%t/truncate-%t", targetIPv6, truncate), func(t *testing.T) {
				resolver := startOppositeFamilyDNS(t, targetIPv6, truncate)
				destination := "127.0.0.1"
				if targetIPv6 {
					destination = "::1"
				}
				for _, protocol := range []string{"tcp", "quic"} {
					t.Run(protocol, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
						defer cancel()
						var conn net.Conn
						var err error
						if protocol == "quic" {
							server := newTestQUICEchoServerAt(t, net.JoinHostPort(destination, "0"))
							defer server.close()
							_, port, splitErr := net.SplitHostPort(server.addr)
							if splitErr != nil {
								t.Fatal(splitErr)
							}
							transport := &QUICTransport{DNSOverride: StringPtr(resolver), ForceIPv4: BoolPtr(!targetIPv6), ForceIPv6: BoolPtr(targetIPv6)}
							defer transport.Close()
							conn, err = transport.Dial(ctx, net.JoinHostPort("target.example.test", port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
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
									t.Error("TCP echo did not stop")
								}
							}()
							_, port, splitErr := net.SplitHostPort(listener.Addr().String())
							if splitErr != nil {
								t.Fatal(splitErr)
							}
							conn, err = (&Transport{DNSOverride: StringPtr(resolver), ForceIPv4: BoolPtr(!targetIPv6), ForceIPv6: BoolPtr(targetIPv6)}).Dial(ctx, net.JoinHostPort("target.example.test", port), nil)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
							t.Fatal(err)
						}
						payload := []byte("opposite-family-dns")
						if _, err := conn.Write(payload); err != nil {
							t.Fatal(err)
						}
						result := make([]byte, len(payload))
						if _, err := io.ReadFull(conn, result); err != nil {
							t.Fatal(err)
						}
						if string(result) != string(payload) {
							t.Fatalf("echo=%q", result)
						}
					})
				}
			})
		}
	}
}

func startOppositeFamilyDNS(t *testing.T, targetIPv6, truncate bool) string {
	t.Helper()
	network, address, target := "tcp6", "[::1]:0", "127.0.0.1"
	queryType := uint16(dns.TypeA)
	if targetIPv6 {
		network, address, target, queryType = "tcp4", "127.0.0.1:0", "::1", dns.TypeAAAA
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket(strings.Replace(network, "tcp", "udp", 1), listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	var tcpQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(req)
		for _, q := range req.Question {
			if q.Qtype != queryType {
				t.Errorf("forced destination family queried record type %d, want %d", q.Qtype, queryType)
			}
		}
		if truncate && w.LocalAddr().Network() == "udp" {
			reply.Truncated = true
		} else {
			if w.LocalAddr().Network() == "tcp" {
				tcpQueries.Add(1)
			}
			for _, q := range req.Question {
				header := dns.RR_Header{Name: q.Name, Rrtype: queryType, Class: dns.ClassINET}
				if targetIPv6 {
					reply.Answer = append(reply.Answer, &dns.AAAA{Hdr: header, AAAA: net.ParseIP(target)})
				} else {
					reply.Answer = append(reply.Answer, &dns.A{Hdr: header, A: net.ParseIP(target)})
				}
			}
		}
		_ = w.WriteMsg(reply)
	})
	udp := &dns.Server{PacketConn: packet, Handler: handler}
	tcp := &dns.Server{Listener: listener, Handler: handler}
	done := make(chan struct{}, 2)
	go func() { _ = udp.ActivateAndServe(); done <- struct{}{} }()
	go func() { _ = tcp.ActivateAndServe(); done <- struct{}{} }()
	t.Cleanup(func() {
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
		for range 2 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("DNS fixture did not stop")
			}
		}
		if truncate && tcpQueries.Load() != 2 {
			t.Errorf("TCP fallback requests=%d, want 2", tcpQueries.Load())
		}
	})
	return listener.Addr().String()
}

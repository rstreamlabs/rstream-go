// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"net"
	"strings"
	"testing"

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
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(tcp) error = %v", err)
	}
	udpConn, err := net.ListenPacket("udp", tcpListener.Addr().String())
	if err != nil {
		_ = tcpListener.Close()
		t.Fatalf("ListenPacket(udp) error = %v", err)
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

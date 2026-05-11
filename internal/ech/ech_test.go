// See LICENSE file in the project root for license information.

package ech

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestDiscoveryQueryForTarget(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   discoveryQuery
		ok     bool
	}{
		{
			name: "rstream control channel",
			target: Target{
				Address:    "f587ee53.c.localhost.rstream.io:443",
				ServerName: "f587ee53.c.localhost.rstream.io",
				NextProtos: []string{"rstrm/1"},
			},
			want: discoveryQuery{
				name:          "_443._rstream.c.localhost.rstream.io",
				queryType:     dns.TypeSVCB,
				supportedALPN: []string{"rstrm/1"},
			},
			ok: true,
		},
		{
			name: "published https",
			target: Target{
				Address:    "38b3df13.t.c.localhost.rstream.io:443",
				ServerName: "38b3df13.t.c.localhost.rstream.io",
				NextProtos: []string{"h2", "http/1.1"},
			},
			want: discoveryQuery{
				name:          "38b3df13.t.c.localhost.rstream.io",
				queryType:     dns.TypeHTTPS,
				supportedALPN: []string{"h2", "http/1.1"},
			},
			ok: true,
		},
		{
			name: "unknown alpn",
			target: Target{
				Address:    "38b3df13.t.c.localhost.rstream.io:443",
				ServerName: "38b3df13.t.c.localhost.rstream.io",
				NextProtos: []string{"imap"},
			},
			ok: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := discoveryQueryForTarget(tt.target)
			if ok != tt.ok {
				t.Fatalf("discoveryQueryForTarget() ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.name != tt.want.name || got.queryType != tt.want.queryType {
				t.Fatalf("discoveryQueryForTarget() = %#v, want %#v", got, tt.want)
			}
			if len(got.supportedALPN) != len(tt.want.supportedALPN) {
				t.Fatalf("supportedALPN length = %d, want %d", len(got.supportedALPN), len(tt.want.supportedALPN))
			}
			for i := range got.supportedALPN {
				if got.supportedALPN[i] != tt.want.supportedALPN[i] {
					t.Fatalf("supportedALPN[%d] = %q, want %q", i, got.supportedALPN[i], tt.want.supportedALPN[i])
				}
			}
		})
	}
}

func TestSelectConfigList(t *testing.T) {
	configList := testConfigList(t)
	record := &dns.SVCB{
		Hdr:      dns.RR_Header{Name: dns.Fqdn("_443._rstream.f587ee53.c.localhost.rstream.io"), Rrtype: dns.TypeSVCB, Class: dns.ClassINET},
		Priority: 1,
		Target:   ".",
		Value: []dns.SVCBKeyValue{
			&dns.SVCBAlpn{Alpn: []string{"rstrm/1"}},
			&dns.SVCBECHConfig{ECH: configList},
		},
	}
	got, ok := selectConfigList([]dns.RR{record}, "_443._rstream.f587ee53.c.localhost.rstream.io.", dns.TypeSVCB, []string{"rstrm/1"})
	if !ok {
		t.Fatal("expected config list")
	}
	if string(got) != string(configList) {
		t.Fatalf("selectConfigList() = %x, want %x", got, configList)
	}
}

func TestSelectConfigListRejectsUnsupportedALPN(t *testing.T) {
	configList := testConfigList(t)
	record := &dns.HTTPS{
		SVCB: dns.SVCB{
			Hdr:      dns.RR_Header{Name: dns.Fqdn("38b3df13.t.c.localhost.rstream.io"), Rrtype: dns.TypeHTTPS, Class: dns.ClassINET},
			Priority: 1,
			Target:   ".",
			Value: []dns.SVCBKeyValue{
				&dns.SVCBAlpn{Alpn: []string{"h3"}},
				&dns.SVCBECHConfig{ECH: configList},
			},
		},
	}
	if _, ok := selectConfigList([]dns.RR{record}, "38b3df13.t.c.localhost.rstream.io.", dns.TypeHTTPS, []string{"h2"}); ok {
		t.Fatal("expected ALPN mismatch")
	}
}

func TestLookupConfigListFollowsAliasMode(t *testing.T) {
	lookup := lookupDNSResponse
	configList := testConfigList(t)
	lookupDNSResponse = func(_ context.Context, qname string, queryType uint16, _ ResolverOptions) (dnsResponse, error) {
		switch {
		case qname == dns.Fqdn("_443._rstream.c.localhost.rstream.io") && queryType == dns.TypeSVCB:
			return dnsResponse{answer: []dns.RR{&dns.SVCB{
				Hdr:      dns.RR_Header{Name: dns.Fqdn(qname), Rrtype: dns.TypeSVCB, Class: dns.ClassINET},
				Priority: 0,
				Target:   dns.Fqdn("_443._rstream.ech.c.localhost.rstream.io"),
			}}}, nil
		case qname == dns.Fqdn("_443._rstream.ech.c.localhost.rstream.io") && queryType == dns.TypeSVCB:
			return dnsResponse{answer: []dns.RR{&dns.SVCB{
				Hdr:      dns.RR_Header{Name: dns.Fqdn(qname), Rrtype: dns.TypeSVCB, Class: dns.ClassINET},
				Priority: 1,
				Target:   ".",
				Value: []dns.SVCBKeyValue{
					&dns.SVCBAlpn{Alpn: []string{"rstrm/1"}},
					&dns.SVCBECHConfig{ECH: configList},
				},
			}}}, nil
		default:
			return dnsResponse{}, nil
		}
	}
	defer func() { lookupDNSResponse = lookup }()
	resolver := NewResolver()
	got, err := resolver.LookupConfigList(context.Background(), Target{
		Address:    "f587ee53.c.localhost.rstream.io:443",
		ServerName: "f587ee53.c.localhost.rstream.io",
		NextProtos: []string{"rstrm/1"},
	}, ResolverOptions{})
	if err != nil {
		t.Fatalf("LookupConfigList() error = %v", err)
	}
	if string(got) != string(configList) {
		t.Fatalf("LookupConfigList() = %x, want %x", got, configList)
	}
}

func TestResolverCachesConfigListsDefensively(t *testing.T) {
	lookup := lookupDNSResponse
	configList := testConfigList(t)
	calls := 0
	lookupDNSResponse = func(_ context.Context, qname string, queryType uint16, _ ResolverOptions) (dnsResponse, error) {
		calls++
		if qname != dns.Fqdn("_443._rstream.c.localhost.rstream.io") || queryType != dns.TypeSVCB {
			return dnsResponse{}, nil
		}
		return dnsResponse{answer: []dns.RR{&dns.SVCB{
			Hdr:      dns.RR_Header{Name: qname, Rrtype: dns.TypeSVCB, Class: dns.ClassINET},
			Priority: 1,
			Target:   ".",
			Value: []dns.SVCBKeyValue{
				&dns.SVCBAlpn{Alpn: []string{"rstrm/1"}},
				&dns.SVCBECHConfig{ECH: configList},
			},
		}}}, nil
	}
	defer func() { lookupDNSResponse = lookup }()
	resolver := NewResolver()
	target := Target{Address: "f587ee53.c.localhost.rstream.io:443", ServerName: "f587ee53.c.localhost.rstream.io", NextProtos: []string{"rstrm/1"}}
	got, err := resolver.LookupConfigList(context.Background(), target, ResolverOptions{})
	if err != nil {
		t.Fatalf("LookupConfigList() error = %v", err)
	}
	got[0] ^= 0xff
	again, err := resolver.LookupConfigList(context.Background(), target, ResolverOptions{})
	if err != nil {
		t.Fatalf("second LookupConfigList() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("lookup calls = %d, want 1", calls)
	}
	if !bytes.Equal(again, configList) {
		t.Fatalf("cached config was mutated: got %x want %x", again, configList)
	}
}

func TestResolverCachesEmptyECHLookups(t *testing.T) {
	lookup := lookupDNSResponse
	calls := 0
	lookupDNSResponse = func(context.Context, string, uint16, ResolverOptions) (dnsResponse, error) {
		calls++
		return dnsResponse{}, nil
	}
	defer func() { lookupDNSResponse = lookup }()
	resolver := NewResolver()
	target := Target{Address: "38b3df13.t.c.localhost.rstream.io:443", ServerName: "38b3df13.t.c.localhost.rstream.io", NextProtos: []string{"h2"}}
	got, err := resolver.LookupConfigList(context.Background(), target, ResolverOptions{})
	if err != nil {
		t.Fatalf("LookupConfigList() error = %v", err)
	}
	if got != nil {
		t.Fatalf("empty lookup = %x, want nil", got)
	}
	if _, err := resolver.LookupConfigList(context.Background(), target, ResolverOptions{}); err != nil {
		t.Fatalf("second LookupConfigList() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("empty lookup calls = %d, want 1", calls)
	}
}

func TestRememberConfigListValidatesAndClones(t *testing.T) {
	resolver := NewResolver()
	configList := testConfigList(t)
	target := Target{Address: "38b3df13.t.c.localhost.rstream.io:443", ServerName: "38b3df13.t.c.localhost.rstream.io", NextProtos: []string{"h2"}}
	if err := resolver.RememberConfigList(target, ResolverOptions{}, configList); err != nil {
		t.Fatalf("RememberConfigList() error = %v", err)
	}
	configList[0] ^= 0xff
	got, err := resolver.LookupConfigList(context.Background(), target, ResolverOptions{})
	if err != nil {
		t.Fatalf("LookupConfigList(cache) error = %v", err)
	}
	if bytes.Equal(got, configList) {
		t.Fatalf("cached remembered config should not share caller buffer")
	}
	if err := resolver.RememberConfigList(target, ResolverOptions{}, []byte{0, 10, 1}); err == nil || !strings.Contains(err.Error(), "length mismatch") {
		t.Fatalf("RememberConfigList(invalid) = %v, want validation error", err)
	}
}

func TestLookupHostFollowsCNAMEAndHonorsAddressFamily(t *testing.T) {
	lookup := lookupDNSResponse
	queries := []uint16{}
	lookupDNSResponse = func(_ context.Context, qname string, queryType uint16, _ ResolverOptions) (dnsResponse, error) {
		queries = append(queries, queryType)
		switch {
		case qname == dns.Fqdn("example.com") && queryType == dns.TypeA:
			return dnsResponse{answer: []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: dns.Fqdn("example.com"), Rrtype: dns.TypeCNAME, Class: dns.ClassINET}, Target: dns.Fqdn("alias.example.com")}}}, nil
		case qname == dns.Fqdn("alias.example.com") && queryType == dns.TypeA:
			return dnsResponse{answer: []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: dns.Fqdn("alias.example.com"), Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.10")}}}, nil
		case qname == dns.Fqdn("v6.example.com") && queryType == dns.TypeAAAA:
			return dnsResponse{answer: []dns.RR{&dns.AAAA{Hdr: dns.RR_Header{Name: dns.Fqdn("v6.example.com"), Rrtype: dns.TypeAAAA, Class: dns.ClassINET}, AAAA: net.ParseIP("2001:db8::1")}}}, nil
		default:
			return dnsResponse{}, nil
		}
	}
	defer func() { lookupDNSResponse = lookup }()
	addrs, err := LookupHost(context.Background(), "example.com", ResolverOptions{ForceIPv4: true})
	if err != nil {
		t.Fatalf("LookupHost(cname) error = %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "192.0.2.10" {
		t.Fatalf("LookupHost(cname) = %#v, want 192.0.2.10", addrs)
	}
	addrs, err = LookupHost(context.Background(), "v6.example.com", ResolverOptions{ForceIPv6: true})
	if err != nil {
		t.Fatalf("LookupHost(v6) error = %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "2001:db8::1" {
		t.Fatalf("LookupHost(v6) = %#v, want 2001:db8::1", addrs)
	}
	for _, queryType := range queries {
		if queryType != dns.TypeA && queryType != dns.TypeAAAA {
			t.Fatalf("unexpected query type: %d", queryType)
		}
	}
}

func TestParseConfigListRejectsMalformedInputs(t *testing.T) {
	tests := []struct {
		name      string
		raw       []byte
		wantError string
	}{
		{name: "too short", raw: []byte{0}, wantError: "too short"},
		{name: "length mismatch", raw: []byte{0, 3, 1}, wantError: "length mismatch"},
		{name: "empty list", raw: []byte{0, 0}, wantError: "empty"},
		{name: "truncated config header", raw: []byte{0, 2, 1, 2}, wantError: "truncated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseConfigList(tt.raw); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("parseConfigList() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestDNSClientOptionsAndNameHelpers(t *testing.T) {
	client, err := newDNSClient(ResolverOptions{DNSOverTLS: true, DNSOverride: "dns.example.com", ForceIPv6: true}, false)
	if err != nil {
		t.Fatalf("newDNSClient(DoT) error = %v", err)
	}
	if client.Net != "tcp6-tls" || client.TLSConfig == nil || client.TLSConfig.ServerName != "dns.example.com" {
		t.Fatalf("DoT client not configured correctly: %#v", client)
	}
	if _, err := newDNSClient(ResolverOptions{DNSOverTLS: true, DNSOverride: "1.1.1.1"}, false); err == nil || !strings.Contains(err.Error(), "dns.serverName") {
		t.Fatalf("newDNSClient(DoT IP) = %v, want serverName error", err)
	}
	if got := addressPort("example.com:8443"); got != "8443" {
		t.Fatalf("addressPort() = %q, want 8443", got)
	}
	if got := hostNoPort("example.com:8443"); got != "example.com" {
		t.Fatalf("hostNoPort() = %q, want example.com", got)
	}
	if got := normalizeDNSName(" Example.COM. "); got != "example.com" {
		t.Fatalf("normalizeDNSName() = %q, want example.com", got)
	}
	if got, ok := clusterHostForServerName("38b3df13.t.c.localhost.rstream.io"); !ok || got != "c.localhost.rstream.io" {
		t.Fatalf("clusterHostForServerName(tunnel) = %q, %v", got, ok)
	}
	if got, ok := clusterHostForServerName("f587ee53.c.localhost.rstream.io"); !ok || got != "c.localhost.rstream.io" {
		t.Fatalf("clusterHostForServerName(project) = %q, %v", got, ok)
	}
	if looksLikeProjectEndpoint("f587ee5z") || !looksLikeProjectEndpoint("f587ee53") {
		t.Fatalf("looksLikeProjectEndpoint returned unexpected values")
	}
}

func TestExchangeDNSQueryHandlesSuccessDNSSECNXDOMAINAndTCPFallback(t *testing.T) {
	addr := startECHTestDNSServers(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		q := req.Question[0]
		switch q.Name {
		case dns.Fqdn("ok.example.com"):
			resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.55")}}
		case dns.Fqdn("secure.example.com"):
			resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.56")}}
		case dns.Fqdn("missing.example.com"):
			resp.Rcode = dns.RcodeNameError
		case dns.Fqdn("secure-missing.example.com"):
			resp.Rcode = dns.RcodeNameError
		case dns.Fqdn("truncated.example.com"):
			if strings.HasPrefix(w.LocalAddr().Network(), "udp") {
				resp.Truncated = true
			} else {
				resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.57")}}
			}
		default:
			resp.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(resp)
	}))
	resp, err := exchangeDNSQuery(context.Background(), dns.Fqdn("ok.example.com"), dns.TypeA, ResolverOptions{DNSOverride: addr})
	if err != nil {
		t.Fatalf("exchangeDNSQuery(success) error = %v", err)
	}
	if len(resp.answer) != 1 {
		t.Fatalf("success answer count = %d, want 1", len(resp.answer))
	}
	if _, err := exchangeDNSQuery(context.Background(), dns.Fqdn("secure.example.com"), dns.TypeA, ResolverOptions{DNSOverride: addr, DNSSECEnabled: true}); err == nil || !strings.Contains(err.Error(), "requires DNS over TLS") {
		t.Fatalf("exchangeDNSQuery(DNSSEC) = %v, want DNSSEC error", err)
	}
	resp, err = exchangeDNSQuery(context.Background(), dns.Fqdn("missing.example.com"), dns.TypeA, ResolverOptions{DNSOverride: addr})
	if err != nil {
		t.Fatalf("exchangeDNSQuery(NXDOMAIN) error = %v", err)
	}
	if len(resp.answer) != 0 {
		t.Fatalf("NXDOMAIN answer count = %d, want 0", len(resp.answer))
	}
	if _, err := exchangeDNSQuery(context.Background(), dns.Fqdn("secure-missing.example.com"), dns.TypeA, ResolverOptions{DNSOverride: addr, DNSSECEnabled: true}); err == nil || !strings.Contains(err.Error(), "requires DNS over TLS") {
		t.Fatalf("exchangeDNSQuery(DNSSEC NXDOMAIN) = %v, want authenticated denial error", err)
	}
	resp, err = exchangeDNSQuery(context.Background(), dns.Fqdn("truncated.example.com"), dns.TypeA, ResolverOptions{DNSOverride: addr})
	if err != nil {
		t.Fatalf("exchangeDNSQuery(truncated) error = %v", err)
	}
	if len(resp.answer) != 1 {
		t.Fatalf("truncated fallback answer count = %d, want 1", len(resp.answer))
	}
}

func TestNameserversForQueryUsesOverridePortForPlainDNS(t *testing.T) {
	got, err := nameserversForQuery(context.Background(), "example.com", ResolverOptions{
		DNSOverride: "1.1.1.1",
	})
	if err != nil {
		t.Fatalf("nameserversForQuery() error = %v", err)
	}
	if len(got) != 1 || got[0] != "1.1.1.1:53" {
		t.Fatalf("nameserversForQuery() = %#v, want [\"1.1.1.1:53\"]", got)
	}
}

func TestNameserversForQueryUsesOverridePortForDoT(t *testing.T) {
	got, err := nameserversForQuery(context.Background(), "example.com", ResolverOptions{
		DNSOverride: "1.1.1.1",
		DNSOverTLS:  true,
	})
	if err != nil {
		t.Fatalf("nameserversForQuery() error = %v", err)
	}
	if len(got) != 1 || got[0] != "1.1.1.1:853" {
		t.Fatalf("nameserversForQuery() = %#v, want [\"1.1.1.1:853\"]", got)
	}
}

func TestNameserversForQueryRejectsDoTWithoutOverride(t *testing.T) {
	_, err := nameserversForQuery(context.Background(), "example.com", ResolverOptions{
		DNSOverTLS: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "DNS over TLS requires dns.override" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testConfigList(t *testing.T) []byte {
	t.Helper()
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	config := marshalECHConfig(t, 7, privateKey.PublicKey().Bytes(), "ech.c.localhost.rstream.io")
	return marshalECHConfigList(t, config)
}

func marshalECHConfigList(t *testing.T, config []byte) []byte {
	t.Helper()
	out := make([]byte, 2+len(config))
	binary.BigEndian.PutUint16(out[:2], uint16(len(config)))
	copy(out[2:], config)
	return out
}

func marshalECHConfig(t *testing.T, configID uint8, publicKey []byte, publicName string) []byte {
	t.Helper()
	publicNameBytes := []byte(publicName)
	length := 1 + 2 + 2 + len(publicKey) + 2 + 4 + 1 + 1 + len(publicNameBytes) + 2
	out := make([]byte, 4+length)
	binary.BigEndian.PutUint16(out[0:2], 0xfe0d)
	binary.BigEndian.PutUint16(out[2:4], uint16(length))
	offset := 4
	out[offset] = configID
	offset++
	binary.BigEndian.PutUint16(out[offset:offset+2], 0x0020)
	offset += 2
	binary.BigEndian.PutUint16(out[offset:offset+2], uint16(len(publicKey)))
	offset += 2
	copy(out[offset:offset+len(publicKey)], publicKey)
	offset += len(publicKey)
	binary.BigEndian.PutUint16(out[offset:offset+2], 4)
	offset += 2
	binary.BigEndian.PutUint16(out[offset:offset+2], 0x0001)
	offset += 2
	binary.BigEndian.PutUint16(out[offset:offset+2], 0x0001)
	offset += 2
	out[offset] = 32
	offset++
	out[offset] = uint8(len(publicNameBytes))
	offset++
	copy(out[offset:offset+len(publicNameBytes)], publicNameBytes)
	offset += len(publicNameBytes)
	binary.BigEndian.PutUint16(out[offset:offset+2], 0)
	return out
}

func startECHTestDNSServers(t *testing.T, handler dns.Handler) string {
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

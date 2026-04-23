// See LICENSE file in the project root for license information.

package ech

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
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

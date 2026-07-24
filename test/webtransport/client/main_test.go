// See LICENSE file in the project root for license information.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
)

func TestWebTransportTLSConfigClonesResolvedPolicy(t *testing.T) {
	roots := x509.NewCertPool()
	base := &tls.Config{
		RootCAs:            roots,
		ServerName:         "engine.example.test",
		InsecureSkipVerify: true,
		NextProtos:         []string{"rstrm/1"},
	}
	got := webTransportTLSConfig(base)
	if got == base {
		t.Fatal("webTransportTLSConfig() returned the input config")
	}
	if got.RootCAs != roots || got.ServerName != base.ServerName || got.InsecureSkipVerify != base.InsecureSkipVerify {
		t.Fatalf("webTransportTLSConfig() = %#v, want resolved TLS policy", got)
	}
	if len(got.NextProtos) != 1 || got.NextProtos[0] != "h3" {
		t.Fatalf("NextProtos = %#v, want [h3]", got.NextProtos)
	}
	if len(base.NextProtos) != 1 || base.NextProtos[0] != "rstrm/1" {
		t.Fatalf("input NextProtos mutated: %#v", base.NextProtos)
	}
}

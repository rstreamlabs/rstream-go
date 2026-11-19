// See LICENSE file in the project root for license information.

package doctor_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/doctor"
)

func TestRunNetworkDiagnosticsUseConfiguredDNS(t *testing.T) {
	resolver := startDoctorDNS(t)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.TLS = &tls.Config{NextProtos: []string{"rstrm/1", "http/1.1"}}
	server.StartTLS()
	t.Cleanup(server.Close)
	listener, err := quic.ListenAddr(server.Listener.Addr().String(), server.TLS.Clone(), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept(t.Context())
			if err != nil {
				return
			}
			<-connection.Context().Done()
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"tls", "quic", "auto", "auto-tls-fallback", "no-addresses", "dnssec-required"} {
		t.Run(mode, func(t *testing.T) {
			tcp := &rstream.Transport{DNSOverride: rstream.StringPtr(resolver), ForceIPv4: rstream.BoolPtr(true)}
			udp := &rstream.QUICTransport{DNSOverride: rstream.StringPtr(resolver), ForceIPv4: rstream.BoolPtr(true)}
			defer udp.Close()
			var transport rstream.Dialer = tcp
			host := "engine.doctor.test"
			switch mode {
			case "quic":
				transport = udp
			case "auto", "auto-tls-fallback":
				auto := &rstream.AutoTransport{TLS: tcp, QUIC: udp}
				defer auto.Close()
				transport = auto
				if mode == "auto-tls-fallback" {
					udp.ForceIPv4, udp.ForceIPv6 = nil, rstream.BoolPtr(true)
				}
			case "no-addresses":
				host = "missing.doctor.test"
			case "dnssec-required":
				tcp.DNSSECEnabled = rstream.BoolPtr(true)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			resolved := config.Resolved{Engine: net.JoinHostPort(host, port), Context: &config.Context{Name: "local"}, Transport: transport, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}}
			report, err := doctor.Run(ctx, resolved, doctor.Options{})
			check := checksByName(report)["dns"]
			if mode == "no-addresses" || mode == "dnssec-required" {
				if !errors.Is(err, doctor.ErrChecksFailed) || check.Status != doctor.StatusFail {
					t.Fatalf("DNS failure was hidden: %+v, %v", report, err)
				}
				if mode == "dnssec-required" && !strings.Contains(check.Message, "DNSSEC") {
					t.Fatalf("DNSSEC policy was lost: %+v", check)
				}
				return
			}
			if !errors.Is(err, doctor.ErrChecksFailed) || report.Summary.Fail != 1 || checksByName(report)["token"].Status != doctor.StatusFail || check.Status != doctor.StatusPass || check.Details["addresses"] != "127.0.0.1" {
				t.Fatalf("configured resolver: %+v, %v", report, err)
			}
			if mode == "auto-tls-fallback" && checksByName(report)["tunnel_transport"].Details["selectedMode"] != "tls" {
				t.Fatalf("TLS fallback not exercised: %+v", report)
			}
		})
	}
}

func startDoctorDNS(t *testing.T) string {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan error, 1)
	server := &dns.Server{PacketConn: packet, NotifyStartedFunc: func() { close(started) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, query *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(query)
		for _, question := range query.Question {
			if question.Name != "engine.doctor.test." {
				reply.Rcode = dns.RcodeNameError
			} else if question.Qtype == dns.TypeA {
				reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("127.0.0.1")})
			}
		}
		if err := w.WriteMsg(reply); err != nil {
			t.Error(err)
		}
	})}
	go func() { stopped <- server.ActivateAndServe() }()
	select {
	case <-started:
	case err := <-stopped:
		_ = packet.Close()
		t.Fatalf("DNS server failed to start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.ShutdownContext(ctx); err != nil {
			t.Error(err)
		}
		if err := <-stopped; err != nil {
			t.Error(err)
		}
	})
	return packet.LocalAddr().String()
}

func TestRunNetworkDiagnosticsRejectMismatchedLiteralFamily(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			transport := &rstream.Transport{ForceIPv4: rstream.BoolPtr(host == "::1"), ForceIPv6: rstream.BoolPtr(host == "127.0.0.1")}
			resolved := config.Resolved{Engine: net.JoinHostPort(host, "1"), Context: &config.Context{Name: "local"}, Transport: transport}
			report, err := doctor.Run(t.Context(), resolved, doctor.Options{})
			check := checksByName(report)["dns"]
			if !errors.Is(err, doctor.ErrChecksFailed) || check.Status != doctor.StatusFail || !strings.Contains(check.Message, "configured IP family") {
				t.Fatalf("literal family restriction was ignored: %+v %v", report, err)
			}
		})
	}
}

// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	enginetls "github.com/rstreamlabs/rstream-go/internal/ech"
)

func TestDialWithECHFallsBackToPlainTLSOnRejection(t *testing.T) {
	lookup := lookupECHConfigList
	lookupECHConfigList = func(context.Context, enginetls.Target, enginetls.ResolverOptions) ([]byte, error) {
		return []byte{0x00, 0x00}, nil
	}
	defer func() { lookupECHConfigList = lookup }()
	transport := &recordingDialer{
		dial: func(_ context.Context, _ string, cfg *tls.Config) (net.Conn, error) {
			if len(cfg.EncryptedClientHelloConfigList) > 0 {
				return nil, &tls.ECHRejectionError{}
			}
			return nil, errors.New("unexpected plain fallback")
		},
	}
	conn, err := dialWithECH(context.Background(), transport, "f587ee53.c.localhost.rstream.io:443", &tls.Config{
		ServerName: "f587ee53.c.localhost.rstream.io",
		NextProtos: []string{"rstrm/1"},
	})
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected ECH rejection")
	}
	if len(transport.calls) != 1 {
		t.Fatalf("expected 1 dial attempt, got %d", len(transport.calls))
	}
	if len(transport.calls[0].EncryptedClientHelloConfigList) == 0 {
		t.Fatalf("expected first attempt to use ECH")
	}
	if transport.calls[0].MinVersion != tls.VersionTLS13 {
		t.Fatalf("expected first attempt MinVersion TLS1.3, got %v", transport.calls[0].MinVersion)
	}
}

func TestDialWithECHRetriesSuggestedConfig(t *testing.T) {
	lookup := lookupECHConfigList
	lookupECHConfigList = func(context.Context, enginetls.Target, enginetls.ResolverOptions) ([]byte, error) {
		return []byte{0x00, 0x01}, nil
	}
	defer func() { lookupECHConfigList = lookup }()
	transport := &recordingDialer{
		dial: func(_ context.Context, _ string, cfg *tls.Config) (net.Conn, error) {
			switch string(cfg.EncryptedClientHelloConfigList) {
			case string([]byte{0x00, 0x01}):
				return nil, &tls.ECHRejectionError{RetryConfigList: []byte{0x00, 0x02}}
			case string([]byte{0x00, 0x02}):
				return stubConn{}, nil
			default:
				return nil, errors.New("unexpected plain fallback")
			}
		},
	}
	conn, err := dialWithECH(context.Background(), transport, "f587ee53.c.localhost.rstream.io:443", &tls.Config{
		ServerName: "f587ee53.c.localhost.rstream.io",
		NextProtos: []string{"rstrm/1"},
	})
	if err != nil {
		t.Fatalf("dialWithECH() error = %v", err)
	}
	_ = conn.Close()
	if len(transport.calls) != 2 {
		t.Fatalf("expected 2 dial attempts, got %d", len(transport.calls))
	}
	if got := transport.calls[1].EncryptedClientHelloConfigList; string(got) != string([]byte{0x00, 0x02}) {
		t.Fatalf("expected retry config list, got %x", got)
	}
}

func TestDialWithECHSkipsLookupWhenUserConfiguresECH(t *testing.T) {
	lookup := lookupECHConfigList
	lookupECHConfigList = func(context.Context, enginetls.Target, enginetls.ResolverOptions) ([]byte, error) {
		t.Fatal("unexpected ECH lookup")
		return nil, nil
	}
	defer func() { lookupECHConfigList = lookup }()
	transport := &recordingDialer{
		dial: func(_ context.Context, _ string, cfg *tls.Config) (net.Conn, error) {
			return stubConn{}, nil
		},
	}
	conn, err := dialWithECH(context.Background(), transport, "f587ee53.c.localhost.rstream.io:443", &tls.Config{
		ServerName:                     "f587ee53.c.localhost.rstream.io",
		NextProtos:                     []string{"rstrm/1"},
		EncryptedClientHelloConfigList: []byte{0x01},
	})
	if err != nil {
		t.Fatalf("dialWithECH() error = %v", err)
	}
	_ = conn.Close()
	if len(transport.calls) != 1 {
		t.Fatalf("expected single dial, got %d", len(transport.calls))
	}
}

func TestDialWithECHFallsBackToPlainTLSWhenLookupFails(t *testing.T) {
	lookup := lookupECHConfigList
	lookupECHConfigList = func(context.Context, enginetls.Target, enginetls.ResolverOptions) ([]byte, error) {
		return nil, errors.New("dns unavailable")
	}
	defer func() { lookupECHConfigList = lookup }()
	transport := &recordingDialer{
		dial: func(_ context.Context, _ string, cfg *tls.Config) (net.Conn, error) {
			if len(cfg.EncryptedClientHelloConfigList) != 0 {
				return nil, errors.New("unexpected ech")
			}
			return stubConn{}, nil
		},
	}
	conn, err := dialWithECH(context.Background(), transport, "f587ee53.c.localhost.rstream.io:443", &tls.Config{
		ServerName: "f587ee53.c.localhost.rstream.io",
		NextProtos: []string{"rstrm/1"},
	})
	if err != nil {
		t.Fatalf("dialWithECH() error = %v", err)
	}
	_ = conn.Close()
	if len(transport.calls) != 1 {
		t.Fatalf("expected single dial, got %d", len(transport.calls))
	}
	if len(transport.calls[0].EncryptedClientHelloConfigList) != 0 {
		t.Fatalf("expected plain TLS fallback")
	}
}

type recordingDialer struct {
	calls []*tls.Config
	dial  func(context.Context, string, *tls.Config) (net.Conn, error)
}

func (d *recordingDialer) Dial(ctx context.Context, addr string, cfg *tls.Config) (net.Conn, error) {
	if cfg == nil {
		d.calls = append(d.calls, nil)
	} else {
		d.calls = append(d.calls, cfg.Clone())
	}
	return d.dial(ctx, addr, cfg)
}

type stubConn struct{}

func (stubConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (stubConn) Write(p []byte) (int, error)      { return len(p), nil }
func (stubConn) Close() error                     { return nil }
func (stubConn) LocalAddr() net.Addr              { return stubAddr("local") }
func (stubConn) RemoteAddr() net.Addr             { return stubAddr("remote") }
func (stubConn) SetDeadline(time.Time) error      { return nil }
func (stubConn) SetReadDeadline(time.Time) error  { return nil }
func (stubConn) SetWriteDeadline(time.Time) error { return nil }

type stubAddr string

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return string(a) }

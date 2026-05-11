// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDatagramChannelIDFromStreamID(t *testing.T) {
	tests := map[string]string{
		"01020304abcd000000000001":             "01020304abcd000000000001",
		"ABCDEF010203040506070809":             "abcdef010203040506070809",
		"01020304-0000-0000-0000-000000000000": "010203040000000000000000",
	}
	for input, want := range tests {
		got, err := datagramChannelIDFromStreamID(input)
		if err != nil {
			t.Fatalf("datagramChannelIDFromStreamID(%q) error = %v", input, err)
		}
		if got.String() != want {
			t.Fatalf("datagramChannelIDFromStreamID(%q) = %s, want %s", input, got.String(), want)
		}
	}
	for _, input := range []string{"short", "zzzzzzzz0000000000000000"} {
		if _, err := datagramChannelIDFromStreamID(input); err == nil {
			t.Fatalf("datagramChannelIDFromStreamID(%q) error = nil, want validation error", input)
		}
	}
}

func TestQUICDatagramChannelWritePrefixesChannelID(t *testing.T) {
	provider := &recordingDatagramProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel := &quicDatagramChannel{
		channelID: mustDatagramChannelID(t, "01020304abcd000000000001"),
		provider:  provider,
		laddr:     stubNetAddr("local"),
		raddr:     stubNetAddr("remote"),
		recvCh:    make(chan []byte, 1),
		ctx:       ctx,
		cancel:    cancel,
	}
	n, err := channel.WriteTo([]byte("payload"), stubNetAddr("remote"))
	if err != nil || n != len("payload") {
		t.Fatalf("WriteTo() = %d, %v", n, err)
	}
	if len(provider.sent) != 1 {
		t.Fatalf("expected one datagram, got %d", len(provider.sent))
	}
	got := provider.sent[0]
	if string(got[:datagramChannelIDSize]) != string(channel.channelID[:]) || string(got[datagramChannelIDSize:]) != "payload" {
		t.Fatalf("unexpected datagram frame: %#v", got)
	}
	provider.err = errors.New("send failed")
	if _, err := channel.WriteTo([]byte("payload"), stubNetAddr("remote")); err == nil {
		t.Fatalf("expected provider error")
	}
}

func mustDatagramChannelID(t *testing.T, streamID string) datagramChannelID {
	t.Helper()
	id, err := datagramChannelIDFromStreamID(streamID)
	if err != nil {
		t.Fatalf("datagramChannelIDFromStreamID(%q) error = %v", streamID, err)
	}
	return id
}

func TestQUICDatagramChannelReadAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	channel := &quicDatagramChannel{
		provider: &recordingDatagramProvider{},
		laddr:    stubNetAddr("local"),
		raddr:    stubNetAddr("remote"),
		recvCh:   make(chan []byte, 1),
		ctx:      ctx,
		cancel:   cancel,
	}
	channel.recvCh <- []byte("packet")
	buf := make([]byte, 16)
	n, addr, err := channel.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "packet" || addr != stubNetAddr("local") {
		t.Fatalf("ReadFrom() = %q from %v", buf[:n], addr)
	}
	if channel.LocalAddr() != stubNetAddr("local") {
		t.Fatalf("LocalAddr() = %v, want local", channel.LocalAddr())
	}
	if err := channel.SetDeadline(time.Now()); !errors.Is(err, errDatagramDeadlineUnsupported) {
		t.Fatalf("SetDeadline() error = %v, want errDatagramDeadlineUnsupported", err)
	}
	if err := channel.SetReadDeadline(time.Now()); !errors.Is(err, errDatagramDeadlineUnsupported) {
		t.Fatalf("SetReadDeadline() error = %v, want errDatagramDeadlineUnsupported", err)
	}
	if err := channel.SetWriteDeadline(time.Now()); !errors.Is(err, errDatagramDeadlineUnsupported) {
		t.Fatalf("SetWriteDeadline() error = %v, want errDatagramDeadlineUnsupported", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := channel.ReadFrom(buf); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadFrom() after close error = %v, want net.ErrClosed", err)
	}
}

func TestQUICTransportErrorsBeforeConnection(t *testing.T) {
	transport := &QUICTransport{}
	if err := transport.SendDatagram([]byte("payload")); err == nil || !strings.Contains(err.Error(), "not established") {
		t.Fatalf("SendDatagram() error = %v, want not established", err)
	}
	if _, err := transport.ReceiveDatagram(t.Context()); err == nil || !strings.Contains(err.Error(), "not established") {
		t.Fatalf("ReceiveDatagram() error = %v, want not established", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() without connection error = %v", err)
	}
	localAddr := "not-an-ip"
	transport.LocalAddr = &localAddr
	if _, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{}); err == nil || !strings.Contains(err.Error(), "failed to parse local address") {
		t.Fatalf("Dial() error = %v, want local address parse error", err)
	}
}

func TestQUICTransportCloseInvalidatesInFlightConnectGeneration(t *testing.T) {
	transport := &QUICTransport{}
	before := transport.closeGeneration
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() without connection error = %v", err)
	}
	if transport.closeGeneration != before+1 {
		t.Fatalf("Close() should invalidate in-flight QUIC connection attempts, generation=%d before=%d", transport.closeGeneration, before)
	}
	localAddr := "not-an-ip"
	transport.LocalAddr = &localAddr
	if _, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{}); err == nil || !strings.Contains(err.Error(), "failed to parse local address") {
		t.Fatalf("Dial() after idle Close() error = %v, want local address validation", err)
	}
}

func TestQUICTransportOriginBindsAddrAndTLSIdentity(t *testing.T) {
	origin, err := quicTransportOrigin("Example.COM:443", &tls.Config{
		ServerName: "Tunnel.Example.COM",
		NextProtos: []string{
			"h3",
			"rstream",
		},
	})
	if err != nil {
		t.Fatalf("quicTransportOrigin() error = %v", err)
	}
	if origin != "example.com:443|tunnel.example.com|h3,rstream" {
		t.Fatalf("unexpected origin: %q", origin)
	}
	other, err := quicTransportOrigin("example.com:443", &tls.Config{ServerName: "other.example.com"})
	if err != nil {
		t.Fatalf("quicTransportOrigin() error = %v", err)
	}
	if other == origin {
		t.Fatalf("different TLS identities should not share QUIC origin")
	}
}

func TestQUICDatagramListenerAcceptAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listener := &quicDatagramListener{
		conns:  make(chan net.PacketConn, 1),
		ctx:    ctx,
		cancel: cancel,
		laddr:  stubNetAddr("listener"),
	}
	conn := newFakePacketConn(stubNetAddr("conn"))
	listener.conns <- conn
	got, addr, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got != conn || addr != conn.LocalAddr() {
		t.Fatalf("Accept() = %v, %v", got, addr)
	}
	if listener.Addr() != stubNetAddr("listener") {
		t.Fatalf("Addr() = %v", listener.Addr())
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() after close error = %v, want net.ErrClosed", err)
	}
}

type recordingDatagramProvider struct {
	sent [][]byte
	err  error
}

func (p *recordingDatagramProvider) SendDatagram(data []byte) error {
	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, append([]byte(nil), data...))
	return nil
}

func (p *recordingDatagramProvider) ReceiveDatagram(context.Context) ([]byte, error) {
	return nil, errors.New("not implemented")
}

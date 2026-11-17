// See LICENSE file in the project root for license information.

package rstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func quicClosePair(t testing.TB) (net.Conn, *quic.Conn, *QUICTransport) {
	t.Helper()
	listener, err := quic.ListenAddr("127.0.0.1:0", testQUICServerTLSConfig(t, testQUICALPN), &quic.Config{InitialStreamReceiveWindow: 32 << 10, MaxStreamReceiveWindow: 32 << 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	accepted := make(chan *quic.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	transport := &QUICTransport{}
	t.Cleanup(func() { _ = transport.Close() })
	conn, err := transport.Dial(ctx, listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case peer := <-accepted:
		t.Cleanup(func() { _ = peer.CloseWithError(0, "test complete") })
		return conn, peer, transport
	case err := <-acceptErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return nil, nil, nil
}

func TestQUICTransportCloseUnblocksReadAndFlowControlledWrite(t *testing.T) {
	conn, peer, transport := quicClosePair(t)
	underlying := transport.quicConn
	written := make(chan error, 1)
	read := make(chan error, 1)
	go func() { _, err := conn.Write(make([]byte, 1<<20)); written <- err }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := peer.AcceptStream(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _, err := conn.Read(make([]byte, 1)); read <- err }()
	select {
	case err := <-written:
		t.Fatalf("write did not encounter the peer's bounded receive window: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	var closers sync.WaitGroup
	deadlineDone := make(chan error, 1)
	go func() {
		for {
			if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
				deadlineDone <- err
				return
			}
			if err := conn.SetWriteDeadline(time.Time{}); err != nil {
				deadlineDone <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	for range 4 {
		closers.Go(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		})
	}
	for name, done := range map[string]<-chan error{"read": read, "write": written} {
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s succeeded after full close", name)
			}
		case <-time.After(500 * time.Millisecond):
			_ = transport.Close()
			t.Errorf("%s remained blocked after full close", name)
		}
	}
	closers.Wait()
	select {
	case err := <-deadlineDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("deadline after close: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("deadline updates reopened the closed writer")
	}
	if _, err := conn.Write([]byte("closed")); err == nil {
		t.Fatal("write accepted after full close")
	}
	if transport.quicConn != underlying || underlying.Context().Err() != nil {
		t.Fatal("stream close invalidated the multiplexed connection")
	}
}

func TestQUICTransportClosePreservesPartialWrite(t *testing.T) {
	conn, peer, _ := quicClosePair(t)
	payload := bytes.Repeat([]byte{0x5a}, 1<<20)
	type writeResult struct {
		n   int
		err error
	}
	written := make(chan writeResult, 1)
	go func() { n, err := conn.Write(payload); written <- writeResult{n, err} }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, err := peer.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-written:
		t.Fatalf("write did not block: %+v", result)
	case <-time.After(30 * time.Millisecond):
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var result writeResult
	select {
	case result = <-written:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("partial writer did not stop")
	}
	if result.err == nil || result.n <= 0 || result.n >= len(payload) {
		t.Fatalf("expected a partially accepted write: %+v", result)
	}
	if err := stream.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, payload[:result.n]) {
		t.Fatalf("partial close lost accepted bytes: received=%d accepted=%d error=%v", len(got), result.n, err)
	}
}

func TestQUICTransportHalfCloseUnblocksConcurrentWriters(t *testing.T) {
	conn, peer, _ := quicClosePair(t)
	defer conn.Close()
	done := make(chan error, 4)
	for range cap(done) {
		go func() { _, err := conn.Write(make([]byte, 64<<10)); done <- err }()
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := peer.AcceptStream(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("writer bypassed the bounded receive window: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	closed := make(chan error, 1)
	go func() { closed <- conn.(interface{ CloseWrite() error }).CloseWrite() }()
	for range cap(done) {
		select {
		case err := <-done:
			if err == nil {
				t.Error("blocked writer succeeded after half close")
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("half close leaked a blocked writer")
		}
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("half close did not stop")
	}
}

func TestQUICTransportClosePreservesAcceptedBytes(t *testing.T) {
	conn, peer, _ := quicClosePair(t)
	payload := bytes.Repeat([]byte("payload"), 4096)
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, err := peer.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("close lost accepted payload: bytes=%d error=%v", len(got), err)
	}
}

func TestQUICTransportHalfClosePreservesResponse(t *testing.T) {
	conn, peer, _ := quicClosePair(t)
	defer conn.Close()
	if _, err := conn.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, err := peer.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	request, err := io.ReadAll(stream)
	if err != nil || string(request) != "request" {
		t.Fatalf("request=%q error=%v", request, err)
	}
	if _, err := stream.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(conn)
	if err != nil || string(response) != "response" {
		t.Fatalf("response=%q error=%v", response, err)
	}
}

func BenchmarkQUICTransportStreamRoundTrip(b *testing.B) {
	conn, peer, transport := quicClosePair(b)
	ctx, cancel := context.WithCancel(b.Context())
	done := make(chan error, 1)
	go func() {
		stream, err := peer.AcceptStream(ctx)
		if err != nil {
			done <- err
			return
		}
		_, err = io.Copy(stream, stream)
		done <- err
	}()
	defer func() {
		_ = conn.Close()
		_ = transport.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			b.Error("benchmark echo worker did not stop")
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		b.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0x5a}, 32<<10)
	reply := make([]byte, len(payload))
	b.SetBytes(2 * int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := conn.Write(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(conn, reply); err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(reply, payload) {
			b.Fatal("benchmark echo payload mismatch")
		}
	}
	b.StopTimer()
}

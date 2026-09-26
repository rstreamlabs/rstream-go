// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type closeTrackingConn struct {
	mu            sync.Mutex
	closeCalls    int
	deadlineCalls int
}

func (c *closeTrackingConn) Read([]byte) (int, error)        { return 0, io.EOF }
func (c *closeTrackingConn) Write(p []byte) (int, error)     { return len(p), nil }
func (c *closeTrackingConn) LocalAddr() net.Addr             { return nil }
func (c *closeTrackingConn) RemoteAddr() net.Addr            { return nil }
func (c *closeTrackingConn) SetDeadline(time.Time) error     { return nil }
func (c *closeTrackingConn) SetReadDeadline(time.Time) error { return nil }

func (c *closeTrackingConn) SetWriteDeadline(time.Time) error {
	c.mu.Lock()
	c.deadlineCalls++
	c.mu.Unlock()
	return nil
}

func (c *closeTrackingConn) Close() error {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
	return nil
}

func (c *closeTrackingConn) calls() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls, c.deadlineCalls
}

type closeWriteTrackingConn struct {
	*closeTrackingConn
	mu              sync.Mutex
	closeWriteCalls int
}

func (c *closeWriteTrackingConn) CloseWrite() error {
	c.mu.Lock()
	c.closeWriteCalls++
	c.mu.Unlock()
	return nil
}

func (c *closeWriteTrackingConn) writeCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeWriteCalls
}

// bufferedCloseWriteConn models a tunnel that accepts terminal writes before
// delivering them. Closing either side early discards the pending records.
type bufferedCloseWriteConn struct {
	net.Conn
	mu                sync.Mutex
	pending           bytes.Buffer
	closed            bool
	directWrites      int
	bufferedWrites    int
	pendingWriteCount int
	pendingReady      chan struct{}
	prematureClose    chan struct{}
	readyOnce         sync.Once
	closeOnce         sync.Once
}

func (c *bufferedCloseWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	if c.directWrites > 0 {
		c.directWrites--
		c.mu.Unlock()
		return c.Conn.Write(p)
	}
	n, err := c.pending.Write(p)
	c.bufferedWrites++
	if c.bufferedWrites == c.pendingWriteCount {
		c.readyOnce.Do(func() { close(c.pendingReady) })
	}
	c.mu.Unlock()
	return n, err
}

func (c *bufferedCloseWriteConn) CloseWrite() error {
	c.discardPending()
	return nil
}

func (c *bufferedCloseWriteConn) discardPending() {
	c.mu.Lock()
	c.closed = true
	c.pending.Reset()
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.prematureClose) })
}

func (c *bufferedCloseWriteConn) flushPending() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	payload := append([]byte(nil), c.pending.Bytes()...)
	c.pending.Reset()
	c.mu.Unlock()
	for len(payload) > 0 {
		n, err := c.Conn.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}

func (c *bufferedCloseWriteConn) Close() error {
	c.discardPending()
	return c.Conn.Close()
}

func TestPlainMessageConnFramesPayloads(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	clientConn := newPlainMessageConn(client)
	serverConn := newPlainMessageConn(server)
	errCh := make(chan error, 1)
	go func() {
		errCh <- clientConn.WriteMessage(websocket.BinaryMessage, []byte("payload"))
	}()
	messageType, payload, err := serverConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if messageType != websocket.BinaryMessage || !bytes.Equal(payload, []byte("payload")) {
		t.Fatalf("ReadMessage() = type %d payload %q", messageType, string(payload))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
}

func TestPlainMessageConnReadLimit(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	clientConn := newPlainMessageConn(client)
	serverConn := newPlainMessageConn(server)
	serverConn.SetReadLimit(3)
	errCh := make(chan error, 1)
	go func() {
		errCh <- clientConn.WriteMessage(websocket.BinaryMessage, []byte("toolong"))
	}()
	if _, _, err := serverConn.ReadMessage(); err == nil {
		t.Fatalf("ReadMessage() expected read limit error")
	}
	_ = serverConn.Close()
	<-errCh
}

func TestPlainMessageConnCloseControlKeepsTransportOpen(t *testing.T) {
	transport := &closeWriteTrackingConn{closeTrackingConn: &closeTrackingConn{}}
	conn := newPlainMessageConn(transport)
	if err := conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl() error = %v", err)
	}
	closeCalls, deadlineCalls := transport.calls()
	if transport.writeCalls() != 0 || closeCalls != 0 || deadlineCalls != 0 {
		t.Fatalf("close control calls = CloseWrite %d, Close %d, deadline %d; want 0, 0, 0", transport.writeCalls(), closeCalls, deadlineCalls)
	}
}

func TestPlainMessageConnCloseOwnsFinalTransportClose(t *testing.T) {
	transport := &closeTrackingConn{}
	conn := newPlainMessageConn(transport)
	if err := conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl() error = %v", err)
	}
	closeCalls, deadlineCalls := transport.calls()
	if closeCalls != 0 || deadlineCalls != 0 {
		t.Fatalf("close control calls = Close %d, deadline %d; want 0, 0", closeCalls, deadlineCalls)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closeCalls, _ = transport.calls()
	if closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", closeCalls)
	}
}

func TestPlainMessageConnCloseControlPreservesPeerResponse(t *testing.T) {
	client, rawServer := net.Pipe()
	defer client.Close()
	defer rawServer.Close()
	clientConn := newPlainMessageConn(client)
	serverConn := newPlainMessageConn(rawServer)
	writeDone := make(chan error, 1)
	go func() {
		if err := serverConn.WriteMessage(websocket.BinaryMessage, []byte("final")); err != nil {
			writeDone <- err
			return
		}
		writeDone <- serverConn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second))
	}()
	_, payload, err := clientConn.ReadMessage()
	if err != nil || string(payload) != "final" {
		t.Fatalf("final message = %q, %v", payload, err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("close control error = %v", err)
	}
	ackDone := make(chan error, 1)
	go func() { ackDone <- clientConn.WriteMessage(websocket.BinaryMessage, []byte("ack")) }()
	_, payload, err = serverConn.ReadMessage()
	if err != nil || string(payload) != "ack" {
		t.Fatalf("peer response after close control = %q, %v", payload, err)
	}
	if err := <-ackDone; err != nil {
		t.Fatalf("peer response write error = %v", err)
	}
}

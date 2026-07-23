// See LICENSE file in the project root for license information.

package main

import (
	"net"
	"testing"
	"time"
)

type retryPacketConn struct {
	deadline time.Time
	echo     chan []byte
	writes   int
}

func (c *retryPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	timer := time.NewTimer(time.Until(c.deadline))
	defer timer.Stop()
	select {
	case payload := <-c.echo:
		return copy(p, payload), nil, nil
	case <-timer.C:
		return 0, nil, retryTimeoutError{}
	}
}

func (c *retryPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	c.writes++
	if c.writes == 2 {
		c.echo <- append([]byte(nil), p...)
	}
	return len(p), nil
}

func (c *retryPacketConn) Close() error {
	return nil
}

func (c *retryPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{}
}

func (c *retryPacketConn) SetDeadline(deadline time.Time) error {
	c.deadline = deadline
	return nil
}

func (c *retryPacketConn) SetReadDeadline(deadline time.Time) error {
	c.deadline = deadline
	return nil
}

func (c *retryPacketConn) SetWriteDeadline(time.Time) error {
	return nil
}

type retryTimeoutError struct{}

func (retryTimeoutError) Error() string {
	return "timeout"
}

func (retryTimeoutError) Timeout() bool {
	return true
}

func (retryTimeoutError) Temporary() bool {
	return true
}

func TestExchangeConnectUDPPayloadRetriesPacketLoss(t *testing.T) {
	conn := &retryPacketConn{echo: make(chan []byte, 1)}
	if err := exchangeConnectUDPPayload(conn, []byte("payload"), time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if conn.writes != 2 {
		t.Fatalf("expected one retransmission, got %d writes", conn.writes)
	}
}

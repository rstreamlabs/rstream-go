// See LICENSE file in the project root for license information.

package main

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type echoReplyConn struct {
	reply      string
	fragment   int
	read       int
	shortWrite bool
	finalEOF   bool
	closed     bool
}

func (c *echoReplyConn) Read(p []byte) (int, error) {
	if c.read == len(c.reply) {
		return 0, io.EOF
	}
	if c.fragment > 0 && len(p) > c.fragment {
		p = p[:c.fragment]
	}
	n := copy(p, c.reply[c.read:])
	c.read += n
	if c.finalEOF && c.read == len(c.reply) {
		return n, io.EOF
	}
	return n, nil
}

func (c *echoReplyConn) Write(p []byte) (int, error) {
	if c.shortWrite {
		return len(p) - 1, nil
	}
	return len(p), nil
}

func (c *echoReplyConn) Close() error                     { c.closed = true; return nil }
func (c *echoReplyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *echoReplyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *echoReplyConn) SetDeadline(time.Time) error      { return nil }
func (c *echoReplyConn) SetReadDeadline(time.Time) error  { return nil }
func (c *echoReplyConn) SetWriteDeadline(time.Time) error { return nil }

func TestHandleConnectionReadsCompleteEcho(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fragment int
		finalEOF bool
	}{
		{name: "fragmented", fragment: 3},
		{name: "final bytes with EOF", finalEOF: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &echoReplyConn{reply: "Hello from rstream-go!", fragment: tc.fragment, finalEOF: tc.finalEOF}
			if err := handleConnection(conn); err != nil {
				t.Fatal(err)
			}
			if conn.read != len(conn.reply) || !conn.closed {
				t.Fatalf("read=%d of %d, closed=%v", conn.read, len(conn.reply), conn.closed)
			}
		})
	}
}

func TestHandleConnectionRejectsIncompleteExchange(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn echoReplyConn
		want error
	}{
		{name: "short write", conn: echoReplyConn{shortWrite: true}, want: io.ErrShortWrite},
		{name: "truncated echo", conn: echoReplyConn{reply: "Hello"}, want: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := handleConnection(&tc.conn)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if !tc.conn.closed {
				t.Fatal("connection was not closed")
			}
		})
	}
}

func TestHandleConnectionRejectsIncorrectEcho(t *testing.T) {
	conn := &echoReplyConn{reply: strings.Repeat("x", len("Hello from rstream-go!"))}
	if err := handleConnection(conn); err == nil {
		t.Fatal("accepted a payload different from the sent message")
	}
}

// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type echoConn struct {
	reader  io.Reader
	written bytes.Buffer
	short   bool
	reads   int
	writes  int
	closed  bool
}

func (c *echoConn) Read(p []byte) (int, error) {
	c.reads++
	return c.reader.Read(p)
}

func (c *echoConn) Write(p []byte) (int, error) {
	c.writes++
	if c.short {
		return c.written.Write(p[:len(p)-1])
	}
	return c.written.Write(p)
}

func (c *echoConn) Close() error                     { c.closed = true; return nil }
func (c *echoConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *echoConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *echoConn) SetDeadline(time.Time) error      { return nil }
func (c *echoConn) SetReadDeadline(time.Time) error  { return nil }
func (c *echoConn) SetWriteDeadline(time.Time) error { return nil }

type finalReader struct {
	data []byte
	err  error
}

func (r *finalReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestHandleConnectionPreservesFinalBytes(t *testing.T) {
	for _, terminal := range []error{io.EOF, errors.New("read failed")} {
		t.Run(terminal.Error(), func(t *testing.T) {
			conn := &echoConn{reader: &finalReader{data: []byte("final bytes"), err: terminal}}
			handleConnection(conn)
			if got := conn.written.String(); got != "final bytes" {
				t.Fatalf("echo = %q, want final bytes", got)
			}
			if !conn.closed {
				t.Fatal("connection was not closed")
			}
		})
	}
}

func TestHandleConnectionStopsAfterShortWrite(t *testing.T) {
	conn := &echoConn{reader: bytes.NewReader(bytes.Repeat([]byte("x"), 65536)), short: true}
	handleConnection(conn)
	if conn.reads != 1 || conn.writes != 1 || !conn.closed {
		t.Fatalf("after short write: reads=%d writes=%d closed=%v", conn.reads, conn.writes, conn.closed)
	}
}

func TestHandleConnectionEchoesMultipleBuffers(t *testing.T) {
	input := bytes.Repeat([]byte("payload"), 20000)
	conn := &echoConn{reader: bytes.NewReader(input)}
	handleConnection(conn)
	if !bytes.Equal(conn.written.Bytes(), input) || !conn.closed {
		t.Fatalf("echoed %d of %d bytes, closed=%v", conn.written.Len(), len(input), conn.closed)
	}
}

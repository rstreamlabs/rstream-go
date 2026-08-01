// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"net"
	"testing"
)

type closeWriteRecorder struct {
	net.Conn
	err    error
	called bool
}

func (c *closeWriteRecorder) CloseWrite() error {
	c.called = true
	return c.err
}

func TestBytestreamConnCloseWriteDelegates(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	wantErr := errors.New("close write")
	underlying := &closeWriteRecorder{Conn: left, err: wantErr}
	conn := &bytestreamConn{conn: underlying}
	if err := conn.CloseWrite(); !errors.Is(err, wantErr) {
		t.Fatalf("CloseWrite() error = %v, want %v", err, wantErr)
	}
	if !underlying.called {
		t.Fatal("CloseWrite() was not delegated")
	}
}

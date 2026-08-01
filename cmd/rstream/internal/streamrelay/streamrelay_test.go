// See LICENSE file in the project root for license information.

package streamrelay

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestBidirectionalPreservesTCPHalfClose(t *testing.T) {
	client, left := tcpPair(t)
	right, server := tcpPair(t)
	defer client.Close()
	defer left.Close()
	defer right.Close()
	defer server.Close()
	deadline := time.Now().Add(time.Second)
	for _, conn := range []net.Conn{client, left, right, server} {
		if err := conn.SetDeadline(deadline); err != nil {
			t.Fatalf("SetDeadline() error = %v", err)
		}
	}
	done := make(chan struct{})
	go func() {
		Bidirectional(left, right)
		close(done)
	}()
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("client CloseWrite() error = %v", err)
	}
	request, err := io.ReadAll(server)
	if err != nil {
		t.Fatalf("server ReadAll() error = %v", err)
	}
	if string(request) != "request" {
		t.Fatalf("server request = %q, want request", request)
	}
	if _, err := server.Write([]byte("response")); err != nil {
		t.Fatalf("server Write() error = %v", err)
	}
	if err := server.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("server CloseWrite() error = %v", err)
	}
	response, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("client ReadAll() error = %v", err)
	}
	if string(response) != "response" {
		t.Fatalf("client response = %q, want response", response)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Bidirectional() did not return")
	}
}

func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatalf("Dial() error = %v", err)
	}
	defer listener.Close()
	select {
	case conn := <-accepted:
		return client, conn
	case err := <-acceptErr:
		_ = client.Close()
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(time.Second):
		_ = client.Close()
		t.Fatal("timed out waiting for Accept()")
	}
	return nil, nil
}

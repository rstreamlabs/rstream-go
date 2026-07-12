// See LICENSE file in the project root for license information.

//go:build !windows

package cmd

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestRunNetcatExecSessionReturnsWhenChildExits(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runNetcatExecSession(context.Background(), client, &netcatExecConfig{Shell: true, Command: "printf test-output"}, false, slog.Default())
	}()
	var buf bytes.Buffer
	copyDoneCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, server)
		copyDoneCh <- err
	}()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("exec session did not exit after child completion")
	}
	_ = server.Close()
	select {
	case err := <-copyDoneCh:
		if err != nil {
			t.Fatalf("unexpected copy error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("copy loop did not stop after session completion")
	}
	if got := buf.String(); got != "test-output" {
		t.Fatalf("unexpected stdout: got %q want %q", got, "test-output")
	}
}

func TestRunNetcatExecSessionStopsChildWhenPeerCloses(t *testing.T) {
	server, client := net.Pipe()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runNetcatExecSession(context.Background(), client, &netcatExecConfig{Shell: true, Command: "sleep 30"}, false, slog.Default())
	}()
	if err := server.Close(); err != nil {
		t.Fatalf("failed to close peer: %v", err)
	}
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("exec session did not stop child after peer close")
	}
}

func TestRunNetcatServerExecTCPRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transportClosed := false
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runNetcatServer(ctx, &netcatServerConfig{
			Listen: func(context.Context) (*netcatListenerResult, error) {
				return &netcatListenerResult{Listener: listener, Display: listener.Addr().String()}, nil
			},
			DownstreamHalfClose: true,
			CloseTransport: func() error {
				transportClosed = true
				return nil
			},
			Exec:        &netcatExecConfig{Shell: true, Command: "cat"},
			OpenTimeout: time.Second,
			Logger:      slog.Default(),
		})
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("unexpected connection type %T", conn)
	}
	if _, err := io.WriteString(tcpConn, "test-payload"); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}
	if err := tcpConn.CloseWrite(); err != nil {
		t.Fatalf("failed to close client write side: %v", err)
	}
	var buf bytes.Buffer
	readDoneCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, tcpConn)
		readDoneCh <- err
	}()
	select {
	case err := <-readDoneCh:
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("client did not observe connection close")
	}
	if err := tcpConn.Close(); err != nil {
		t.Fatalf("failed to close client connection: %v", err)
	}
	if got := buf.String(); got != "test-payload" {
		t.Fatalf("unexpected payload: got %q want %q", got, "test-payload")
	}
	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("unexpected server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not stop after cancellation")
	}
	if !transportClosed {
		t.Fatalf("transport was not closed")
	}
}

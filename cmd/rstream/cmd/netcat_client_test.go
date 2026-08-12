// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunNetcatClientValidatesConfig(t *testing.T) {
	if err := runNetcatClient(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "client config is required") {
		t.Fatalf("runNetcatClient(nil) = %v, want config error", err)
	}
	if err := runNetcatClient(t.Context(), &netcatClientConfig{}); err == nil || !strings.Contains(err.Error(), "client dialer is required") {
		t.Fatalf("runNetcatClient(missing dialer) = %v, want dialer error", err)
	}
}

func TestRunNetcatClientInteractiveCopiesInputAndOutput(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	if err := serverConn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	var stdout bytes.Buffer
	cfg := &netcatClientConfig{
		Target:      "pipe",
		Interactive: true,
		Stdin:       strings.NewReader("ping"),
		Stdout:      &stdout,
		Dial: func(context.Context) (net.Conn, error) {
			return clientConn, nil
		},
		Logger: slog.Default(),
	}
	done := make(chan error, 1)
	go func() {
		done <- runNetcatClient(t.Context(), cfg)
	}()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("server ReadFull() error = %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("server received %q, want ping", buf)
	}
	if _, err := serverConn.Write([]byte("pong")); err != nil {
		t.Fatalf("server Write() error = %v", err)
	}
	_ = serverConn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runNetcatClient() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("runNetcatClient() timed out")
	}
	if stdout.String() != "pong" {
		t.Fatalf("stdout = %q, want pong", stdout.String())
	}
}

func TestRunNetcatClientClosesTransport(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	closed := make(chan struct{}, 1)
	cfg := &netcatClientConfig{
		Target: "pipe",
		Dial: func(context.Context) (net.Conn, error) {
			return clientConn, nil
		},
		CloseTransport: func() error {
			closed <- struct{}{}
			return nil
		},
		Logger: slog.Default(),
	}
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runNetcatClient(t.Context(), cfg)
	}()
	if err := serverConn.Close(); err != nil {
		t.Fatalf("server Close() error = %v", err)
	}
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("runNetcatClient() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("runNetcatClient() timed out")
	}
	select {
	case <-closed:
	default:
		t.Fatalf("transport was not closed")
	}
}

func TestRunNetcatClientJoinsContextStdinReader(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	started := make(chan struct{})
	stopped := make(chan struct{})
	var startedOnce sync.Once
	var stoppedOnce sync.Once
	done := make(chan error, 1)
	go func() {
		done <- runNetcatClient(t.Context(), &netcatClientConfig{
			Target:      "pipe",
			Interactive: true,
			Stdin:       strings.NewReader(""),
			StdinReadContext: func(ctx context.Context, _ []byte) (int, error) {
				startedOnce.Do(func() { close(started) })
				<-ctx.Done()
				stoppedOnce.Do(func() { close(stopped) })
				return 0, ctx.Err()
			},
			Dial:   func(context.Context) (net.Conn, error) { return clientConn, nil },
			Logger: slog.Default(),
		})
	}()
	<-started
	if err := serverConn.Close(); err != nil {
		t.Fatalf("server Close() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runNetcatClient() error = %v", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("runNetcatClient returned before its stdin reader stopped")
	}
}

type uncancelableNetcatReader struct{}

func (uncancelableNetcatReader) Read([]byte) (int, error) { return 0, nil }

func TestRunNetcatClientRejectsUncancelableStdinBeforeDial(t *testing.T) {
	dialed := false
	err := runNetcatClient(t.Context(), &netcatClientConfig{
		Interactive: true,
		Stdin:       uncancelableNetcatReader{},
		Dial: func(context.Context) (net.Conn, error) {
			dialed = true
			return nil, errors.New("unexpected dial")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stdin reader must support cancellation") {
		t.Fatalf("runNetcatClient() error = %v, want cancellable stdin error", err)
	}
	if dialed {
		t.Fatal("runNetcatClient dialed before validating stdin cancellation")
	}
}

func TestCopyNetcatInputHalfClosesDestination(t *testing.T) {
	dst := &halfCloseRecorderConn{Reader: strings.NewReader(""), Writer: io.Discard}
	if err := copyNetcatInput(dst, strings.NewReader("payload"), true); err != nil {
		t.Fatalf("copyNetcatInput() error = %v", err)
	}
	if dst.writes.String() != "payload" || !dst.closeWriteCalled {
		t.Fatalf("copyNetcatInput() state = writes %q closeWrite %v", dst.writes.String(), dst.closeWriteCalled)
	}
	dst.closeWriteErr = errors.New("close write failed")
	if err := copyNetcatInput(dst, strings.NewReader(""), true); err == nil || !strings.Contains(err.Error(), "close write failed") {
		t.Fatalf("copyNetcatInput(close error) = %v, want close error", err)
	}
}

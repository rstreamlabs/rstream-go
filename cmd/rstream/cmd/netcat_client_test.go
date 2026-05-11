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

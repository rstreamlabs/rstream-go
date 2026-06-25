// See LICENSE file in the project root for license information.

//go:build !windows

package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRunNetcatDatagramExecSessionEchoesDatagrams(t *testing.T) {
	connA := newNetcatTestUDPConn(t)
	connB := newNetcatTestUDPConn(t)
	execCfg := &netcatExecConfig{Command: "cat"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sessionErrCh := make(chan error, 1)
	go func() {
		sessionErrCh <- runNetcatDatagramExecSession(ctx, connA, connB.LocalAddr(), execCfg, 0, io.Discard, slog.Default())
	}()
	if err := connB.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, err := connB.WriteTo([]byte("ping"), connA.LocalAddr()); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := connB.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("echoed datagram = %q, want ping", buf[:n])
	}
	cancel()
	select {
	case err := <-sessionErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("exec session error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("exec session did not terminate after cancellation")
	}
}

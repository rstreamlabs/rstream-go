// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestRunNetcatProxySessionCopiesBidirectionally(t *testing.T) {
	downstreamClient, downstreamProxy := net.Pipe()
	upstreamProxy, upstreamServer := net.Pipe()
	defer downstreamClient.Close()
	defer downstreamProxy.Close()
	defer upstreamProxy.Close()
	defer upstreamServer.Close()
	if err := downstreamClient.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("downstream SetDeadline() error = %v", err)
	}
	if err := upstreamServer.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("upstream SetDeadline() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runNetcatProxySession(ctx, downstreamProxy, func(context.Context) (net.Conn, error) {
			return upstreamProxy, nil
		}, time.Second, false, false, slog.Default())
	}()
	if _, err := downstreamClient.Write([]byte("ping")); err != nil {
		t.Fatalf("downstream Write() error = %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(upstreamServer, buf); err != nil {
		t.Fatalf("upstream ReadFull() error = %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("upstream received %q, want ping", buf)
	}
	if _, err := upstreamServer.Write([]byte("pong")); err != nil {
		t.Fatalf("upstream Write() error = %v", err)
	}
	if _, err := io.ReadFull(downstreamClient, buf); err != nil {
		t.Fatalf("downstream ReadFull() error = %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("downstream received %q, want pong", buf)
	}
	_ = downstreamClient.Close()
	_ = upstreamServer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runNetcatProxySession() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("runNetcatProxySession() timed out")
	}
}

func TestNetcatManagedListenerClosesListenerAndControlOnce(t *testing.T) {
	listenerErr := errors.New("listener close")
	ctrlErr := errors.New("control close")
	listener := &netcatCloseRecorderListener{err: listenerErr}
	ctrl := &netcatCloseRecorder{err: ctrlErr}
	managed := &netcatManagedListener{Listener: listener, ctrl: ctrl}
	if err := managed.Close(); !errors.Is(err, listenerErr) {
		t.Fatalf("Close() error = %v, want listener error", err)
	}
	if err := managed.Close(); !errors.Is(err, listenerErr) {
		t.Fatalf("second Close() error = %v, want listener error", err)
	}
	if listener.closes != 1 || ctrl.closes != 1 {
		t.Fatalf("close counts listener=%d control=%d, want 1 each", listener.closes, ctrl.closes)
	}
	ctrl = &netcatCloseRecorder{err: ctrlErr}
	managed = &netcatManagedListener{ctrl: ctrl}
	if err := managed.Close(); !errors.Is(err, ctrlErr) {
		t.Fatalf("Close(control only) error = %v, want control error", err)
	}
}

type netcatCloseRecorder struct {
	err    error
	closes int
}

func (c *netcatCloseRecorder) Close() error {
	c.closes++
	return c.err
}

type netcatCloseRecorderListener struct {
	err    error
	closes int
}

func (l *netcatCloseRecorderListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (l *netcatCloseRecorderListener) Close() error {
	l.closes++
	return l.err
}

func (l *netcatCloseRecorderListener) Addr() net.Addr {
	return netcatTestAddr("listener")
}

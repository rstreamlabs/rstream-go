//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func echoPipe(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		_, _ = io.Copy(server, server)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("echo worker leaked")
		}
	})
	return client
}

func TestTransferStreamVerifiesPartialBlocksAndSlowReaders(t *testing.T) {
	for _, delay := range []time.Duration{0, 2 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var out result
			var active atomic.Bool
			if err := transferStream(ctx, echoPipe(t), (64<<10)+17, delay, &out, &active); err != nil {
				t.Fatal(err)
			}
			if out.Bytes != (64<<10)+17 || out.Seconds <= 0 || active.Load() {
				t.Fatalf("incomplete transfer observation: %+v, active=%v", out, active.Load())
			}
		})
	}
}

func TestTransferStreamDetectsCorruption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		payload := make([]byte, 64)
		_, _ = io.ReadFull(server, payload)
		payload[0] ^= 1
		_, _ = server.Write(payload)
	}()
	var out result
	var active atomic.Bool
	if err := transferStream(ctx, client, 64, 0, &out, &active); err == nil {
		t.Fatal("corrupted payload accepted")
	}
	<-done
}

func TestTransferStreamCancellationUnblocksBothDirections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		var out result
		var active atomic.Bool
		done <- transferStream(ctx, client, 1<<20, 0, &out, &active)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked transfer reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("transfer workers did not stop after cancellation")
	}
}

func TestCancellationProbeStopsBlockedWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	var out result
	if err := cancellationProbe(ctx, client, &out); err != nil {
		t.Fatal(err)
	}
	if out.CancelUS <= 0 || out.CancelUS >= 2e6 {
		t.Fatalf("invalid cancellation observation: %f", out.CancelUS)
	}
}

func TestServeStreamsCancellationStopsAcceptAndActiveWorker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveStreams(ctx, listener, 2) }()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := exchangePing(client); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accept or active echo worker leaked")
	}
}

func TestProfilesPreserveExistingEvidence(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "sample")
	path := prefix + "-cpu.pprof"
	if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := startProfiles(prefix); err == nil {
		t.Fatal("existing profile overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "retain" {
		t.Fatalf("existing profile changed: %q, %v", got, err)
	}
}

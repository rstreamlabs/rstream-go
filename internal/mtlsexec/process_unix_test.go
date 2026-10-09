// See LICENSE file in the project root for license information.

//go:build unix && !rstream_fips

package mtlsexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestExecCanceledProcessIsReaped(t *testing.T) {
	f := newFixture(t, nil)
	events := filepath.Join(t.TempDir(), "events")
	f.options.Args[5] = events
	p := f.provider(t, "sleep")
	for range 3 {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, err := p.loadIdentity(ctx); done <- err }()
		waitEvent(t, events, "identity")
		data, err := os.ReadFile(events)
		if err != nil {
			t.Fatal(err)
		}
		var pid int
		if _, err := fmt.Sscanf(string(data), "identity %d", &pid); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancellation did not complete")
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("helper %d was not reaped: %v", pid, err)
		}
		if err := os.Remove(events); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecRejectsUnsafeExecutablePermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o777} {
		f := newFixture(t, nil)
		f.options.Command = filepath.Join(t.TempDir(), "helper")
		if err := os.WriteFile(f.options.Command, []byte("invalid executable"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f.options.Command, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := f.provider(t, "message").Check(t.Context()); err == nil {
			t.Fatalf("accepted executable permissions %o", mode)
		}
	}
}

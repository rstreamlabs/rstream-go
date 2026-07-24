// See LICENSE file in the project root for license information.

package netretry

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestNextAcceptDelay(t *testing.T) {
	for _, err := range []error{syscall.EINTR, syscall.ECONNABORTED, syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM} {
		delay, retry := NextAcceptDelay(err, 0, time.Second)
		if !retry || delay != 5*time.Millisecond {
			t.Fatalf("NextAcceptDelay(%v) = %v, %t, want 5ms, true", err, delay, retry)
		}
	}
	delay, retry := NextAcceptDelay(syscall.EMFILE, 800*time.Millisecond, time.Second)
	if !retry || delay != time.Second {
		t.Fatalf("capped NextAcceptDelay() = %v, %t, want 1s, true", delay, retry)
	}
	if delay, retry := NextAcceptDelay(errors.New("closed"), 0, time.Second); retry || delay != 0 {
		t.Fatalf("NextAcceptDelay(non-retryable) = %v, %t, want 0, false", delay, retry)
	}
}

func TestWaitReturnsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	if Wait(ctx, time.Hour) {
		t.Fatal("Wait() = true, want false")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Wait() took %v after cancellation", elapsed)
	}
}

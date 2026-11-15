// See LICENSE file in the project root for license information.

package contextsync

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutexCancellationDoesNotConsumeOwnership(t *testing.T) {
	var mutex Mutex
	cause := errors.New("writer cancelled")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	if err := mutex.Lock(ctx); !errors.Is(err, cause) {
		t.Fatalf("cancelled acquisition = %v", err)
	}
	if !mutex.TryLock() || mutex.TryLock() {
		t.Fatal("zero-value mutex did not retain exclusive ownership")
	}
	waitCtx, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	if err := mutex.Lock(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued acquisition = %v", err)
	}
	mutex.Unlock()
	if !mutex.TryLock() {
		t.Fatal("cancelled waiter retained ownership")
	}
	mutex.Unlock()
}

func TestMutexBoundsConcurrentWriterOwnership(t *testing.T) {
	const callers, operations = 16, 50
	var mutex Mutex
	var active atomic.Int32
	var completed int
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for range callers {
		workers.Go(func() {
			for range operations {
				if err := mutex.Lock(ctx); err != nil {
					t.Error(err)
					return
				}
				if active.Add(1) != 1 {
					t.Error("more than one active writer")
				}
				completed++
				active.Add(-1)
				mutex.Unlock()
			}
		})
	}
	workers.Wait()
	if completed != callers*operations || active.Load() != 0 || !mutex.TryLock() {
		t.Fatalf("writer lifecycle = %d completed, %d active", completed, active.Load())
	}
	mutex.Unlock()
}

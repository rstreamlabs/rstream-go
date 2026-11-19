// See LICENSE file in the project root for license information.

package rtc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestRestartCadenceRequiresReliableProgress(t *testing.T) {
	now := time.Now()
	cadence := restartCadence{restarted: now, interval: time.Second}
	assertDue := func(at time.Time, progress int64, complete, want bool) {
		t.Helper()
		got := cadence.due(at, progress, complete)
		if got != want {
			t.Fatalf("restart=%v want=%v", got, want)
		}
	}
	assertDue(now.Add(time.Second), 10, false, true)
	cadence.selected(now.Add(time.Second), 10, false)
	assertDue(now.Add(5*time.Second), 10, false, false)
	assertDue(now.Add(5*time.Second), 9, false, false)
	assertDue(now.Add(5*time.Second), 11, false, false)
	assertDue(now.Add(5999*time.Millisecond), 12, false, false)
	assertDue(now.Add(6*time.Second), 12, false, true)
	assertDue(now.Add(time.Hour), 12, true, false)
}

func TestRestartRecoveryPreservesPausedReaders(t *testing.T) {
	now := time.Now()
	cadence := restartCadence{restarted: now, interval: time.Second}
	cadence.selected(now, 10, false)
	if cadence.due(now.Add(time.Hour), 10, false) {
		t.Fatal("paused consumer caused another ICE replacement")
	}
	if cadence.due(now.Add(time.Hour), 11, false) {
		t.Fatal("resumed data did not receive a fresh interval")
	}
	if !cadence.due(now.Add(time.Hour+time.Second), 11, false) {
		t.Fatal("resumed transfer never rearmed ICE replacement")
	}
}

func TestICESelectionRejectsStaleNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var selected atomic.Bool
		selection := &iceSelection{changed: make(chan struct{}, 1), current: selected.Load}
		selection.changed <- struct{}{}
		done := make(chan error, 1)
		go func() { done <- selection.wait(t.Context()) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("stale callback released an unselected restart")
		default:
		}
		selected.Store(true)
		selection.changed <- struct{}{}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestICESelectionChecksCurrentStateBeforeWaiting(t *testing.T) {
	selection := &iceSelection{changed: make(chan struct{}, 1), current: func() bool { return true }}
	if err := selection.wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	selection.current = func() bool { t.Fatal("canceled owner queried selection"); return false }
	if err := selection.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selection: %v", err)
	}
}

func TestICESelectionCancellationUnblocksOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		selection := &iceSelection{changed: make(chan struct{}, 1), current: func() bool { return false }}
		done := make(chan error, 1)
		go func() { done <- selection.wait(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("selection wait did not honor cancellation: %v", err)
		}
	})
}

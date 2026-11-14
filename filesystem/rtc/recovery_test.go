// See LICENSE file in the project root for license information.

package rtc

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func recoveryBody(t *testing.T) *responseBody {
	t.Helper()
	ctx, cancel := context.WithCancelCause(t.Context())
	t.Cleanup(func() { cancel(context.Canceled) })
	return &responseBody{ctx: ctx, cancel: cancel, packets: make(chan []byte, Window), ended: make(chan struct{}), recovery: make(chan struct{}, 1)}
}

func TestRecoveryBudgetStartsOnlyForAnActiveRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := recoveryBody(t)
		body.recovering.Store(true)
		body.packets <- []byte("queued")
		time.Sleep(time.Minute)
		packet, err := body.nextPacket()
		if err != nil || !bytes.Equal(packet, []byte("queued")) || body.ctx.Err() != nil {
			t.Fatal("backpressure expired a paused consumer")
		}
		done := make(chan error, 1)
		go func() { _, err := body.nextPacket(); done <- err }()
		synctest.Wait()
		time.Sleep(restartRecoveryBudget)
		synctest.Wait()
		if err := <-done; err == nil || !strings.Contains(err.Error(), "did not resume") {
			t.Fatalf("stalled active read was not bounded: %v", err)
		}
	})
}

func TestRecoveryCanBeginWhileReaderIsAlreadyWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := recoveryBody(t)
		done := make(chan error, 1)
		go func() { _, err := body.nextPacket(); done <- err }()
		synctest.Wait()
		body.recovering.Store(true)
		body.recovery <- struct{}{}
		synctest.Wait()
		time.Sleep(restartRecoveryBudget)
		synctest.Wait()
		if err := <-done; err == nil || !strings.Contains(err.Error(), "did not resume") {
			t.Fatalf("existing read missed recovery transition: %v", err)
		}
	})
}

func TestRecoveryReadHonorsOwnerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := recoveryBody(t)
		body.recovering.Store(true)
		done := make(chan error, 1)
		go func() { _, err := body.nextPacket(); done <- err }()
		synctest.Wait()
		reason := context.Canceled
		body.cancel(reason)
		if err := <-done; err != reason {
			t.Fatalf("cancellation changed identity: %v", err)
		}
	})
}

func TestRecoverySnapshotCannotHideConcurrentProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := recoveryBody(t)
		body.recovering.Store(true)
		done := make(chan error, 1)
		go func() { _, err := body.nextPacket(); done <- err }()
		synctest.Wait()
		body.progress.Add(1)
		time.Sleep(restartRecoveryBudget)
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("stale recovery flag ignored observed progress: %v", err)
		default:
		}
		body.cancel(context.Canceled)
		if err := <-done; err != context.Canceled {
			t.Fatal(err)
		}
	})
}

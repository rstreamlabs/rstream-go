// See LICENSE file in the project root for license information.

package netretry

import (
	"context"
	"errors"
	"syscall"
	"time"
)

const initialAcceptDelay = 5 * time.Millisecond

func NextAcceptDelay(err error, current, maximum time.Duration) (time.Duration, bool) {
	retry := errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.ENOMEM)
	if !retry {
		return 0, false
	}
	if current <= 0 {
		current = initialAcceptDelay
	} else {
		current *= 2
	}
	if maximum > 0 && current > maximum {
		current = maximum
	}
	return current, true
}

func Wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

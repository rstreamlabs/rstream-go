// See LICENSE file in the project root for license information.

package rtc

import (
	"time"
)

const restartRecoveryBudget = 20 * time.Second

type restartCadence struct {
	restarted time.Time
	interval  time.Duration
	waiting   bool
	progress  int64
}

func (c *restartCadence) due(now time.Time, progress int64, complete bool) bool {
	if c.waiting {
		if progress <= c.progress && !complete {
			return false
		}
		c.waiting = false
		c.restarted = now
	}
	return !complete && now.Sub(c.restarted) >= c.interval
}

func (c *restartCadence) selected(now time.Time, progress int64, complete bool) {
	c.restarted = now
	c.waiting = !complete
	c.progress = progress
}

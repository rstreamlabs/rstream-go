// See LICENSE file in the project root for license information.

package rstream

import (
	"sync"
	"time"
)

type packetDeadline struct {
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	changed  chan struct{}
}

func newPacketDeadline() *packetDeadline {
	return &packetDeadline{changed: make(chan struct{})}
}

func (d *packetDeadline) set(t time.Time) {
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	closePacketDeadlineSignal(d.changed)
	d.deadline = t
	d.changed = make(chan struct{})
	if t.IsZero() {
		d.mu.Unlock()
		return
	}
	if dur := time.Until(t); dur <= 0 {
		close(d.changed)
	} else {
		changed := d.changed
		d.timer = time.AfterFunc(dur, func() {
			d.mu.Lock()
			if d.changed == changed {
				closePacketDeadlineSignal(changed)
				d.timer = nil
			}
			d.mu.Unlock()
		})
	}
	d.mu.Unlock()
}

func (d *packetDeadline) snapshot() (time.Time, <-chan struct{}) {
	d.mu.Lock()
	deadline := d.deadline
	changed := d.changed
	d.mu.Unlock()
	return deadline, changed
}

func (d *packetDeadline) expired() bool {
	deadline, _ := d.snapshot()
	return !deadline.IsZero() && !time.Now().Before(deadline)
}

func closePacketDeadlineSignal(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

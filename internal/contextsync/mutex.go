// See LICENSE file in the project root for license information.

package contextsync

import (
	"context"
	"sync"
)

// Mutex serializes protocol writes without retaining cancelled waiters.
// Its zero value is ready for use and must not be copied after first use.
type Mutex struct {
	once sync.Once
	ch   chan struct{}
}

func (m *Mutex) channel() chan struct{} {
	m.once.Do(func() {
		m.ch = make(chan struct{}, 1)
		m.ch <- struct{}{}
	})
	return m.ch
}

func (m *Mutex) Lock(ctx context.Context) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-m.channel():
		if err := context.Cause(ctx); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}

func (m *Mutex) TryLock() bool {
	select {
	case <-m.channel():
		return true
	default:
		return false
	}
}

func (m *Mutex) Unlock() {
	m.ch <- struct{}{}
}

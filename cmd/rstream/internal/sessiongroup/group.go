// See LICENSE file in the project root for license information.

package sessiongroup

import (
	"context"
	"io"
	"sync"
)

// Group owns relays accepted across successive control-channel generations.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	active map[uint64]io.Closer
	nextID uint64
	closed bool
	wg     sync.WaitGroup
}

func New(ctx context.Context) *Group {
	sessionCtx, cancel := context.WithCancel(ctx)
	return &Group{ctx: sessionCtx, cancel: cancel, active: make(map[uint64]io.Closer)}
}

func (g *Group) Start(closer io.Closer, run func(context.Context)) bool {
	if g == nil || closer == nil || run == nil {
		if closer != nil {
			_ = closer.Close()
		}
		return false
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		_ = closer.Close()
		return false
	}
	g.nextID++
	id := g.nextID
	g.active[id] = closer
	g.wg.Add(1)
	g.mu.Unlock()
	go func() {
		defer g.wg.Done()
		defer func() {
			g.mu.Lock()
			delete(g.active, id)
			g.mu.Unlock()
		}()
		run(g.ctx)
	}()
	return true
}

func (g *Group) Close() {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		g.wg.Wait()
		return
	}
	g.closed = true
	g.cancel()
	active := make([]io.Closer, 0, len(g.active))
	for _, closer := range g.active {
		active = append(active, closer)
	}
	g.mu.Unlock()
	for _, closer := range active {
		_ = closer.Close()
	}
	g.wg.Wait()
}

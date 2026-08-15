// See LICENSE file in the project root for license information.

package sessiongroup

import (
	"context"
	"sync"
	"testing"
	"time"
)

type testCloser struct {
	closed chan struct{}
	once   sync.Once
}

type nonComparableCloser []byte

func (nonComparableCloser) Close() error { return nil }

func (c *testCloser) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func TestGroupKeepsSessionsUntilRootShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	group := New(ctx)
	closer := &testCloser{closed: make(chan struct{})}
	started := make(chan struct{})
	stopped := make(chan struct{})
	if !group.Start(closer, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		<-closer.closed
		close(stopped)
	}) {
		t.Fatal("Start() rejected an open group")
	}
	<-started
	select {
	case <-closer.closed:
		t.Fatal("session closed before root shutdown")
	default:
	}
	cancel()
	group.Close()
	select {
	case <-stopped:
	default:
		t.Fatal("Close() returned before the session stopped")
	}
}

func TestGroupConcurrentCloseAndStartIsBounded(t *testing.T) {
	group := New(t.Context())
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			closer := &testCloser{closed: make(chan struct{})}
			group.Start(closer, func(context.Context) { <-closer.closed })
		}()
	}
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			group.Close()
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent Start and Close did not converge")
	}
	late := &testCloser{closed: make(chan struct{})}
	if group.Start(late, func(context.Context) {}) {
		t.Fatal("Start() accepted a session after Close()")
	}
	select {
	case <-late.closed:
	default:
		t.Fatal("rejected session was not closed")
	}
}

func TestGroupAcceptsNonComparableCloserImplementations(t *testing.T) {
	group := New(t.Context())
	if !group.Start(nonComparableCloser{1}, func(ctx context.Context) { <-ctx.Done() }) {
		t.Fatal("Start() rejected a valid closer")
	}
	group.Close()
}

func TestGroupTracksRepeatedCloserInstancesIndependently(t *testing.T) {
	group := New(t.Context())
	closer := &testCloser{closed: make(chan struct{})}
	firstRelease := make(chan struct{})
	firstDone := make(chan struct{})
	if !group.Start(closer, func(context.Context) {
		<-firstRelease
		close(firstDone)
	}) {
		t.Fatal("Start() rejected first session")
	}
	if !group.Start(closer, func(context.Context) { <-closer.closed }) {
		t.Fatal("Start() rejected second session")
	}
	close(firstRelease)
	<-firstDone
	done := make(chan struct{})
	go func() {
		group.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close() lost a repeated active closer")
	}
}

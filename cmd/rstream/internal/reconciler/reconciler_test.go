// See LICENSE file in the project root for license information.

package reconciler

import (
	"context"
	"sync"
	"testing"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

type fakeStarter struct {
	mu    sync.Mutex
	start map[string]int
	stop  map[string]int
}

type fakeHandle struct {
	name   string
	parent *fakeStarter
}

func (f *fakeStarter) Start(ctx context.Context, desired runmodel.DesiredTunnel) (runmodel.Handle, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.start == nil {
		f.start = make(map[string]int)
	}
	if f.stop == nil {
		f.stop = make(map[string]int)
	}
	f.start[desired.Name]++
	return &fakeHandle{name: desired.Name, parent: f}, nil
}

func (h *fakeHandle) Stop() error {
	if h == nil || h.parent == nil {
		return nil
	}
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()
	h.parent.stop[h.name]++
	return nil
}

func TestReconcilerDiff(t *testing.T) {
	ctx := context.Background()
	starter := &fakeStarter{}
	recon := New(ctx, starter, nil)
	initial := []runmodel.DesiredTunnel{
		{Name: "one", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "8080"}},
		{Name: "two", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "8081"}},
	}
	if err := recon.Reconcile(initial); err != nil {
		t.Fatalf("reconcile initial: %v", err)
	}
	next := []runmodel.DesiredTunnel{
		{Name: "one", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "8080"}},
		{Name: "two", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "9090"}},
		{Name: "three", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "8082"}},
	}
	if err := recon.Reconcile(next); err != nil {
		t.Fatalf("reconcile next: %v", err)
	}
	final := []runmodel.DesiredTunnel{{Name: "three", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "8082"}}}
	if err := recon.Reconcile(final); err != nil {
		t.Fatalf("reconcile final: %v", err)
	}
	starter.mu.Lock()
	defer starter.mu.Unlock()
	cases := []struct {
		name      string
		startWant int
		stopWant  int
	}{
		{name: "one", startWant: 1, stopWant: 1},
		{name: "two", startWant: 2, stopWant: 2},
		{name: "three", startWant: 1, stopWant: 0},
	}
	for _, tc := range cases {
		if got := starter.start[tc.name]; got != tc.startWant {
			t.Fatalf("start count for %q: got %d want %d", tc.name, got, tc.startWant)
		}
		if got := starter.stop[tc.name]; got != tc.stopWant {
			t.Fatalf("stop count for %q: got %d want %d", tc.name, got, tc.stopWant)
		}
	}
}

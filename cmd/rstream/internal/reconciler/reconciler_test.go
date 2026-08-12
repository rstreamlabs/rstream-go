// See LICENSE file in the project root for license information.

package reconciler

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/config"
)

type mutableDialer struct {
	selected bool
}

func (d *mutableDialer) Dial(context.Context, string, *tls.Config) (net.Conn, error) {
	return nil, net.ErrClosed
}

type fakeStarter struct {
	mu       sync.Mutex
	start    map[string]int
	stop     map[string]int
	startErr map[string]error
	stopErr  map[string]error
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
	if err := f.startErr[desired.Name]; err != nil {
		return nil, err
	}
	return &fakeHandle{name: desired.Name, parent: f}, nil
}

func (h *fakeHandle) Stop() error {
	if h == nil || h.parent == nil {
		return nil
	}
	h.parent.mu.Lock()
	defer h.parent.mu.Unlock()
	h.parent.stop[h.name]++
	return h.parent.stopErr[h.name]
}

func TestReconcilerDiff(t *testing.T) {
	ctx := context.Background()
	starter := &fakeStarter{}
	recon := New(ctx, starter, nil)
	defer recon.Stop()
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

func TestReconcilerOwnsDesiredMutableState(t *testing.T) {
	starter := &fakeStarter{}
	recon := New(t.Context(), starter, nil)
	defer recon.Stop()
	desired := runmodel.DesiredTunnel{Name: "web", Props: rstream.TunnelProperties{Labels: map[string]string{"env": "prod"}, GeoIP: []string{"FR"}}, Context: runmodel.ResolvedContext{TransportConfig: &config.TransportConfig{Proxy: &config.ProxyConfig{Headers: map[string]string{"X-Test": "original"}}}}}
	if err := recon.Reconcile([]runmodel.DesiredTunnel{desired}); err != nil {
		t.Fatal(err)
	}
	desired.Props.Labels["env"] = "mutated"
	desired.Props.GeoIP[0] = "US"
	desired.Context.TransportConfig.Proxy.Headers["X-Test"] = "mutated"
	canonical := runmodel.DesiredTunnel{Name: "web", Props: rstream.TunnelProperties{Labels: map[string]string{"env": "prod"}, GeoIP: []string{"FR"}}, Context: runmodel.ResolvedContext{TransportConfig: &config.TransportConfig{Proxy: &config.ProxyConfig{Headers: map[string]string{"X-Test": "original"}}}}}
	if err := recon.Reconcile([]runmodel.DesiredTunnel{canonical}); err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	starts, stops := starter.start["web"], starter.stop["web"]
	starter.mu.Unlock()
	if starts != 1 || stops != 0 {
		t.Fatalf("caller mutation changed active desired state: starts %d stops %d", starts, stops)
	}
}

func TestReconcilerReportsLifecycleErrorsAndRetriesFailedResources(t *testing.T) {
	starter := &fakeStarter{startErr: make(map[string]error), stopErr: make(map[string]error)}
	recon := New(t.Context(), starter, nil)
	defer recon.Stop()
	initial := []runmodel.DesiredTunnel{{Name: "web", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "8080"}}}
	if err := recon.Reconcile(initial); err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	starter.stopErr["web"] = errors.New("stop failed")
	starter.mu.Unlock()
	updated := []runmodel.DesiredTunnel{{Name: "web", Forward: runmodel.ForwardTarget{Host: "localhost", Port: "9090"}}}
	if err := recon.Reconcile(updated); err == nil || !strings.Contains(err.Error(), "stop updated tunnel") {
		t.Fatalf("Reconcile() stop error = %v", err)
	}
	starter.mu.Lock()
	starts, stops := starter.start["web"], starter.stop["web"]
	starter.stopErr["web"] = nil
	starter.mu.Unlock()
	if starts != 1 || stops != 1 {
		t.Fatalf("failed update lifecycle = starts %d stops %d, want 1/1", starts, stops)
	}
	if err := recon.Reconcile(updated); err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	starter.startErr["worker"] = errors.New("start failed")
	starter.mu.Unlock()
	withWorker := append(updated, runmodel.DesiredTunnel{Name: "worker"})
	if err := recon.Reconcile(withWorker); err == nil || !strings.Contains(err.Error(), "start tunnel") {
		t.Fatalf("Reconcile() start error = %v", err)
	}
	starter.mu.Lock()
	starter.startErr["worker"] = nil
	starter.mu.Unlock()
	if err := recon.Reconcile(withWorker); err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	webStarts, webStops := starter.start["web"], starter.stop["web"]
	workerStarts := starter.start["worker"]
	starter.mu.Unlock()
	if webStarts != 2 || webStops != 2 || workerStarts != 2 {
		t.Fatalf("retried lifecycle = web %d/%d worker starts %d, want 2/2/2", webStarts, webStops, workerStarts)
	}
}

func TestReconcilerIgnoresMutableTransportStateAndSerializesConcurrentReloads(t *testing.T) {
	starter := &fakeStarter{}
	recon := New(context.Background(), starter, nil)
	defer recon.Stop()
	desired := func(selected bool) []runmodel.DesiredTunnel {
		return []runmodel.DesiredTunnel{{Name: "web", Context: runmodel.ResolvedContext{Transport: &mutableDialer{selected: selected}}}}
	}
	if err := recon.Reconcile(desired(false)); err != nil {
		t.Fatal(err)
	}
	const reloads = 64
	var wg sync.WaitGroup
	errs := make(chan error, reloads)
	for i := 0; i < reloads; i++ {
		wg.Add(1)
		go func(selected bool) {
			defer wg.Done()
			errs <- recon.Reconcile(desired(selected))
		}(i%2 == 0)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	starter.mu.Lock()
	defer starter.mu.Unlock()
	if got := starter.start["web"]; got != 1 {
		t.Fatalf("start count = %d, want 1", got)
	}
	if got := starter.stop["web"]; got != 0 {
		t.Fatalf("stop count = %d, want 0", got)
	}
}

func TestReconcilerContextCancellationStopsActiveTunnelsAndRejectsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	starter := &fakeStarter{}
	recon := New(ctx, starter, nil)
	if err := recon.Reconcile([]runmodel.DesiredTunnel{{Name: "web"}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-recon.stopped:
	case <-time.After(time.Second):
		t.Fatal("reconciler did not stop after context cancellation")
	}
	if err := recon.Reconcile([]runmodel.DesiredTunnel{{Name: "other"}}); !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Reconcile() after cancellation error = %v", err)
	}
	starter.mu.Lock()
	starts, stops := starter.start["web"], starter.stop["web"]
	starter.mu.Unlock()
	if starts != 1 || stops != 1 {
		t.Fatalf("active tunnel lifecycle = starts %d stops %d, want 1/1", starts, stops)
	}
}

func TestReconcilerConcurrentStopIsSafe(t *testing.T) {
	starter := &fakeStarter{}
	recon := New(t.Context(), starter, nil)
	if err := recon.Reconcile([]runmodel.DesiredTunnel{{Name: "web"}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for index := 0; index < 64; index++ {
		wg.Add(1)
		go func() { defer wg.Done(); recon.Stop() }()
	}
	wg.Wait()
	starter.mu.Lock()
	starts, stops := starter.start["web"], starter.stop["web"]
	starter.mu.Unlock()
	if starts != 1 || stops != 1 {
		t.Fatalf("active tunnel lifecycle = starts %d stops %d, want 1/1", starts, stops)
	}
}

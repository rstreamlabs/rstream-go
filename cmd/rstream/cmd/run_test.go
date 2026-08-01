// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/reconciler"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/config"
)

func TestRunLoopAppliesDesiredStateAndReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	starter := &runLoopStarter{}
	recon := reconciler.New(ctx, starter, nil)
	source := &runLoopSource{listFunc: func(context.Context) ([]runmodel.DesiredTunnel, error) {
		cancel()
		return []runmodel.DesiredTunnel{{Name: "api", Forward: runmodel.ForwardTarget{Host: "127.0.0.1", Port: "8080"}}}, nil
	}}
	err := runLoop(ctx, source, recon, false, slog.Default())
	if err != nil {
		t.Fatalf("runLoop() error = %v", err)
	}
	if got := starter.startCount("api"); got != 1 {
		t.Fatalf("started tunnels = %d, want 1", got)
	}
}

func TestRunLoopPropagatesListAndReconcileErrorsOutsideWatchMode(t *testing.T) {
	listErr := errors.New("list failed")
	ctx := context.Background()
	starter := &runLoopStarter{}
	recon := reconciler.New(ctx, starter, nil)
	source := &runLoopSource{listFunc: func(context.Context) ([]runmodel.DesiredTunnel, error) {
		return nil, listErr
	}}
	if err := runLoop(ctx, source, recon, false, slog.Default()); !errors.Is(err, listErr) {
		t.Fatalf("runLoop(list error) = %v, want %v", err, listErr)
	}
	source = &runLoopSource{listFunc: func(context.Context) ([]runmodel.DesiredTunnel, error) {
		return []runmodel.DesiredTunnel{{Forward: runmodel.ForwardTarget{Host: "127.0.0.1", Port: "8080"}}}, nil
	}}
	if err := runLoop(ctx, source, recon, false, slog.Default()); err == nil || !strings.Contains(err.Error(), "tunnel name is required") {
		t.Fatalf("runLoop(invalid desired) = %v, want tunnel name error", err)
	}
}

func TestRunLoopWatchModeRecoversAfterInitialListError(t *testing.T) {
	ctx := context.Background()
	events := make(chan struct{}, 1)
	starter := &runLoopStarter{}
	recon := reconciler.New(ctx, starter, nil)
	var calls int
	source := &runLoopSource{
		listFunc: func(context.Context) ([]runmodel.DesiredTunnel, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("temporary config error")
			}
			return []runmodel.DesiredTunnel{{Name: "watch", Forward: runmodel.ForwardTarget{Host: "127.0.0.1", Port: "9090"}}}, nil
		},
		watchFunc: func(context.Context) (<-chan struct{}, error) {
			events <- struct{}{}
			close(events)
			return events, nil
		},
	}
	err := runLoop(ctx, source, recon, true, slog.Default())
	if err != nil {
		t.Fatalf("runLoop(watch) error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("List calls = %d, want 2", calls)
	}
	if got := starter.startCount("watch"); got != 1 {
		t.Fatalf("started tunnels = %d, want 1", got)
	}
}

func TestRunLoopWatchErrorIsReturned(t *testing.T) {
	watchErr := errors.New("watch failed")
	ctx := context.Background()
	starter := &runLoopStarter{}
	recon := reconciler.New(ctx, starter, nil)
	source := &runLoopSource{
		listFunc: func(context.Context) ([]runmodel.DesiredTunnel, error) {
			return []runmodel.DesiredTunnel{{Name: "api", Forward: runmodel.ForwardTarget{Host: "127.0.0.1", Port: "8080"}}}, nil
		},
		watchFunc: func(context.Context) (<-chan struct{}, error) {
			return nil, watchErr
		},
	}
	if err := runLoop(ctx, source, recon, true, slog.Default()); !errors.Is(err, watchErr) {
		t.Fatalf("runLoop(watch error) = %v, want %v", err, watchErr)
	}
}

func TestResolveNamedContextRequiresEngineAndToken(t *testing.T) {
	command := runtimeFlagsCommand(t)
	mustSetFlag(t, command, "api-url", "https://api.example.com")
	cfg := config.Config{Contexts: []config.Context{{Name: "prod", APIURL: "https://api.example.com", Engine: "engine.example.com:443"}}}
	resolved, err := resolveNamedContext(cfg, config.EnvSettings{Token: "env-token"}, command, "prod")
	if err != nil {
		t.Fatalf("resolveNamedContext() error = %v", err)
	}
	if resolved.Engine != "engine.example.com:443" || resolved.Token != "env-token" {
		t.Fatalf("unexpected resolved context: %#v", resolved)
	}
	if _, err := resolveNamedContext(cfg, config.EnvSettings{}, command, "prod"); err == nil || !strings.Contains(err.Error(), "authentication is required") {
		t.Fatalf("resolveNamedContext(missing auth) = %v, want auth error", err)
	}
}

type runLoopSource struct {
	listFunc  func(context.Context) ([]runmodel.DesiredTunnel, error)
	watchFunc func(context.Context) (<-chan struct{}, error)
}

func (s *runLoopSource) List(ctx context.Context) ([]runmodel.DesiredTunnel, error) {
	if s.listFunc == nil {
		return nil, nil
	}
	return s.listFunc(ctx)
}

func (s *runLoopSource) Watch(ctx context.Context) (<-chan struct{}, error) {
	if s.watchFunc == nil {
		return nil, errors.New("watch not configured")
	}
	return s.watchFunc(ctx)
}

type runLoopStarter struct {
	mu     sync.Mutex
	starts map[string]int
}

func (s *runLoopStarter) Start(ctx context.Context, desired runmodel.DesiredTunnel) (runmodel.Handle, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.starts == nil {
		s.starts = make(map[string]int)
	}
	s.starts[desired.Name]++
	return runLoopHandle{}, nil
}

func (s *runLoopStarter) startCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts[name]
}

type runLoopHandle struct{}

func (runLoopHandle) Stop() error {
	return nil
}

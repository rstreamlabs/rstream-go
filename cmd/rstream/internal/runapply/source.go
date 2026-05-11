// See LICENSE file in the project root for license information.

package runapply

import (
	"context"
	"log/slog"

	"github.com/fsnotify/fsnotify"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

type Source struct {
	path     string
	fallback runmodel.ResolvedContext
	lookup   ResolvedContextLookup
	logger   *slog.Logger
}

func NewSource(path string, fallback runmodel.ResolvedContext, lookup ResolvedContextLookup, logger *slog.Logger) *Source {
	if logger == nil {
		logger = slog.Default()
	}
	return &Source{path: path, fallback: fallback, lookup: lookup, logger: logger}
}

func (s *Source) List(ctx context.Context) ([]runmodel.DesiredTunnel, error) {
	_ = ctx
	desired, err := DesiredTunnels(s.path, s.fallback, s.lookup)
	if err != nil {
		return nil, err
	}
	s.logger.Info("Configuration loaded", "path", s.path, "tunnels", len(desired))
	return desired, nil
}

func (s *Source) Watch(ctx context.Context) (<-chan struct{}, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	targets := watchTargets(s.path)
	for _, dir := range watchDirs(targets) {
		if err := watcher.Add(dir); err != nil {
			_ = watcher.Close()
			return nil, err
		}
	}
	s.logger.Info("Watching configuration file", "path", s.path)
	out := make(chan struct{}, 1)
	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				close(out)
				return
			case event, ok := <-watcher.Events:
				if !ok {
					close(out)
					return
				}
				if shouldReloadEvent(event, targets...) {
					select {
					case out <- struct{}{}:
					default:
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					close(out)
					return
				}
				s.logger.Warn("Watcher error", "error", err)
			}
		}
	}()
	return out, nil
}

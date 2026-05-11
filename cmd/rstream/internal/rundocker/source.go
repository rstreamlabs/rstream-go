// See LICENSE file in the project root for license information.

package rundocker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

type ContextLookup func(name string) (runmodel.ResolvedContext, error)

type Source struct {
	client             *client.Client
	fallback           runmodel.ResolvedContext
	lookup             ContextLookup
	network            string
	logger             *slog.Logger
	allowContextLabels bool
}

func NewSource(socket, network string, fallback runmodel.ResolvedContext, lookup ContextLookup, allowContextLabels bool, logger *slog.Logger) (*Source, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cli, err := client.NewClientWithOpts(client.WithHost(socket), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Source{
		client:             cli,
		fallback:           fallback,
		lookup:             lookup,
		network:            network,
		logger:             logger,
		allowContextLabels: allowContextLabels,
	}, nil
}

func (s *Source) List(ctx context.Context) ([]runmodel.DesiredTunnel, error) {
	result, err := s.client.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, err
	}
	out := []runmodel.DesiredTunnel{}
	for _, c := range result.Items {
		info := ContainerInfo{
			ID:       c.ID,
			Name:     containerName(c),
			Labels:   c.Labels,
			Networks: containerNetworks(c),
		}
		ctxResolved := s.fallback
		if labelCtx := strings.TrimSpace(info.Labels["rstream.context"]); labelCtx != "" {
			if !s.allowContextLabels {
				return nil, fmt.Errorf("container %q uses rstream.context but Docker context labels are disabled", info.Name)
			}
			if s.lookup == nil {
				return nil, fmt.Errorf("container %q requires context lookup", info.Name)
			}
			resolved, err := s.lookup(labelCtx)
			if err != nil {
				return nil, fmt.Errorf("container %q: %w", info.Name, err)
			}
			ctxResolved = resolved
		} else {
			if strings.TrimSpace(ctxResolved.Engine) == "" || strings.TrimSpace(ctxResolved.Token) == "" {
				return nil, fmt.Errorf("container %q requires a default context", info.Name)
			}
		}
		desired, err := ParseDesiredTunnels(info, s.network, ctxResolved)
		if err != nil {
			return nil, fmt.Errorf("container %q: %w", info.Name, err)
		}
		out = append(out, desired...)
	}
	s.logger.Info("Docker configuration loaded", "tunnels", len(out))
	return out, nil
}

func (s *Source) Watch(ctx context.Context) (<-chan struct{}, error) {
	f := make(client.Filters).Add("type", "container")
	result := s.client.Events(ctx, client.EventsListOptions{Filters: f})
	s.logger.Info("Watching Docker events")
	out := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(out)
				return
			case err, ok := <-result.Err:
				if !ok {
					close(out)
					return
				}
				s.logger.Warn("Docker events error", "error", err)
			case msg, ok := <-result.Messages:
				if !ok {
					close(out)
					return
				}
				if shouldTriggerDockerEvent(msg) {
					select {
					case out <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return out, nil
}

func (s *Source) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func containerName(c container.Summary) string {
	if len(c.Names) > 0 {
		name := strings.TrimPrefix(c.Names[0], "/")
		if name != "" {
			return name
		}
	}
	if c.ID != "" {
		if len(c.ID) > 12 {
			return c.ID[:12]
		}
		return c.ID
	}
	return "unknown"
}

func containerNetworks(c container.Summary) map[string]string {
	if c.NetworkSettings == nil || c.NetworkSettings.Networks == nil {
		return nil
	}
	out := make(map[string]string, len(c.NetworkSettings.Networks))
	for name, settings := range c.NetworkSettings.Networks {
		if settings == nil {
			continue
		}
		if settings.IPAddress.IsValid() {
			out[name] = settings.IPAddress.String()
		}
	}
	return out
}

func shouldTriggerDockerEvent(msg events.Message) bool {
	if msg.Type != "container" {
		return false
	}
	if msg.Action == "" {
		return false
	}
	switch msg.Action {
	case "start", "stop", "die", "destroy", "rename", "update", "create", "connect", "disconnect", "pause", "unpause":
		return true
	default:
		return false
	}
}

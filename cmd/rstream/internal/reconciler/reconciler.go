// See LICENSE file in the project root for license information.

package reconciler

import (
	"context"
	"log/slog"
	"reflect"
	"sync"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

type Starter interface {
	Start(ctx context.Context, desired runmodel.DesiredTunnel) (runmodel.Handle, error)
}

type Reconciler struct {
	ctx     context.Context
	logger  *slog.Logger
	starter Starter

	mu     sync.Mutex
	active map[string]*activeTunnel
}

type activeTunnel struct {
	desired runmodel.DesiredTunnel
	handle  runmodel.Handle
}

func New(ctx context.Context, starter Starter, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		ctx:     ctx,
		logger:  logger,
		starter: starter,
		active:  make(map[string]*activeTunnel),
	}
}

func (r *Reconciler) Reconcile(desired []runmodel.DesiredTunnel) error {
	if r == nil {
		return nil
	}
	desiredMap := make(map[string]runmodel.DesiredTunnel, len(desired))
	for _, d := range desired {
		if d.Name == "" {
			return errInvalidTunnelName
		}
		if _, ok := desiredMap[d.Name]; ok {
			return errDuplicateTunnelName
		}
		desiredMap[d.Name] = d
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	updated := 0
	created := 0
	for name, active := range r.active {
		if _, ok := desiredMap[name]; !ok {
			r.logger.Info("Tunnel removed", "tunnel", name)
			_ = active.handle.Stop()
			delete(r.active, name)
			removed++
		}
	}
	for name, d := range desiredMap {
		if active, ok := r.active[name]; ok {
			if reflect.DeepEqual(active.desired, d) {
				continue
			}
			r.logger.Info("Tunnel updated", "tunnel", name)
			r.logger.Debug("Tunnel spec changed", "tunnel", name, "current", runmodel.Summary(active.desired), "desired", runmodel.Summary(d))
			_ = active.handle.Stop()
			delete(r.active, name)
			updated++
		}
		handle, err := r.starter.Start(r.ctx, d)
		if err != nil {
			r.logger.Warn("Failed to start tunnel", "tunnel", name, "error", err)
			continue
		}
		r.active[name] = &activeTunnel{desired: d, handle: handle}
		created++
	}
	r.logger.Info("Reconciled desired state", "tunnels", len(desiredMap), "created", created, "updated", updated, "removed", removed)
	return nil
}

func (r *Reconciler) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, active := range r.active {
		r.logger.Info("Tunnel stopped", "tunnel", name)
		_ = active.handle.Stop()
		delete(r.active, name)
	}
}

var (
	errInvalidTunnelName   = &reconcilerError{msg: "tunnel name is required"}
	errDuplicateTunnelName = &reconcilerError{msg: "duplicate tunnel name"}
)

type reconcilerError struct{ msg string }

func (e *reconcilerError) Error() string { return e.msg }

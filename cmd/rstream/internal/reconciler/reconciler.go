// See LICENSE file in the project root for license information.

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

type Starter interface {
	Start(ctx context.Context, desired runmodel.DesiredTunnel) (runmodel.Handle, error)
}

type Reconciler struct {
	ctx      context.Context
	logger   *slog.Logger
	starter  Starter
	requests chan reconcileRequest
	stopped  chan struct{}
}

type activeTunnel struct {
	desired runmodel.DesiredTunnel
	handle  runmodel.Handle
}

type reconcileRequest struct {
	desired []runmodel.DesiredTunnel
	stop    bool
	done    chan error
}

func New(ctx context.Context, starter Starter, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Reconciler{
		ctx:      ctx,
		logger:   logger,
		starter:  starter,
		requests: make(chan reconcileRequest),
		stopped:  make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *Reconciler) Reconcile(desired []runmodel.DesiredTunnel) error {
	if r == nil {
		return nil
	}
	desiredCopy := make([]runmodel.DesiredTunnel, 0, len(desired))
	desiredMap := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		if d.Name == "" {
			return errInvalidTunnelName
		}
		if _, ok := desiredMap[d.Name]; ok {
			return errDuplicateTunnelName
		}
		desiredMap[d.Name] = struct{}{}
		desiredCopy = append(desiredCopy, runmodel.CloneDesired(d))
	}
	done := make(chan error, 1)
	request := reconcileRequest{desired: desiredCopy, done: done}
	select {
	case r.requests <- request:
	case <-r.ctx.Done():
		return context.Cause(r.ctx)
	case <-r.stopped:
		return net.ErrClosed
	}
	select {
	case err := <-done:
		return err
	case <-r.ctx.Done():
		return context.Cause(r.ctx)
	case <-r.stopped:
		return net.ErrClosed
	}
}

func (r *Reconciler) reconcile(active map[string]*activeTunnel, desired []runmodel.DesiredTunnel) error {
	desiredMap := make(map[string]runmodel.DesiredTunnel, len(desired))
	for _, d := range desired {
		desiredMap[d.Name] = d
	}
	removed := 0
	updated := 0
	created := 0
	failed := 0
	var reconcileErr error
	for name, current := range active {
		if _, ok := desiredMap[name]; !ok {
			r.logger.Info("Tunnel removed", "tunnel", name)
			if err := current.handle.Stop(); err != nil {
				failed++
				reconcileErr = errors.Join(reconcileErr, fmt.Errorf("stop removed tunnel %q: %w", name, err))
				continue
			}
			delete(active, name)
			removed++
		}
	}
	for name, d := range desiredMap {
		if current, ok := active[name]; ok {
			if runmodel.EqualDesired(current.desired, d) {
				continue
			}
			r.logger.Info("Tunnel updated", "tunnel", name)
			r.logger.Debug("Tunnel spec changed", "tunnel", name, "current", runmodel.Summary(current.desired), "desired", runmodel.Summary(d))
			if err := current.handle.Stop(); err != nil {
				failed++
				reconcileErr = errors.Join(reconcileErr, fmt.Errorf("stop updated tunnel %q: %w", name, err))
				continue
			}
			delete(active, name)
			updated++
		}
		handle, err := r.starter.Start(r.ctx, d)
		if err != nil {
			r.logger.Warn("Failed to start tunnel", "tunnel", name, "error", err)
			failed++
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("start tunnel %q: %w", name, err))
			continue
		}
		if handle == nil {
			failed++
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("start tunnel %q: starter returned a nil handle", name))
			continue
		}
		active[name] = &activeTunnel{desired: d, handle: handle}
		created++
	}
	r.logger.Info("Reconciled desired state", "tunnels", len(desiredMap), "created", created, "updated", updated, "removed", removed, "failed", failed)
	return reconcileErr
}

func (r *Reconciler) Stop() {
	if r == nil {
		return
	}
	done := make(chan error, 1)
	select {
	case r.requests <- reconcileRequest{stop: true, done: done}:
		select {
		case <-done:
		case <-r.stopped:
		}
	case <-r.stopped:
	}
}

func (r *Reconciler) stop(active map[string]*activeTunnel) {
	for name, current := range active {
		r.logger.Info("Tunnel stopped", "tunnel", name)
		if err := current.handle.Stop(); err != nil {
			r.logger.Warn("Failed to stop tunnel", "tunnel", name, "error", err)
		}
		delete(active, name)
	}
}

func (r *Reconciler) run() {
	active := make(map[string]*activeTunnel)
	defer func() {
		r.stop(active)
		close(r.stopped)
	}()
	for {
		select {
		case <-r.ctx.Done():
			return
		case request := <-r.requests:
			if request.stop {
				request.done <- nil
				return
			}
			request.done <- r.reconcile(active, request.desired)
		}
	}
}

var (
	errInvalidTunnelName   = &reconcilerError{msg: "tunnel name is required"}
	errDuplicateTunnelName = &reconcilerError{msg: "duplicate tunnel name"}
)

type reconcilerError struct{ msg string }

func (e *reconcilerError) Error() string { return e.msg }

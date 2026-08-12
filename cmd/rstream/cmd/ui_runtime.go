// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

type uiRuntimeSwitchResult struct {
	ctx          context.Context
	cancel       context.CancelFunc
	runtime      *resolvedRuntime
	client       *rstream.Client
	clientCloser *ownedRstreamClient
	store        *uiStore
	done         <-chan struct{}
	connection   uiConnectionInfo
	warning      string
	persisted    bool
}

func (r *uiRuntimeSwitchResult) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	err := r.clientCloser.Close()
	if r.done != nil {
		<-r.done
	}
	return err
}

func closeUIRuntimeSwitchResultLogged(result *uiRuntimeSwitchResult) {
	if err := result.Close(); err != nil {
		slog.Warn("failed to close prepared UI runtime", "error", err)
	}
}

func (u *uiApp) switchTarget(target uiTarget, persist bool) {
	if u.session != nil || u.activePage == uiPageSession {
		u.setMessage("Close the WebTTY session before switching context")
		return
	}
	if u.ctx == nil || u.ctx.Err() != nil {
		u.setMessage("The UI is shutting down")
		return
	}
	if u.switchCancel != nil {
		u.setMessage("A context switch is already in progress")
		return
	}
	u.switchGen++
	generation := u.switchGen
	switchCtx, switchCancel := context.WithCancel(u.ctx)
	transport := u.store.transport
	readyTimeout := u.readyTimeout
	u.switchCancel = switchCancel
	u.closeTargetPicker()
	u.beginRuntimeSwitchPresentation(target.displayName())
	started := u.async.Go(func() {
		result, err := u.prepareRuntimeSwitch(switchCtx, switchCancel, target, persist, transport, readyTimeout)
		u.postUpdate(u.ctx, uiUpdate{apply: func() {
			if generation != u.switchGen {
				closeUIRuntimeSwitchResultLogged(result)
				return
			}
			u.switchCancel = nil
			if err != nil {
				message := fmt.Sprintf("Could not switch to %s: %v", target.displayName(), err)
				if result != nil && result.persisted {
					message = fmt.Sprintf("Default saved, but the UI could not switch to %s: %v", target.displayName(), err)
				}
				u.finishRuntimeSwitchPresentation(message)
				return
			}
			u.activateRuntime(result)
		}, discard: func() { closeUIRuntimeSwitchResultLogged(result) }})
	})
	if !started {
		switchCancel()
		u.switchCancel = nil
		u.finishRuntimeSwitchPresentation("Could not switch context: UI is shutting down")
	}
}

func (u *uiApp) prepareRuntimeSwitch(ctx context.Context, cancel context.CancelFunc, target uiTarget, persist bool, transport string, readyTimeout time.Duration) (*uiRuntimeSwitchResult, error) {
	runtime, connection, warning, persisted, err := u.resolver.prepareTarget(ctx, target, persist)
	result := &uiRuntimeSwitchResult{ctx: ctx, cancel: cancel, runtime: runtime, connection: connection, warning: warning, persisted: persisted}
	if err != nil {
		cancel()
		return result, err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		cancel()
		return result, fmt.Errorf("create Engine client: %w", err)
	}
	store := newUIStore(transport)
	ready := make(chan error, 1)
	result.client = client
	result.clientCloser = ownRstreamClient(client)
	result.store = store
	result.done = startUIStore(ctx, store, client, ready)
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return result, errors.Join(ctx.Err(), result.Close())
	case err := <-ready:
		if err != nil {
			return result, errors.Join(fmt.Errorf("connect Engine inventory: %w", err), result.Close())
		}
		return result, nil
	case <-timer.C:
		return result, errors.Join(fmt.Errorf("engine inventory did not become ready within %s", readyTimeout), result.Close())
	}
}

func (u *uiApp) activateRuntime(result *uiRuntimeSwitchResult) {
	if result == nil || result.runtime == nil || result.client == nil || result.store == nil {
		closeUIRuntimeSwitchResultLogged(result)
		u.finishRuntimeSwitchPresentation("Could not activate context: prepared runtime is incomplete")
		return
	}
	previousCancel := u.runtimeCancel
	previousClient := u.runtimeClient
	previousDone := u.runtimeDone
	u.runtime = result.runtime
	u.client = result.client
	u.store = result.store
	u.connection = result.connection
	u.switchingTo = ""
	u.runtimeCancel = result.cancel
	u.runtimeClient = result.clientCloser
	u.runtimeDone = result.done
	result.cancel = nil
	result.clientCloser = nil
	result.done = nil
	u.runtimeGen++
	generation := u.runtimeGen
	u.state.ClientID = ""
	u.state.TunnelID = ""
	u.clientRows = nil
	u.tunnelRows = nil
	u.webttyRows = nil
	u.snapshot = result.store.snapshot()
	if strings.TrimSpace(result.warning) != "" {
		u.state.Message = result.warning
	} else {
		u.state.Message = ""
	}
	u.async.Go(func() { u.watchStore(result.ctx, generation, result.store) })
	u.refreshSnapshot(u.snapshot)
	if previousCancel != nil {
		previousCancel()
	}
	u.async.Go(func() {
		if err := previousClient.Close(); err != nil {
			slog.Warn("failed to close replaced UI runtime client", "error", err)
		}
		if previousDone != nil {
			<-previousDone
		}
	})
}

func (u *uiApp) beginRuntimeSwitchPresentation(target string) {
	u.switchingTo = strings.TrimSpace(target)
	u.state.Message = ""
	if u.activePage == uiPageInventory && u.table != nil && u.detail != nil {
		u.renderInventory()
	}
	u.refreshChrome()
}

func (u *uiApp) finishRuntimeSwitchPresentation(message string) {
	u.switchingTo = ""
	u.state.Message = strings.TrimSpace(message)
	if u.activePage == uiPageInventory && u.table != nil && u.detail != nil {
		u.renderInventory()
	}
	u.refreshChrome()
}

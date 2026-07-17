// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

type uiRuntimeSwitchResult struct {
	ctx        context.Context
	cancel     context.CancelFunc
	runtime    *resolvedRuntime
	client     *rstream.Client
	store      *uiStore
	connection uiConnectionInfo
	warning    string
	persisted  bool
}

func (u *uiApp) switchTarget(target uiTarget, persist bool) {
	if u.session != nil || u.activePage == uiPageSession {
		u.setMessage("Close the WebTTY session before switching context")
		return
	}
	if u.switchCancel != nil {
		u.switchCancel()
	}
	u.switchGen++
	generation := u.switchGen
	switchCtx, switchCancel := context.WithCancel(u.ctx)
	transport := u.store.transport
	readyTimeout := u.readyTimeout
	u.switchCancel = switchCancel
	u.closeTargetPicker()
	u.beginRuntimeSwitchPresentation(target.displayName())
	go func() {
		result, err := u.prepareRuntimeSwitch(switchCtx, switchCancel, target, persist, transport, readyTimeout)
		if u.ctx.Err() != nil {
			if result != nil {
				result.cancel()
			}
			return
		}
		u.app.QueueUpdateDraw(func() {
			if generation != u.switchGen {
				if result != nil {
					result.cancel()
				}
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
		})
	}()
}

func (u *uiApp) prepareRuntimeSwitch(ctx context.Context, cancel context.CancelFunc, target uiTarget, persist bool, transport string, readyTimeout time.Duration) (*uiRuntimeSwitchResult, error) {
	runtime, connection, warning, persisted, err := u.resolver.prepareTarget(target, persist)
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
	result.store = store
	go store.run(ctx, client, ready)
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		cancel()
		return result, ctx.Err()
	case err := <-ready:
		if err != nil {
			cancel()
			return result, fmt.Errorf("connect Engine inventory: %w", err)
		}
		return result, nil
	case <-timer.C:
		cancel()
		return result, fmt.Errorf("Engine inventory did not become ready within %s", readyTimeout)
	}
}

func (u *uiApp) activateRuntime(result *uiRuntimeSwitchResult) {
	if result == nil || result.runtime == nil || result.client == nil || result.store == nil {
		if result != nil && result.cancel != nil {
			result.cancel()
		}
		u.finishRuntimeSwitchPresentation("Could not activate context: prepared runtime is incomplete")
		return
	}
	previousCancel := u.runtimeCancel
	u.runtime = result.runtime
	u.client = result.client
	u.store = result.store
	u.connection = result.connection
	u.switchingTo = ""
	u.runtimeCancel = result.cancel
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
	go u.watchStore(result.ctx, generation, result.store)
	u.refreshSnapshot(u.snapshot)
	if previousCancel != nil {
		previousCancel()
	}
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

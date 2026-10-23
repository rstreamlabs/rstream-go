// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
)

type unixChildProcessGroup struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	released bool
}

func ownChildProcess(cmd *exec.Cmd) childProcessGroup {
	if !processGroupSupported {
		return nil
	}
	return &unixChildProcessGroup{cmd: cmd}
}

func (g *unixChildProcessGroup) interrupt() error {
	return g.signal(syscall.SIGINT)
}

func (g *unixChildProcessGroup) kill() error {
	return g.signal(syscall.SIGKILL)
}

func (g *unixChildProcessGroup) signal(signal syscall.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return nil
	}
	if signal == syscall.SIGINT && g.cmd.SysProcAttr != nil && g.cmd.SysProcAttr.Setsid {
		signal = syscall.SIGHUP
	}
	return signalOwnedChild(g.cmd, signal)
}

func (g *unixChildProcessGroup) close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return nil
	}
	err := signalOwnedChild(g.cmd, syscall.SIGKILL)
	g.released = true
	return err
}

func (g *unixChildProcessGroup) waitForExit() error {
	if err := waitChildExited(g.cmd.Process.Pid); err != nil {
		if errors.Is(err, syscall.ECHILD) {
			g.mu.Lock()
			g.released = true
			g.mu.Unlock()
		}
		return err
	}
	// The leader stays unreaped until its group is stopped and released. No
	// later signal can target a reused process-group ID after cmd.Wait.
	return g.close()
}

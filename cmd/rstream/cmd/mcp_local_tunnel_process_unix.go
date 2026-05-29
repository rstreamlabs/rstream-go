// See LICENSE file in the project root for license information.

//go:build !windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureMCPLocalTunnelProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func mcpLocalTunnelProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func terminateMCPLocalTunnelProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = process.Kill()
		return err
	}
	for i := 0; i < 25; i++ {
		if !mcpLocalTunnelProcessRunning(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return process.Kill()
}

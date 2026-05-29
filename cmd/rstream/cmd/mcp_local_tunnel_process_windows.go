// See LICENSE file in the project root for license information.

//go:build windows

package cmd

import (
	"os"
	"os/exec"
)

func configureMCPLocalTunnelProcess(cmd *exec.Cmd) {}

func mcpLocalTunnelProcessRunning(pid int) bool {
	return pid > 0
}

func terminateMCPLocalTunnelProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

// See LICENSE file in the project root for license information.

//go:build windows

package cmd

import (
	"os"
	"os/exec"
)

func configureMCPPreviewProcess(cmd *exec.Cmd) {}

func mcpPreviewProcessRunning(pid int) bool {
	return pid > 0
}

func terminateMCPPreviewProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

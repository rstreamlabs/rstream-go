// See LICENSE file in the project root for license information.

//go:build unix

package mtlsexec

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) func() {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = kill
	return func() { _ = kill() }
}

// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"os/exec"
	"syscall"
)

func SetupCredential(cmd *exec.Cmd, ui *UserInfo) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: ui.UID, Gid: ui.GID},
	}
	return nil
}

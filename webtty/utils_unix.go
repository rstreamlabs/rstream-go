// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"syscall"
)

func SetupCredential(cmd *exec.Cmd, ui *UserInfo) error {
	current, err := user.Current()
	if err == nil {
		uid, gid, err := lookupNumericUserIDs(current)
		if err == nil && uid == ui.UID && gid == ui.GID {
			return nil
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: ui.UID, Gid: ui.GID},
	}
	return nil
}

func interruptChildProcess(cmd *exec.Cmd) error {
	err := cmd.Process.Signal(os.Interrupt)
	if err != nil && errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func isStreamEOS(err error, usingPTY bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	return usingPTY && runtime.GOOS == "linux" && errors.Is(err, syscall.EIO)
}

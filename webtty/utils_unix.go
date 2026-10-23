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
		Credential: &syscall.Credential{Uid: ui.UID, Gid: ui.GID, Groups: ui.Groups},
	}
	return nil
}

func interruptChildProcess(cmd *exec.Cmd) error {
	return signalOwnedChild(cmd, syscall.SIGINT)
}

func prepareChildProcess(cmd *exec.Cmd, allocateTTY bool) {
	if allocateTTY || !processGroupSupported {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func startChildProcess(cmd *exec.Cmd) (childProcessGroup, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return ownChildProcess(cmd), nil
}

func killChildProcess(cmd *exec.Cmd) error {
	return signalOwnedChild(cmd, syscall.SIGKILL)
}

func signalOwnedChild(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if cmd.SysProcAttr != nil && ((cmd.SysProcAttr.Setpgid && cmd.SysProcAttr.Pgid == 0) || cmd.SysProcAttr.Setsid) {
		err := syscall.Kill(-cmd.Process.Pid, signal)
		if errors.Is(err, syscall.ESRCH) || processGroupIsEmpty(cmd.Process.Pid, err) {
			return nil
		}
		return err
	}
	err := cmd.Process.Signal(signal)
	if err != nil && errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func hangupChildProcess(cmd *exec.Cmd) error {
	return signalOwnedChild(cmd, syscall.SIGHUP)
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

func isBrokenPipeError(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}

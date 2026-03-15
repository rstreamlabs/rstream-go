// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"

	"golang.org/x/sys/windows"
)

const windowsProcessStillActive = 259

func SetupCredential(cmd *exec.Cmd, ui *UserInfo) error {
	return fmt.Errorf("user switching is not supported on %s", runtime.GOOS)
}

func interruptChildProcess(cmd *exec.Cmd) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(handle)
	err = windows.TerminateProcess(handle, windows.CTRL_C_EVENT)
	if err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		var exitCode uint32
		if exitErr := windows.GetExitCodeProcess(handle, &exitCode); exitErr == nil && exitCode != windowsProcessStillActive {
			return nil
		}
	}
	return err
}

func isStreamEOS(err error, _ bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	return errors.Is(err, windows.ERROR_BROKEN_PIPE)
}

// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"fmt"
	"os/exec"
	"runtime"
)

func SetupCredential(cmd *exec.Cmd, ui *UserInfo) error {
	return fmt.Errorf("user switching is not supported on %s", runtime.GOOS)
}

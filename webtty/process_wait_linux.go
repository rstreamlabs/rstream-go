// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"

	"golang.org/x/sys/unix"
)

const processGroupSupported = true

func waitChildExited(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

func processGroupIsEmpty(_ int, _ error) bool {
	return false
}

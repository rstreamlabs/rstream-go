// See LICENSE file in the project root for license information.

package webtty

import "os/exec"

type childProcessGroup interface {
	interrupt() error
	kill() error
	close() error
	waitForExit() error
}

func killOwnedChild(cmd *exec.Cmd, group childProcessGroup) error {
	if group != nil {
		return group.kill()
	}
	return killChildProcess(cmd)
}

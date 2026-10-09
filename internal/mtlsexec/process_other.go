// See LICENSE file in the project root for license information.

//go:build !unix

package mtlsexec

import "os/exec"

func configureProcess(cmd *exec.Cmd) func() {
	return func() {}
}

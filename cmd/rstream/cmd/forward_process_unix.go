// See LICENSE file in the project root for license information.

//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

func newForwardProcessGroup() error {
	if syscall.Getpgrp() == os.Getpid() {
		return nil
	}
	return syscall.Setpgid(0, 0)
}

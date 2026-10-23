// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const processGroupSupported = true

// SZOMB from Darwin's sys/proc.h. A zombie remains owned until cmd.Wait.
const darwinProcessZombie = 5

func waitChildExited(pid int) error {
	// Darwin emits NOTE_EXIT before the process becomes waitable. waitid with
	// WNOWAIT waits for that state without releasing the process-group ID.
	// siginfo_t is 104 bytes on both supported 64-bit Darwin architectures;
	// the result is deliberately left to cmd.Wait rather than decoded here.
	var info [13]uint64
	const waitPID = 1
	for {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, waitPID, uintptr(pid), uintptr(unsafe.Pointer(&info)), unix.WEXITED|unix.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}

func processGroupIsEmpty(pid int, signalError error) bool {
	if !errors.Is(signalError, unix.EPERM) {
		return false
	}
	// A single-record buffer fails with ENOMEM if another member exists.
	info, err := unix.SysctlKinfoProc("kern.proc.pgrp", pid)
	return err == nil && int(info.Proc.P_pid) == pid && info.Proc.P_stat == darwinProcessZombie
}

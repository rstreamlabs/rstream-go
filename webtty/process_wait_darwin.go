// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const processGroupSupported = true

// SZOMB and P_WEXIT from Darwin's sys/proc.h. Both states are irreversible;
// group signalling can stop finding a process before it becomes a zombie.
const (
	darwinProcessZombie  = 5
	darwinProcessExiting = 0x2000
)

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
	if pid <= 0 || !errors.Is(signalError, unix.EPERM) {
		return false
	}
	// Darwin skips exited processes when signalling a group. Inspect a bounded snapshot,
	// including the unreaped leader, without retrying a growing process table.
	// Oversized or incomplete snapshots retain the original permission error.
	const kernProc, kernProcGroup = 14, 2
	query := [4]int32{unix.CTL_KERN, kernProc, kernProcGroup, int32(pid)}
	var members [256]unix.KinfoProc
	size := uintptr(unsafe.Sizeof(members))
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&query[0])), uintptr(len(query)), uintptr(unsafe.Pointer(&members[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 || size > unsafe.Sizeof(members) || size%unix.SizeofKinfoProc != 0 {
		return false
	}
	leaderFound := false
	for _, member := range members[:size/unix.SizeofKinfoProc] {
		if int(member.Eproc.Pgid) != pid || (member.Proc.P_stat != darwinProcessZombie && member.Proc.P_flag&darwinProcessExiting == 0) {
			return false
		}
		leaderFound = leaderFound || int(member.Proc.P_pid) == pid
	}
	return leaderFound
}

// See LICENSE file in the project root for license information.

//go:build !windows

package filesystem

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Lock an already rooted directory inode, never a replaceable pathname. New
// external journals coordinate through their private directory so confinement
// need not grant file-lock permission on the exported root.
func (f *Local) openMutationLock() (*os.File, error) {
	if f.privateMutationLock {
		return f.journal.Open(".")
	}
	return f.root.Open(".")
}
func tryMutationLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func unlockMutation(file *os.File) { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }

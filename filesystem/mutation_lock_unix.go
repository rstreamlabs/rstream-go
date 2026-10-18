// See LICENSE file in the project root for license information.

//go:build !windows

package filesystem

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Lock the already rooted directory inode; no pathname lock can be replaced or
// accidentally exported. Renamed and symlinked aliases share this same lock.
func (f *Local) openMutationLock() (*os.File, error) { return f.root.Open(".") }
func tryMutationLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func unlockMutation(file *os.File) { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }

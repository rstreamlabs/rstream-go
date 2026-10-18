// See LICENSE file in the project root for license information.

//go:build windows

package filesystem

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func (f *Local) openMutationLock() (*os.File, error) {
	file, err := f.root.OpenFile(mutationLockName, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	current, err := f.root.Lstat(mutationLockName)
	opened, openedErr := file.Stat()
	if err != nil || openedErr != nil || !current.Mode().IsRegular() || !os.SameFile(current, opened) {
		file.Close()
		return nil, os.ErrPermission
	}
	return file, nil
}
func tryMutationLock(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
func unlockMutation(file *os.File) {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

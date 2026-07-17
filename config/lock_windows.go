// See LICENSE file in the project root for license information.

//go:build windows

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type FileLock struct {
	file       *os.File
	path       string
	overlapped windows.Overlapped
}

func LockFile(path string) (*FileLock, error) {
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		lock := &FileLock{file: f, path: path}
		if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &lock.overlapped); err != nil {
			_ = f.Close()
			return nil, err
		}
		lockedInfo, err := f.Stat()
		if err != nil {
			_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &lock.overlapped)
			_ = f.Close()
			return nil, err
		}
		currentInfo, statErr := os.Stat(path)
		if statErr == nil && os.SameFile(lockedInfo, currentInfo) {
			return lock, nil
		}
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &lock.overlapped)
		_ = f.Close()
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
}

func (l *FileLock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	if l.path != "" {
		_ = os.Remove(l.path)
	}
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	_ = l.file.Close()
}

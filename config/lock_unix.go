// See LICENSE file in the project root for license information.

//go:build !windows

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type FileLock struct {
	file *os.File
	path string
}

func LockFile(path string) (*FileLock, error) {
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			_ = f.Close()
			return nil, err
		}
		lockedInfo, err := f.Stat()
		if err != nil {
			_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
			_ = f.Close()
			return nil, err
		}
		currentInfo, statErr := os.Stat(path)
		if statErr == nil && os.SameFile(lockedInfo, currentInfo) {
			return &FileLock{file: f, path: path}, nil
		}
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
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
	_ = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	_ = l.file.Close()
}

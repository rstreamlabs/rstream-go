// See LICENSE file in the project root for license information.

//go:build windows

package config

import "os"

type FileLock struct {
	file *os.File
	path string
}

func LockFile(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &FileLock{file: f, path: path}, nil
}

func (l *FileLock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close()
	if l.path != "" {
		_ = os.Remove(l.path)
	}
}

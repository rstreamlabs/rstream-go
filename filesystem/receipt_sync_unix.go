// See LICENSE file in the project root for license information.
//go:build !windows

package filesystem

import (
	"fmt"
	"os"
	"syscall"
)

const durableUploads = true

func uploadFileIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("file identity unavailable")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

func (f *Local) syncDirectory(name string) error {
	directory, err := f.root.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

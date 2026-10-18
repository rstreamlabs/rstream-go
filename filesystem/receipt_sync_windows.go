// See LICENSE file in the project root for license information.
//go:build windows

package filesystem

import (
	"errors"
	"os"
)

const durableUploads = false

func uploadFileIdentity(os.FileInfo) (string, error) {
	return "", errors.New("durable upload identity is not supported on Windows")
}

func (f *Local) syncDirectory(string) error {
	return errors.New("durable directory synchronization is not supported on Windows")
}

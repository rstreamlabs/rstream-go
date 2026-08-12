// See LICENSE file in the project root for license information.

//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package webtty

import (
	"context"
	"os"
)

func clientFileStdinRead(file *os.File) func(context.Context, []byte) (int, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return func(ctx context.Context, buffer []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return file.Read(buffer)
	}
}

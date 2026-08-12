// See LICENSE file in the project root for license information.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package webtty

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func clientFileStdinRead(file *os.File) func(context.Context, []byte) (int, error) {
	fd := int32(file.Fd())
	return func(ctx context.Context, buffer []byte) (int, error) {
		pollFDs := []unix.PollFd{{Fd: fd, Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		for {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			ready, err := unix.Poll(pollFDs, int(defaultStdinReadPollPeriod/time.Millisecond))
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil {
				return 0, err
			}
			if ready == 0 {
				continue
			}
			return file.Read(buffer)
		}
	}
}

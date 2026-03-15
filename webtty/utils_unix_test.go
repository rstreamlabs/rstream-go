// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"io"
	"runtime"
	"syscall"
	"testing"
)

func TestIsStreamEOS(t *testing.T) {
	if !isStreamEOS(io.EOF, false) {
		t.Fatal("expected io.EOF to be treated as EOS")
	}
	if runtime.GOOS == "linux" && !isStreamEOS(syscall.EIO, true) {
		t.Fatal("expected linux PTY EIO to be treated as EOS")
	}
	if isStreamEOS(syscall.EIO, false) {
		t.Fatal("expected non-PTY EIO to be treated as non-EOS")
	}
}

//go:build unix

// See LICENSE file in the project root for license information.

package filesystem

import "syscall"

const nonblockFlag = syscall.O_NONBLOCK

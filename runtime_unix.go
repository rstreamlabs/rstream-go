// See LICENSE file in the project root for license information.

//go:build !windows
// +build !windows

package rstream

import "golang.org/x/sys/unix"

func runtimeIdentity() Identity {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return Identity{}
	}
	return Identity{
		OS:   utsString(u.Sysname[:]),
		Arch: utsString(u.Machine[:]),
	}
}

func utsString[T ~int8 | ~uint8](data []T) string {
	buf := make([]byte, 0, len(data))
	for _, c := range data {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}

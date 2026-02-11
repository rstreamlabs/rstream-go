// See LICENSE file in the project root for license information.

//go:build windows
// +build windows

package rstream

import (
	"os"
	"strings"
)

func runtimeIdentity() Identity {
	arch := runtimeArchFromEnv()
	return Identity{
		OS:   "windows",
		Arch: arch,
	}
}

func runtimeArchFromEnv() string {
	arch := strings.TrimSpace(os.Getenv("PROCESSOR_ARCHITEW6432"))
	if arch == "" {
		arch = strings.TrimSpace(os.Getenv("PROCESSOR_ARCHITECTURE"))
	}
	return arch
}

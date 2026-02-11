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
	if arch == "" {
		arch = CompiletimeArch()
	}
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
	arch = strings.ToLower(arch)
	switch arch {
	case "amd64", "x64":
		return "x86_64"
	case "x86", "i386", "i686":
		return "x86"
	case "arm64", "aarch64":
		return "arm64"
	case "arm":
		return "arm"
	default:
		return arch
	}
}

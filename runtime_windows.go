// See LICENSE file in the project root for license information.

//go:build windows
// +build windows

package rstream

import "golang.org/x/sys/windows"

func runtimeIdentity() Identity {
	var info windows.SystemInfo
	windows.GetSystemInfo(&info)
	return Identity{
		OS:   "windows",
		Arch: processorArchitecture(info.ProcessorArchitecture),
	}
}

func processorArchitecture(arch uint16) string {
	switch arch {
	case windows.PROCESSOR_ARCHITECTURE_AMD64:
		return "x86_64"
	case windows.PROCESSOR_ARCHITECTURE_INTEL:
		return "x86"
	case windows.PROCESSOR_ARCHITECTURE_ARM64:
		return "arm64"
	case windows.PROCESSOR_ARCHITECTURE_ARM:
		return "arm"
	default:
		return "unknown"
	}
}

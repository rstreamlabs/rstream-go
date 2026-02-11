// See LICENSE file in the project root for license information.

package rstream

import (
	"strings"
	"sync"
)

// Identity represents the OS/arch pair.
type Identity struct {
	OS   string
	Arch string
}

func normalizeOS(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "darwin", "macos", "macosx", "osx":
		return "macos"
	case "windows", "win32", "win":
		return "windows"
	case "linux":
		return "linux"
	case "netbsd":
		return "netbsd"
	case "openbsd":
		return "openbsd"
	case "freebsd":
		return "freebsd"
	default:
		return ""
	}
}

func normalizeIdentity(identity Identity) Identity {
	identity.OS = normalizeOS(identity.OS)
	return identity
}

// CompiletimeIdentity returns the OS/arch values embedded at build time.
func CompiletimeIdentity() Identity {
	return normalizeIdentity(Identity{
		OS:   OS,
		Arch: Arch,
	})
}

// CompiletimeOS returns the OS value embedded at build time.
func CompiletimeOS() string {
	return CompiletimeIdentity().OS
}

// CompiletimeArch returns the arch value embedded at build time.
func CompiletimeArch() string {
	return CompiletimeIdentity().Arch
}

var runtimeIdentityOnce sync.Once
var runtimeIdentityValue Identity

// RuntimeIdentity returns the OS/arch detected at runtime.
func RuntimeIdentity() Identity {
	runtimeIdentityOnce.Do(func() {
		runtimeIdentityValue = normalizeIdentity(runtimeIdentity())
	})
	return runtimeIdentityValue
}

// RuntimeOS returns the OS detected at runtime.
func RuntimeOS() string {
	return RuntimeIdentity().OS
}

// RuntimeArch returns the arch detected at runtime.
func RuntimeArch() string {
	return RuntimeIdentity().Arch
}

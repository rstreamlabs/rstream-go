// See LICENSE file in the project root for license information.

package rstream

import "sync"

// Identity represents the OS/arch pair.
type Identity struct {
	OS   string
	Arch string
}

// CompiletimeIdentity returns the OS/arch values embedded at build time.
func CompiletimeIdentity() Identity {
	return Identity{
		OS:   OS,
		Arch: Arch,
	}
}

// CompiletimeOS returns the OS value embedded at build time.
func CompiletimeOS() string {
	return OS
}

// CompiletimeArch returns the arch value embedded at build time.
func CompiletimeArch() string {
	return Arch
}

var runtimeIdentityOnce sync.Once
var runtimeIdentityValue Identity

// RuntimeIdentity returns the OS/arch detected at runtime.
func RuntimeIdentity() Identity {
	runtimeIdentityOnce.Do(func() {
		runtimeIdentityValue = runtimeIdentity()
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

// See LICENSE file in the project root for license information.

package rstream

import "testing"

func TestNormalizeOS(t *testing.T) {
	tests := map[string]string{
		" Darwin ": "macos",
		"macOSX":   "macos",
		"win32":    "windows",
		"linux":    "linux",
		"openbsd":  "openbsd",
		"unknown":  "",
		"":         "",
	}
	for input, want := range tests {
		if got := normalizeOS(input); got != want {
			t.Fatalf("normalizeOS(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeIdentity(t *testing.T) {
	got := normalizeIdentity(Identity{OS: "OSX", Arch: "arm64"})
	if got.OS != "macos" || got.Arch != "arm64" {
		t.Fatalf("normalizeIdentity() = %#v", got)
	}
}

func TestRuntimeIdentityExportsNormalizedValues(t *testing.T) {
	_ = CompiletimeOS()
	_ = CompiletimeArch()
	_ = CompiletimeIdentity()
	identity := RuntimeIdentity()
	if identity.OS == "" || identity.Arch == "" {
		t.Fatalf("RuntimeIdentity() = %#v", identity)
	}
	if RuntimeOS() != identity.OS || RuntimeArch() != identity.Arch {
		t.Fatalf("RuntimeOS/RuntimeArch mismatch identity: %#v", identity)
	}
}

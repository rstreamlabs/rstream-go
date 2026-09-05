//go:build rstream_fips

// See LICENSE file in the project root for license information.

package rstream

import "testing"

func TestFIPSProfileRequiresFrozenEnabledModule(t *testing.T) {
	if err := RequireFIPS(); err != nil {
		t.Fatal(err)
	}
	status := CurrentFIPSStatus()
	if !status.Profile || !status.Enabled {
		t.Fatalf("unexpected FIPS status: %#v", status)
	}
	if status.ModuleBuild != FIPSModuleBuild {
		t.Fatalf("Go FIPS module build = %q, want %q", status.ModuleBuild, FIPSModuleBuild)
	}
}

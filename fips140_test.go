// See LICENSE file in the project root for license information.

package rstream

import "testing"

func TestCurrentFIPSStatusMatchesBuildProfile(t *testing.T) {
	status := CurrentFIPSStatus()
	if status.Profile != FIPSProfileEnabled() {
		t.Fatalf("CurrentFIPSStatus().Profile = %v, FIPSProfileEnabled() = %v", status.Profile, FIPSProfileEnabled())
	}
	if status.ModuleVersion == "" {
		t.Fatal("CurrentFIPSStatus().ModuleVersion is empty")
	}
}

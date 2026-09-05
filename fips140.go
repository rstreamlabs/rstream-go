// See LICENSE file in the project root for license information.

package rstream

import "github.com/rstreamlabs/rstream-go/internal/fipsprofile"

// FIPSStatus describes the rstream FIPS profile and the Go cryptographic
// module selected for the current executable.
type FIPSStatus = fipsprofile.Status

// FIPSModuleBuild is the exact Go cryptographic module required by the current
// rstream FIPS profile.
const FIPSModuleBuild = fipsprofile.RequiredModuleBuild

// CurrentFIPSStatus returns the FIPS 140-3 status of the current executable.
func CurrentFIPSStatus() FIPSStatus {
	return fipsprofile.Current()
}

// FIPSProfileEnabled reports whether the rstream FIPS profile was selected at
// build time. Call RequireFIPS to also verify the Go cryptographic module.
func FIPSProfileEnabled() bool {
	return fipsprofile.BuildEnabled()
}

// RequireFIPS verifies that the executable uses the rstream FIPS profile and
// the exact frozen Go cryptographic module with FIPS mode enabled.
func RequireFIPS() error {
	return fipsprofile.Require()
}

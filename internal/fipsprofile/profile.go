// See LICENSE file in the project root for license information.

package fipsprofile

import (
	"crypto/fips140"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
)

// Status describes the compile-time rstream profile and the Go cryptographic
// module selected for the current executable.
type Status struct {
	Profile       bool   `json:"profile"`
	Enabled       bool   `json:"enabled"`
	ModuleVersion string `json:"module_version"`
	ModuleBuild   string `json:"module_build"`
}

// Current returns the FIPS 140-3 status of the current executable.
func Current() Status {
	return Status{
		Profile:       buildEnabled,
		Enabled:       fips140.Enabled(),
		ModuleVersion: fips140.Version(),
		ModuleBuild:   moduleBuildVersion(),
	}
}

// BuildEnabled reports whether the rstream FIPS profile was selected at build
// time. It does not by itself prove that Go FIPS mode is active.
func BuildEnabled() bool {
	return buildEnabled
}

// Require verifies that the executable uses the rstream FIPS profile and a
// frozen Go cryptographic module with FIPS mode enabled.
func Require() error {
	status := Current()
	if !status.Profile {
		return errors.New("rstream FIPS profile is not compiled into this executable")
	}
	if !status.Enabled {
		return errors.New("Go FIPS 140-3 mode is not enabled")
	}
	if status.ModuleVersion == "" || status.ModuleVersion == "latest" {
		return fmt.Errorf("Go FIPS 140-3 module is not frozen: %q", status.ModuleVersion)
	}
	if !strings.HasPrefix(status.ModuleBuild, "v") {
		return fmt.Errorf("Go FIPS 140-3 module build is not frozen: %q", status.ModuleBuild)
	}
	return nil
}

// Unavailable reports that a feature is outside the current rstream FIPS
// profile. Standard builds are unaffected.
func Unavailable(feature string) error {
	if !buildEnabled {
		return nil
	}
	return fmt.Errorf("%s is not available in the rstream FIPS profile", feature)
}

func moduleBuildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "GOFIPS140" {
			return setting.Value
		}
	}
	return ""
}

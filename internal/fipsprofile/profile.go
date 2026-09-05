// See LICENSE file in the project root for license information.

package fipsprofile

import (
	"crypto/fips140"
	"errors"
	"fmt"
	"runtime/debug"
)

const (
	// RequiredModuleVersion is the semantic version reported by crypto/fips140.
	RequiredModuleVersion = "v1.0.0"
	// RequiredModuleBuild is the exact source revision recorded in Go build metadata.
	RequiredModuleBuild = "v1.0.0-c2097c7c"
	// RequiredQUICModuleVersion is the reviewed quic-go implementation used by
	// the phase-two profile. A dependency change requires a new FIPS review.
	RequiredQUICModuleVersion = "v0.60.0"
)

const quicModulePath = "github.com/quic-go/quic-go"

// Status describes the compile-time rstream profile and the Go cryptographic
// module selected for the current executable.
type Status struct {
	Profile       bool   `json:"profile"`
	Enabled       bool   `json:"enabled"`
	ModuleVersion string `json:"module_version"`
	ModuleBuild   string `json:"module_build"`
	QUICVersion   string `json:"quic_version"`
}

// Current returns the FIPS 140-3 status of the current executable.
func Current() Status {
	return Status{
		Profile:       buildEnabled,
		Enabled:       fips140.Enabled(),
		ModuleVersion: fips140.Version(),
		ModuleBuild:   moduleBuildVersion(),
		QUICVersion:   dependencyVersion(quicModulePath),
	}
}

// BuildEnabled reports whether the rstream FIPS profile was selected at build
// time. It does not by itself prove that Go FIPS mode is active.
func BuildEnabled() bool {
	return buildEnabled
}

// Require verifies that the executable uses the rstream FIPS profile and the
// exact frozen Go cryptographic module with FIPS mode enabled.
func Require() error {
	status := Current()
	if !status.Profile {
		return errors.New("rstream FIPS profile is not compiled into this executable")
	}
	if !status.Enabled {
		return errors.New("Go FIPS 140-3 mode is not enabled")
	}
	if status.ModuleVersion != RequiredModuleVersion {
		return fmt.Errorf("Go FIPS 140-3 module version is %q, require %q", status.ModuleVersion, RequiredModuleVersion)
	}
	if status.ModuleBuild != RequiredModuleBuild {
		return fmt.Errorf("Go FIPS 140-3 module build is %q, require %q", status.ModuleBuild, RequiredModuleBuild)
	}
	if status.QUICVersion != RequiredQUICModuleVersion {
		return fmt.Errorf("quic-go module version is %q, require %q", status.QUICVersion, RequiredQUICModuleVersion)
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

func dependencyVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dependency := range info.Deps {
		if dependency.Path != path {
			continue
		}
		if dependency.Replace != nil {
			return dependency.Replace.Version
		}
		return dependency.Version
	}
	return ""
}

// See LICENSE file in the project root for license information.

package fipsprofile

import (
	"runtime/debug"
	"testing"
)

func TestReviewedDependencyIdentityRejectsReplacements(t *testing.T) {
	for _, module := range []struct{ path, version string }{{quicModulePath, RequiredQUICModuleVersion}, {webTransportModulePath, RequiredWebTransportModuleVersion}} {
		t.Run(module.path, func(t *testing.T) {
			if got := unreplacedModuleVersion([]*debug.Module{{Path: module.path, Version: module.version}}, module.path); got != module.version {
				t.Fatalf("reviewed module version = %q, want %q", got, module.version)
			}
			for _, replacement := range []*debug.Module{{Path: "example.org/unreviewed", Version: module.version}, {Path: module.path, Version: module.version}, {Path: "../local-source"}} {
				got := unreplacedModuleVersion([]*debug.Module{{Path: module.path, Version: module.version, Replace: replacement}}, module.path)
				if got != "" {
					t.Fatalf("accepted replacement as reviewed module: %q", got)
				}
			}
			if got := unreplacedModuleVersion(nil, module.path); got != "" {
				t.Fatalf("missing module version = %q", got)
			}
		})
	}
}

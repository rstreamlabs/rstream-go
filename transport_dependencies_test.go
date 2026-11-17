// See LICENSE file in the project root for license information.

package rstream

import (
	"runtime/debug"
	"testing"

	_ "github.com/pion/dtls/v3"
	_ "github.com/pion/dtls/v4"
	_ "github.com/quic-go/connect-ip-go"
	_ "github.com/quic-go/masque-go"
	_ "github.com/quic-go/quic-go"
	_ "github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
)

// Link the reviewed transport dependencies into every standard test binary so
// generated modfiles and workspace overrides cannot silently select another tuple.
func TestReviewedTransportDependencyGraph(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("missing compiled dependency metadata")
	}
	expected := map[string]string{
		"github.com/quic-go/quic-go":         fipsprofile.RequiredQUICModuleVersion,
		"github.com/quic-go/webtransport-go": fipsprofile.RequiredWebTransportModuleVersion,
		"github.com/pion/dtls/v3":            "v3.1.15",
		"github.com/pion/dtls/v4":            "v4.0.2",
		"github.com/quic-go/masque-go":       "v0.6.0",
		"github.com/quic-go/connect-ip-go":   "v0.6.0",
	}
	for _, dependency := range info.Deps {
		version, required := expected[dependency.Path]
		if !required {
			continue
		}
		if dependency.Version != version || dependency.Replace != nil {
			t.Errorf("unreviewed transport dependency %s: version=%s replacement=%+v; require published %s", dependency.Path, dependency.Version, dependency.Replace, version)
		}
		t.Logf("reviewed transport dependency %s %s", dependency.Path, dependency.Version)
		delete(expected, dependency.Path)
	}
	for path := range expected {
		t.Errorf("missing reviewed dependency %s", path)
	}
}

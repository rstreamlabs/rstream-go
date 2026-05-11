// See LICENSE file in the project root for license information.

package webtty

import "testing"

func TestOSReleaseFormattingHelpers(t *testing.T) {
	release := map[string]string{
		"NAME":             "Ubuntu",
		"VERSION":          "24.04 LTS",
		"VERSION_CODENAME": "noble",
		"UBUNTU_CODENAME":  "ignored",
	}
	if got := releaseCodename(release); got != "noble" {
		t.Fatalf("got codename %q", got)
	}
	delete(release, "VERSION_CODENAME")
	if got := releaseCodename(release); got != "ignored" {
		t.Fatalf("got fallback codename %q", got)
	}
	if got := releasePrettyFallback(release); got != "Ubuntu 24.04 LTS" {
		t.Fatalf("got pretty fallback %q", got)
	}
	if got := productPretty("macOS", "15.5"); got != "macOS 15.5" {
		t.Fatalf("got product pretty %q", got)
	}
	if releaseCodename(nil) != "" || releasePrettyFallback(nil) != "" {
		t.Fatalf("nil release should produce empty strings")
	}
}

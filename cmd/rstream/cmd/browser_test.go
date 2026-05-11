// See LICENSE file in the project root for license information.

package cmd

import "testing"

func TestValidateBrowserTarget(t *testing.T) {
	valid := []string{
		"https://example.com/path?x=1",
		" http://127.0.0.1:8080/callback ",
	}
	for _, target := range valid {
		if err := validateBrowserTarget(target); err != nil {
			t.Fatalf("validateBrowserTarget(%q) error = %v", target, err)
		}
	}
	invalid := []string{
		"",
		"javascript:alert(1)",
		"file:///tmp/x",
		"https://example.com/\nwhoami",
		"https:///missing-host",
	}
	for _, target := range invalid {
		if err := validateBrowserTarget(target); err == nil {
			t.Fatalf("validateBrowserTarget(%q) expected error", target)
		}
	}
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"regexp"
	"testing"
)

func TestSessionIDGenerator(t *testing.T) {
	gen := newSessionIDGenerator()
	first := gen.Generate()
	second := gen.Generate()
	if first == second {
		t.Fatal("expected distinct session IDs")
	}
	if len(first) != 24 {
		t.Fatalf("unexpected session ID length: got %d want 24", len(first))
	}
	if !regexp.MustCompile(`^[0-9a-f]{24}$`).MatchString(first) {
		t.Fatalf("unexpected session ID format: %q", first)
	}
}

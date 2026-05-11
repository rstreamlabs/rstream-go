// See LICENSE file in the project root for license information.

package rstream

import (
	"regexp"
	"testing"
)

func TestGenerateStableDomain(t *testing.T) {
	got, ok, err := GenerateStableDomain("https://Project-42.edge.example.com:443")
	if err != nil {
		t.Fatalf("GenerateStableDomain() error = %v", err)
	}
	if !ok {
		t.Fatalf("GenerateStableDomain() ok = false, want true")
	}
	pattern := regexp.MustCompile(`^r[0-9a-f]{8}-project-42\.t\.edge\.example\.com$`)
	if !pattern.MatchString(got) {
		t.Fatalf("GenerateStableDomain() = %q, want %s", got, pattern)
	}
}

func TestGenerateStableDomainRejectsUnsupportedEngines(t *testing.T) {
	tests := []string{
		"",
		"localhost:443",
		"bad_label.example.com:443",
		"project.bad_cluster.example.com:443",
		"[2001:db8::1]:443",
	}
	for _, engine := range tests {
		t.Run(engine, func(t *testing.T) {
			got, ok, err := GenerateStableDomain(engine)
			if err != nil {
				t.Fatalf("GenerateStableDomain() error = %v", err)
			}
			if ok || got != "" {
				t.Fatalf("GenerateStableDomain() = %q, %v; want empty, false", got, ok)
			}
		})
	}
}

func TestMaybeSetGeneratedStableDomain(t *testing.T) {
	t.Run("keeps explicit hostname", func(t *testing.T) {
		props := TunnelProperties{Hostname: StringPtr("custom.example.com")}
		if err := MaybeSetGeneratedStableDomain(&props, "project.edge.example.com:443"); err != nil {
			t.Fatalf("MaybeSetGeneratedStableDomain() error = %v", err)
		}
		if props.Hostname == nil || *props.Hostname != "custom.example.com" {
			t.Fatalf("Hostname = %#v, want explicit hostname", props.Hostname)
		}
	})
	t.Run("keeps legacy host", func(t *testing.T) {
		props := TunnelProperties{Host: StringPtr("legacy.example.com")}
		if err := MaybeSetGeneratedStableDomain(&props, "project.edge.example.com:443"); err != nil {
			t.Fatalf("MaybeSetGeneratedStableDomain() error = %v", err)
		}
		if props.Hostname != nil {
			t.Fatalf("Hostname = %#v, want nil when Host is set", props.Hostname)
		}
	})
	t.Run("skips unpublished tunnels", func(t *testing.T) {
		props := TunnelProperties{Publish: BoolPtr(false)}
		if err := MaybeSetGeneratedStableDomain(&props, "project.edge.example.com:443"); err != nil {
			t.Fatalf("MaybeSetGeneratedStableDomain() error = %v", err)
		}
		if props.Hostname != nil {
			t.Fatalf("Hostname = %#v, want nil for unpublished tunnel", props.Hostname)
		}
	})
	t.Run("generates for published tunnel", func(t *testing.T) {
		props := TunnelProperties{Publish: BoolPtr(true)}
		if err := MaybeSetGeneratedStableDomain(&props, "project.edge.example.com:443"); err != nil {
			t.Fatalf("MaybeSetGeneratedStableDomain() error = %v", err)
		}
		if props.Hostname == nil {
			t.Fatalf("Hostname = nil, want generated hostname")
		}
	})
}

func TestStableHostnameCompatibilityWrappers(t *testing.T) {
	got, ok, err := GenerateStableHostname("project.edge.example.com:443")
	if err != nil || !ok || got == "" {
		t.Fatalf("GenerateStableHostname() = %q, %v, %v", got, ok, err)
	}
	props := TunnelProperties{Publish: BoolPtr(true)}
	if err := MaybeSetGeneratedStableHostname(&props, "project.edge.example.com:443"); err != nil {
		t.Fatalf("MaybeSetGeneratedStableHostname() error = %v", err)
	}
	if props.Hostname == nil || *props.Hostname == "" {
		t.Fatalf("Hostname was not generated: %#v", props.Hostname)
	}
}

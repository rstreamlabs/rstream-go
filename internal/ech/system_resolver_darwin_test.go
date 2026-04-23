// See LICENSE file in the project root for license information.

//go:build darwin

package ech

import (
	"testing"

	scutildns "github.com/johnstarich/go/dns/scutil"
)

func TestNameserversFromResolversPrefersScopedMatch(t *testing.T) {
	resolvers := []scutildns.Resolver{
		{
			Nameservers: []string{"9.9.9.9"},
		},
		{
			Domain:      "localhost.rstream.io",
			Nameservers: []string{"127.0.0.1"},
		},
	}
	got := nameserversFromResolvers(resolvers, "f587ee53.c.localhost.rstream.io")
	if len(got) != 1 || got[0] != "127.0.0.1:53" {
		t.Fatalf("unexpected scoped nameservers: %#v", got)
	}
}

func TestNameserversFromResolversUsesResolverPort(t *testing.T) {
	resolvers := []scutildns.Resolver{
		{
			Domain:      "localhost.rstream.io",
			Nameservers: []string{"127.0.0.1"},
			Port:        853,
		},
	}
	got := nameserversFromResolvers(resolvers, "f587ee53.c.localhost.rstream.io")
	if len(got) != 1 || got[0] != "127.0.0.1:853" {
		t.Fatalf("unexpected resolver port handling: %#v", got)
	}
}

func TestNameserversFromResolversFallsBackToDefaultResolver(t *testing.T) {
	resolvers := []scutildns.Resolver{
		{
			Domain:      "localhost.rstream.io",
			Nameservers: []string{"127.0.0.1"},
		},
		{
			Nameservers: []string{"9.9.9.9"},
		},
	}
	got := nameserversFromResolvers(resolvers, "other.example.com")
	if len(got) != 1 || got[0] != "9.9.9.9:53" {
		t.Fatalf("unexpected default fallback: %#v", got)
	}
}

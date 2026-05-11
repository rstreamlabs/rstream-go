// See LICENSE file in the project root for license information.

package config

import (
	"strings"
	"testing"
)

func TestEnsureEnvironmentAndContextLookupEdges(t *testing.T) {
	cfg := Config{
		Environments: []Environment{{APIURL: "https://api.example.com"}},
		Contexts: []Context{
			{Name: "prod", APIURL: "https://api.example.com", Engine: "engine-a"},
			{Name: "prod", APIURL: "https://other.example.com", Engine: "engine-b"},
			{Name: "local", Engine: "engine-local"},
		},
	}
	env := cfg.EnsureEnvironment("https://api.example.com")
	if env == nil || env.APIURL != "https://api.example.com" || len(cfg.Environments) != 1 {
		t.Fatalf("existing environment not reused: %#v", cfg.Environments)
	}
	env = cfg.EnsureEnvironment("https://new.example.com")
	if env == nil || env.APIURL != "https://new.example.com" || len(cfg.Environments) != 2 {
		t.Fatalf("new environment not appended: %#v", cfg.Environments)
	}
	ctx, idx, err := cfg.FindContextForAPIURL("prod", "https://api.example.com")
	if err != nil || idx != 0 || ctx == nil || ctx.Engine != "engine-a" {
		t.Fatalf("exact context lookup failed: ctx=%#v idx=%d err=%v", ctx, idx, err)
	}
	ctx, idx, err = cfg.FindContextForAPIURL("local", "https://api.example.com")
	if err != nil || idx != 2 || ctx == nil || ctx.Engine != "engine-local" {
		t.Fatalf("unlinked context lookup failed: ctx=%#v idx=%d err=%v", ctx, idx, err)
	}
	_, _, err = cfg.FindContextForAPIURL("prod", "https://missing.example.com")
	if err == nil || !strings.Contains(err.Error(), "not found for API URL") {
		t.Fatalf("expected API-scoped lookup error, got %v", err)
	}
}

func TestAPIURLLookupNormalizesTrailingSlashes(t *testing.T) {
	cfg := Config{
		Environments: []Environment{{APIURL: " https://api.example.com/ "}},
		Contexts: []Context{
			{Name: "prod", APIURL: "https://api.example.com/", Engine: "engine-a"},
		},
	}
	env := cfg.EnsureEnvironment("https://api.example.com")
	if env == nil || env.APIURL != "https://api.example.com" || len(cfg.Environments) != 1 {
		t.Fatalf("environment lookup did not normalize API URL: %#v", cfg.Environments)
	}
	ctx, idx, err := cfg.FindContextByNameAndAPIURL("prod", "https://api.example.com")
	if err != nil || idx != 0 || ctx == nil {
		t.Fatalf("context lookup did not normalize API URL: ctx=%#v idx=%d err=%v", ctx, idx, err)
	}
}

func TestContextLookupAmbiguityAndDuplicates(t *testing.T) {
	cfg := Config{Contexts: []Context{
		{Name: "dup", APIURL: "https://api.example.com"},
		{Name: "dup", APIURL: "https://api.example.com"},
		{Name: "ambiguous"},
		{Name: "ambiguous"},
	}}
	if _, _, err := cfg.FindContextByName("ambiguous"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous context error, got %v", err)
	}
	if _, _, err := cfg.FindContextByNameAndAPIURL("dup", "https://api.example.com"); err == nil || !strings.Contains(err.Error(), "multiple contexts") {
		t.Fatalf("expected duplicate context error, got %v", err)
	}
	if ctx, idx, err := cfg.FindContextByName("missing"); err != nil || ctx != nil || idx != -1 {
		t.Fatalf("missing context should be nil/-1 without error: ctx=%#v idx=%d err=%v", ctx, idx, err)
	}
}

func TestFindContextUnlinked(t *testing.T) {
	cfg := Config{Contexts: []Context{
		{Name: "dev", APIURL: "https://api.example.com", Engine: "linked"},
		{Name: "dev", Engine: "unlinked"},
		{Name: "prod", Engine: "prod"},
	}}
	ctx, idx, err := cfg.FindContextUnlinked("dev")
	if err != nil || idx != 1 || ctx == nil || ctx.Engine != "unlinked" {
		t.Fatalf("FindContextUnlinked() = ctx=%#v idx=%d err=%v, want unlinked dev at index 1", ctx, idx, err)
	}
	ctx, idx, err = cfg.FindContextUnlinked("missing")
	if err != nil || idx != -1 || ctx != nil {
		t.Fatalf("missing FindContextUnlinked() = ctx=%#v idx=%d err=%v, want nil/-1/nil", ctx, idx, err)
	}
	linkedOnly := Config{Contexts: []Context{{Name: "linked", APIURL: "https://api.example.com"}}}
	_, _, err = linkedOnly.FindContextUnlinked("linked")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error for linked-only context, got %v", err)
	}
	duplicateUnlinked := Config{Contexts: []Context{{Name: "dup"}, {Name: "dup"}}}
	_, _, err = duplicateUnlinked.FindContextUnlinked("dup")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error for duplicate unlinked contexts, got %v", err)
	}
}

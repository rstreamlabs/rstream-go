// See LICENSE file in the project root for license information.

package config

import "testing"

func TestResolvePrecedence(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{
			Context: &DefaultContext{
				Name: "primary",
			},
		},
		Environments: []Environment{
			{
				APIURL: "https://flag.example",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "env-token"}},
				},
			},
		},
		Contexts: []Context{
			{
				Name:   "primary",
				APIURL: "https://flag.example",
				Engine: "engine.example:443",
				Auth:   &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "ctx-token"}}},
			},
		},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		FlagAPIURL:    "https://flag.example",
		EnvAPIURL:     "https://env.example",
		EnvToken:      "env-var-token",
		ResolveToken:  true,
		RequireEngine: true,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if resolved.APIURL != "https://flag.example" {
		t.Fatalf("expected apiUrl to be flag override, got %q", resolved.APIURL)
	}
	if resolved.Engine != "engine.example:443" {
		t.Fatalf("expected engine from context, got %q", resolved.Engine)
	}
	if resolved.Token != "env-var-token" {
		t.Fatalf("expected env token override, got %q", resolved.Token)
	}
}

func TestResolveUnlinkedContextDoesNotInheritEnvToken(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{
			Context: &DefaultContext{
				Name: "local",
			},
		},
		Environments: []Environment{
			{
				APIURL: "https://rstream.io",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "env-token"}},
				},
			},
		},
		Contexts: []Context{
			{
				Name:   "local",
				Engine: "engine.local:8443",
			},
		},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		ResolveToken:  true,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if resolved.Token != "" {
		t.Fatalf("expected no token inherited for unlinked context, got %q", resolved.Token)
	}
}

func TestResolveLinkedContextInheritsEnvToken(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{
			Context: &DefaultContext{
				Name: "linked",
			},
		},
		Environments: []Environment{
			{
				APIURL: "https://rstream.io",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "env-token"}},
				},
			},
		},
		Contexts: []Context{
			{
				Name:   "linked",
				APIURL: "https://rstream.io",
				Engine: "engine.local:8443",
			},
		},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		RequireToken:  true,
		ResolveToken:  true,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if resolved.Token != "env-token" {
		t.Fatalf("expected env token inherited, got %q", resolved.Token)
	}
}

func TestResolveDefaultContextUsesContextAPIURL(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{
			Context: &DefaultContext{
				Name: "primary",
			},
		},
		Environments: []Environment{
			{
				APIURL: "https://dev.example",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "env-token"}},
				},
			},
		},
		Contexts: []Context{
			{
				Name:   "primary",
				APIURL: "https://dev.example",
				Engine: "engine.dev:8443",
			},
		},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		ResolveToken:  true,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if resolved.APIURL != "https://dev.example" {
		t.Fatalf("expected apiUrl to come from context, got %q", resolved.APIURL)
	}
	if resolved.Token != "env-token" {
		t.Fatalf("expected env token inherited, got %q", resolved.Token)
	}
}

func TestResolveIgnoreDefaultContext(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{
			Context: &DefaultContext{
				Name: "prod",
			},
		},
		Environments: []Environment{
			{
				APIURL: "http://localhost:3000",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "env-token"}},
				},
			},
		},
		Contexts: []Context{
			{
				Name:   "prod",
				APIURL: "https://rstream.io",
				Engine: "engine.prod:443",
			},
		},
	}
	resolved, err := Resolve(ResolveInput{
		Config:               cfg,
		FlagAPIURL:           "http://localhost:3000",
		IgnoreDefaultContext: true,
		RequireToken:         true,
		ResolveToken:         true,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if resolved.APIURL != "http://localhost:3000" {
		t.Fatalf("expected selected apiUrl, got %q", resolved.APIURL)
	}
	if resolved.Context != nil {
		t.Fatalf("expected no context, got %#v", resolved.Context)
	}
	if resolved.Token != "env-token" {
		t.Fatalf("expected token inherited from selected environment, got %q", resolved.Token)
	}
}

// See LICENSE file in the project root for license information.

package config

import "testing"

func TestResolvePrecedence(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{
			APIURL: "https://default.example",
			Context: &DefaultContext{
				APIURL: "https://flag.example",
				Name:   "primary",
			},
		},
		Environments: []Environment{
			{
				APIURL: "https://flag.example",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "env-token"}},
				},
				Contexts: []Context{
					{
						Name:   "primary",
						Engine: "engine.example:8443",
						Auth:   &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "ctx-token"}}},
					},
				},
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
	if resolved.Engine != "engine.example:8443" {
		t.Fatalf("expected engine from context, got %q", resolved.Engine)
	}
	if resolved.Token != "env-var-token" {
		t.Fatalf("expected env token override, got %q", resolved.Token)
	}
}

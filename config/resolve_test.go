// See LICENSE file in the project root for license information.

package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

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

func TestResolveRejectsStoredTokenWithEngineOverride(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "prod"}},
		Contexts: []Context{
			{
				Name:   "prod",
				APIURL: "https://rstream.io",
				Engine: "engine.prod:443",
				Auth:   &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "ctx-token"}}},
			},
		},
	}
	_, err := Resolve(ResolveInput{
		Config:        cfg,
		EnvEngine:     "attacker.example:443",
		RequireEngine: true,
		RequireToken:  true,
		ResolveToken:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "stored token with an explicit engine override") {
		t.Fatalf("Resolve() error = %v, want stored-token override rejection", err)
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		EnvEngine:     "attacker.example:443",
		EnvToken:      "explicit-token",
		RequireEngine: true,
		RequireToken:  true,
		ResolveToken:  true,
	})
	if err != nil {
		t.Fatalf("Resolve() with explicit token failed: %v", err)
	}
	if resolved.Engine != "attacker.example:443" || resolved.Token != "explicit-token" {
		t.Fatalf("unexpected resolved override: %#v", resolved)
	}
}

func TestResolveAllowsEngineOverrideMatchingSelectedContext(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "prod"}},
		Contexts: []Context{
			{
				Name:   "prod",
				Engine: "engine.prod:443",
				Auth:   &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "ctx-token"}}},
			},
		},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		EnvEngine:     "engine.prod:443",
		RequireEngine: true,
		RequireToken:  true,
		ResolveToken:  true,
	})
	if err != nil {
		t.Fatalf("Resolve() failed: %v", err)
	}
	if resolved.Token != "ctx-token" {
		t.Fatalf("expected context token, got %q", resolved.Token)
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

func TestResolveRejectsUnlinkedContextWhenExplicitAPIURLIsExplicit(t *testing.T) {
	cfg := Config{
		Contexts: []Context{
			{Name: "local", Engine: "engine-local"},
		},
	}
	_, err := Resolve(ResolveInput{
		Config:      cfg,
		FlagContext: "local",
		FlagAPIURL:  "https://api.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "not found for API URL") {
		t.Fatalf("expected API-scoped context error, got %v", err)
	}
	resolved, err := Resolve(ResolveInput{
		Config:      cfg,
		FlagContext: "local",
	})
	if err != nil {
		t.Fatalf("unexpected resolve without explicit API URL: %v", err)
	}
	if resolved.Context == nil || resolved.Context.Engine != "engine-local" {
		t.Fatalf("unexpected resolved context: %#v", resolved)
	}
}

func TestTokenFromAuthErrors(t *testing.T) {
	tests := []struct {
		name string
		auth *Auth
	}{
		{name: "missing storage", auth: &Auth{Token: &Token{}}},
		{name: "missing kind", auth: &Auth{Token: &Token{Storage: &TokenStorage{}}}},
		{name: "unsupported keychain", auth: &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageKeychain}}}},
		{name: "unknown kind", auth: &Auth{Token: &Token{Storage: &TokenStorage{Kind: "vault"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := TokenFromAuth(tt.auth); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
	token, ok, err := TokenFromAuth(&Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "token"}}})
	if err != nil || !ok || token != "token" {
		t.Fatalf("inline token not returned: token=%q ok=%v err=%v", token, ok, err)
	}
	token, ok, err = TokenFromAuth(&Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline}}})
	if err != nil || ok || token != "" {
		t.Fatalf("empty inline token should be absent: token=%q ok=%v err=%v", token, ok, err)
	}
}

func TestIsTokenExpired(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	expiredToken := unsignedJWT(`{"exp": 100}`)
	expired, err := isTokenExpired(expiredToken, now)
	if err != nil || !expired {
		t.Fatalf("expired token result = %v, %v", expired, err)
	}
	futureToken := unsignedJWT(fmt.Sprintf(`{"exp": %d}`, now.Add(time.Hour).Unix()))
	expired, err = isTokenExpired(futureToken, now)
	if err != nil || expired {
		t.Fatalf("future token result = %v, %v", expired, err)
	}
	noExpToken := unsignedJWT(`{"sub":"user"}`)
	expired, err = isTokenExpired(noExpToken, now)
	if err != nil || expired {
		t.Fatalf("token without exp result = %v, %v", expired, err)
	}
	expired, err = isTokenExpired("not-a-jwt", now)
	if err != nil || expired {
		t.Fatalf("opaque token result = %v, %v", expired, err)
	}
	expired, err = isTokenExpired(unsignedJWT(`{`), now)
	if err != nil || expired {
		t.Fatalf("invalid JSON claims should be ignored like opaque tokens: %v, %v", expired, err)
	}
}

func unsignedJWT(payload string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

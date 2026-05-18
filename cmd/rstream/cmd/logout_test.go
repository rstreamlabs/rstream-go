// See LICENSE file in the project root for license information.

package cmd

import (
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
)

func TestClearLogoutCredentialsClearsEnvironmentAndMatchingContextTokens(t *testing.T) {
	cfg := config.Config{
		Environments: []config.Environment{
			{
				APIURL: "https://api.example.com",
				Auth:   authWithInlineToken("env-token"),
			},
			{
				APIURL: "https://other.example.com",
				Auth:   authWithInlineToken("other-env-token"),
			},
		},
		Contexts: []config.Context{
			{Name: "prod", APIURL: "https://api.example.com", Auth: authWithInlineToken("ctx-token")},
			{Name: "other", APIURL: "https://other.example.com", Auth: authWithInlineToken("other-ctx-token")},
		},
	}
	if err := clearLogoutCredentials(&cfg, config.Resolved{APIURL: "https://api.example.com"}); err != nil {
		t.Fatalf("clearLogoutCredentials() error = %v", err)
	}
	if cfg.Environments[0].Auth != nil || cfg.Contexts[0].Auth != nil {
		t.Fatalf("selected API credentials were not cleared: %#v %#v", cfg.Environments[0].Auth, cfg.Contexts[0].Auth)
	}
	if cfg.Environments[1].Auth == nil || cfg.Contexts[1].Auth == nil {
		t.Fatalf("unrelated API credentials should be preserved: %#v %#v", cfg.Environments[1].Auth, cfg.Contexts[1].Auth)
	}
}

func TestClearLogoutCredentialsNormalizesAPIURL(t *testing.T) {
	cfg := config.Config{
		Environments: []config.Environment{{
			APIURL: "https://api.example.com/",
			Auth:   authWithInlineToken("env-token"),
		}},
		Contexts: []config.Context{{
			Name:   "prod",
			APIURL: "https://api.example.com/",
			Auth:   authWithInlineToken("ctx-token"),
		}},
	}
	if err := clearLogoutCredentials(&cfg, config.Resolved{APIURL: "https://api.example.com"}); err != nil {
		t.Fatalf("clearLogoutCredentials() error = %v", err)
	}
	if cfg.Environments[0].Auth != nil || cfg.Contexts[0].Auth != nil {
		t.Fatalf("normalized API credentials were not cleared: %#v %#v", cfg.Environments[0].Auth, cfg.Contexts[0].Auth)
	}
}

func TestClearLogoutCredentialsClearsSelectedUnlinkedContextToken(t *testing.T) {
	cfg := config.Config{
		Contexts: []config.Context{
			{Name: "local", Auth: authWithInlineToken("local-token")},
			{Name: "other", Auth: authWithInlineToken("other-token")},
		},
	}
	if err := clearLogoutCredentials(&cfg, config.Resolved{
		APIURL:      config.DefaultAPIURL(),
		ContextName: "local",
		Context:     &config.Context{Name: "local"},
	}); err != nil {
		t.Fatalf("clearLogoutCredentials() error = %v", err)
	}
	if cfg.Contexts[0].Auth != nil {
		t.Fatalf("selected unlinked context auth was not cleared: %#v", cfg.Contexts[0].Auth)
	}
	if cfg.Contexts[1].Auth == nil {
		t.Fatalf("unselected unlinked context auth should be preserved")
	}
}

func authWithInlineToken(value string) *config.Auth {
	return &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
		Kind:  config.TokenStorageInline,
		Value: value,
	}}}
}

// See LICENSE file in the project root for license information.

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigTokenPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := Config{
		Environments: []Environment{
			{
				APIURL: "https://rstream.io",
				Auth: &Auth{
					Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "secret"}},
				},
			},
		},
	}
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Environments[0].Auth.Token.Storage.Value != "secret" {
		t.Fatalf("expected token to persist, got %q", loaded.Environments[0].Auth.Token.Storage.Value)
	}
	loaded.Environments[0].Auth.Token = nil
	if err := WriteAtomic(path, loaded); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if reloaded.Environments[0].Auth != nil && reloaded.Environments[0].Auth.Token != nil {
		t.Fatalf("expected token to be cleared")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

// See LICENSE file in the project root for license information.

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLoadHandlesMissingEmptyAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.yaml")
	cfg, err := Load(missing)
	if err != nil {
		t.Fatalf("Load(missing) error = %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("Load(missing).Version = %d, want 1", cfg.Version)
	}
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte(" \n\t"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	cfg, err = Load(empty)
	if err != nil {
		t.Fatalf("Load(empty) error = %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("Load(empty).Version = %d, want 1", cfg.Version)
	}
	invalid := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(invalid, []byte("contexts: ["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	if _, err := Load(invalid); err == nil || !strings.Contains(err.Error(), "invalid config YAML") {
		t.Fatalf("Load(invalid) error = %v, want invalid YAML error", err)
	}
}

func TestDefaultConfigPathUsesUserHomeDirectory(t *testing.T) {
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error = %v", err)
	}
	want := filepath.Join(home, ".rstream", "config.yaml")
	if got != want {
		t.Fatalf("DefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestDefaultAPIURL(t *testing.T) {
	if got := DefaultAPIURL(); got != "https://rstream.io" {
		t.Fatalf("DefaultAPIURL() = %q, want https://rstream.io", got)
	}
}

func TestWriteAtomicPersistsNormalizedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".rstream", "config.yaml")
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "  "}},
		Contexts: []Context{{
			Name:   "dev",
			Engine: "engine.example.com:443",
		}},
	}
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written config: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %v, want 0600", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load(written) error = %v", err)
	}
	if loaded.Version != 1 {
		t.Fatalf("Version = %d, want normalized version 1", loaded.Version)
	}
	if loaded.Defaults.Context != nil {
		t.Fatalf("Defaults.Context = %#v, want nil for blank default context", loaded.Defaults.Context)
	}
	if len(loaded.Contexts) != 1 || loaded.Contexts[0].Name != "dev" {
		t.Fatalf("Contexts = %#v, want dev context", loaded.Contexts)
	}
}

func TestUpdateAtomicPreservesLatestConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	initial := Config{Version: 1, Contexts: []Context{{Name: "first", Engine: "first.example:443"}}}
	if err := WriteAtomic(path, initial); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	if err := UpdateAtomic(path, func(cfg *Config) error {
		cfg.Contexts = append(cfg.Contexts, Context{Name: "second", Engine: "second.example:443"})
		cfg.Defaults.Context = &DefaultContext{Name: "second"}
		return nil
	}); err != nil {
		t.Fatalf("UpdateAtomic() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Contexts) != 2 || loaded.Contexts[0].Name != "first" || loaded.Contexts[1].Name != "second" {
		t.Fatalf("UpdateAtomic() lost config values: %#v", loaded.Contexts)
	}
	if loaded.Defaults.Context == nil || loaded.Defaults.Context.Name != "second" {
		t.Fatalf("UpdateAtomic() default = %#v, want second", loaded.Defaults.Context)
	}
}

func TestUpdateAtomicRejectsNilAndKeepsConfigOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := Config{Version: 1, Contexts: []Context{{Name: "first", Engine: "first.example:443"}}}
	if err := WriteAtomic(path, initial); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	if err := UpdateAtomic(path, nil); err == nil {
		t.Fatal("UpdateAtomic() accepted a nil update")
	}
	wantErr := errors.New("stop")
	if err := UpdateAtomic(path, func(cfg *Config) error {
		cfg.Contexts = nil
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("UpdateAtomic() error = %v, want %v", err, wantErr)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Contexts) != 1 || loaded.Contexts[0].Name != "first" {
		t.Fatalf("failed UpdateAtomic() changed config: %#v", loaded.Contexts)
	}
}

func TestWriteAtomicCanReplaceInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("contexts: ["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	if err := WriteAtomic(path, Config{Contexts: []Context{{Name: "recovered", Engine: "engine.example:443"}}}); err != nil {
		t.Fatalf("WriteAtomic() could not replace invalid config: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Contexts) != 1 || loaded.Contexts[0].Name != "recovered" {
		t.Fatalf("WriteAtomic() config = %#v, want recovered context", loaded.Contexts)
	}
}

func TestUpdateAtomicSerializesConcurrentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, Config{}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	const updates = 8
	errors := make(chan error, updates)
	var wait sync.WaitGroup
	for index := 0; index < updates; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errors <- UpdateAtomic(path, func(cfg *Config) error {
				cfg.Contexts = append(cfg.Contexts, Context{Name: fmt.Sprintf("context-%d", index)})
				return nil
			})
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("UpdateAtomic() error = %v", err)
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Contexts) != updates {
		t.Fatalf("concurrent updates persisted %d contexts, want %d", len(loaded.Contexts), updates)
	}
}

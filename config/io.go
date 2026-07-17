// See LICENSE file in the project root for license information.

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rstream", "config.yaml"), nil
}

func Load(path string) (Config, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.EnsureVersion()
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return cfg, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		cfg.EnsureVersion()
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid config YAML: %w", err)
	}
	cfg.Normalize()
	return cfg, nil
}

func WriteAtomic(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	return writeAtomicUnlocked(path, cfg)
}

func UpdateAtomic(path string, update func(*Config) error) error {
	if update == nil {
		return errors.New("config update function is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	if err := update(&cfg); err != nil {
		return err
	}
	return writeAtomicUnlocked(path, cfg)
}

func writeAtomicUnlocked(path string, cfg Config) error {
	cfg.Normalize()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&cfg); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rstream-config-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

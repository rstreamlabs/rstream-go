// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type webTTYServerRuntimeConfig struct {
	Version    int                             `yaml:"version,omitempty"`
	Server     webTTYServerRuntimeServerConfig `yaml:"server,omitempty"`
	E2E        webTTYServerRuntimeE2EConfig    `yaml:"e2e,omitempty"`
	TLS        webTTYServerRuntimeTLSConfig    `yaml:"tls,omitempty"`
	Filesystem webTTYServerRuntimeFSConfig     `yaml:"filesystem,omitempty"`
}

type webTTYServerRuntimeServerConfig struct {
	Rstream              *bool             `yaml:"rstream,omitempty"`
	Listen               string            `yaml:"listen,omitempty"`
	Name                 string            `yaml:"name,omitempty"`
	ServerID             string            `yaml:"serverId,omitempty"`
	ServerEnrollment     string            `yaml:"serverEnrollment,omitempty"`
	Transport            string            `yaml:"transport,omitempty"`
	ExecutionMode        string            `yaml:"executionMode,omitempty"`
	LoginUser            string            `yaml:"loginUser,omitempty"`
	AllowClientUser      *bool             `yaml:"allowClientUser,omitempty"`
	Retry                *bool             `yaml:"retry,omitempty"`
	RetryIntervalMS      *int64            `yaml:"retryIntervalMs,omitempty"`
	ShutdownTimeoutMS    *int64            `yaml:"shutdownTimeoutMs,omitempty"`
	Publish              *bool             `yaml:"publish,omitempty"`
	AuthTokenFile        string            `yaml:"authTokenFile,omitempty"`
	AllowUnauthenticated *bool             `yaml:"allowUnauthenticated,omitempty"`
	AllowedOrigins       []string          `yaml:"allowedOrigins,omitempty"`
	Labels               map[string]string `yaml:"labels,omitempty"`
}

type webTTYServerRuntimeE2EConfig struct {
	Enabled               *bool    `yaml:"enabled,omitempty"`
	Identity              string   `yaml:"identity,omitempty"`
	IdentityFile          string   `yaml:"identityFile,omitempty"`
	AuthorizedClientsFile string   `yaml:"authorizedClientsFile,omitempty"`
	AuthorizedClientKeys  []string `yaml:"authorizedClientKeys,omitempty"`
}

type webTTYServerRuntimeTLSConfig struct {
	CertFile string `yaml:"certFile,omitempty"`
	KeyFile  string `yaml:"keyFile,omitempty"`
}

type webTTYServerRuntimeFSConfig struct {
	Root               string `yaml:"root,omitempty"`
	ReadOnly           *bool  `yaml:"readOnly,omitempty"`
	MaxUploadSizeBytes *int64 `yaml:"maxUploadSizeBytes,omitempty"`
}

func applyWebTTYServerRuntimeConfig(cmd *cobra.Command) error {
	path, explicit, err := webTTYServerRuntimeConfigPath(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return nil
	}
	cfg, err := loadWebTTYServerRuntimeConfig(path)
	if err != nil {
		if explicit {
			return err
		}
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return applyWebTTYServerRuntimeConfigValues(cmd, cfg)
}

func webTTYServerRuntimeConfigPath(cmd *cobra.Command) (string, bool, error) {
	if value, changed := stringFlagValue(cmd, "webtty-config"); changed {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", true, fmt.Errorf("--webtty-config is empty")
		}
		path, err := expandWebTTYPath(value)
		return path, true, err
	}
	value := strings.TrimSpace(os.Getenv(webTTYConfigEnv))
	if value == "" {
		return "", false, nil
	}
	path, err := expandWebTTYPath(value)
	return path, true, err
}

func loadWebTTYServerRuntimeConfig(path string) (*webTTYServerRuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read WebTTY runtime config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg webTTYServerRuntimeConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid WebTTY runtime config YAML: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported WebTTY runtime config version %d", cfg.Version)
	}
	if cfg.Server.Rstream != nil && !*cfg.Server.Rstream && (strings.TrimSpace(cfg.Server.ServerID) != "" || strings.TrimSpace(cfg.Server.ServerEnrollment) != "") {
		return nil, fmt.Errorf("WebTTY runtime config server.serverId and server.serverEnrollment imply rstream mode")
	}
	return &cfg, nil
}

func applyWebTTYServerRuntimeConfigValues(cmd *cobra.Command, cfg *webTTYServerRuntimeConfig) error {
	if cfg == nil {
		return nil
	}
	server := cfg.Server
	if server.Rstream != nil && *server.Rstream {
		if err := setBoolFlagFromConfig(cmd, "rstream", true); err != nil {
			return err
		}
	}
	if err := setStringFlagFromConfig(cmd, "listen", server.Listen, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "name", server.Name, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "server-id", server.ServerID, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "server-enrollment", server.ServerEnrollment, true); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "transport", server.Transport, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "execution-mode", server.ExecutionMode, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "login-user", server.LoginUser, false); err != nil {
		return err
	}
	if err := setBoolFlagFromConfigPtr(cmd, "allow-client-user", server.AllowClientUser); err != nil {
		return err
	}
	if err := setRetryFlagsFromConfig(cmd, server.Retry); err != nil {
		return err
	}
	if err := setInt64FlagFromConfig(cmd, "retry-interval", server.RetryIntervalMS); err != nil {
		return err
	}
	if err := setInt64FlagFromConfig(cmd, "shutdown-timeout", server.ShutdownTimeoutMS); err != nil {
		return err
	}
	if err := setPublishFlagsFromConfig(cmd, server.Publish); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "auth-token-file", server.AuthTokenFile, true); err != nil {
		return err
	}
	if err := setBoolFlagFromConfigPtr(cmd, "allow-unauthenticated", server.AllowUnauthenticated); err != nil {
		return err
	}
	if err := setStringArrayFlagFromConfig(cmd, "allowed-origin", server.AllowedOrigins, false); err != nil {
		return err
	}
	if err := setLabelFlagFromConfig(cmd, server.Labels); err != nil {
		return err
	}
	if err := setBoolFlagFromConfigPtr(cmd, "e2e", cfg.E2E.Enabled); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "identity", cfg.E2E.Identity, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "identity-file", cfg.E2E.IdentityFile, true); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "authorized-clients-file", cfg.E2E.AuthorizedClientsFile, true); err != nil {
		return err
	}
	if err := setStringArrayFlagFromConfig(cmd, "authorized-client-key", cfg.E2E.AuthorizedClientKeys, false); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "tls-cert-file", cfg.TLS.CertFile, true); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "tls-key-file", cfg.TLS.KeyFile, true); err != nil {
		return err
	}
	if err := setStringFlagFromConfig(cmd, "fs-root", cfg.Filesystem.Root, true); err != nil {
		return err
	}
	if err := setBoolFlagFromConfigPtr(cmd, "fs-read-only", cfg.Filesystem.ReadOnly); err != nil {
		return err
	}
	return setInt64FlagFromConfig(cmd, "fs-max-upload-size", cfg.Filesystem.MaxUploadSizeBytes)
}

func setStringFlagFromConfig(cmd *cobra.Command, name string, value string, pathValue bool) error {
	value = strings.TrimSpace(value)
	if value == "" || flagChanged(cmd, name) {
		return nil
	}
	if pathValue {
		var err error
		value, err = expandWebTTYPath(value)
		if err != nil {
			return err
		}
	}
	return cmd.Flags().Set(name, value)
}

func setBoolFlagFromConfigPtr(cmd *cobra.Command, name string, value *bool) error {
	if value == nil {
		return nil
	}
	return setBoolFlagFromConfig(cmd, name, *value)
}

func setBoolFlagFromConfig(cmd *cobra.Command, name string, value bool) error {
	if flagChanged(cmd, name) {
		return nil
	}
	return cmd.Flags().Set(name, fmt.Sprintf("%t", value))
}

func setInt64FlagFromConfig(cmd *cobra.Command, name string, value *int64) error {
	if value == nil || flagChanged(cmd, name) {
		return nil
	}
	return cmd.Flags().Set(name, fmt.Sprintf("%d", *value))
}

func setStringArrayFlagFromConfig(cmd *cobra.Command, name string, values []string, pathValues bool) error {
	if len(values) == 0 || flagChanged(cmd, name) {
		return nil
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if pathValues {
			var err error
			value, err = expandWebTTYPath(value)
			if err != nil {
				return err
			}
		}
		if err := cmd.Flags().Set(name, value); err != nil {
			return err
		}
	}
	return nil
}

func setLabelFlagFromConfig(cmd *cobra.Command, labels map[string]string) error {
	if len(labels) == 0 || flagChanged(cmd, "label") {
		return nil
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		key = strings.TrimSpace(key)
		value := strings.TrimSpace(labels[key])
		if key == "" {
			continue
		}
		if err := cmd.Flags().Set("label", key+"="+value); err != nil {
			return err
		}
	}
	return nil
}

func setRetryFlagsFromConfig(cmd *cobra.Command, value *bool) error {
	if value == nil || flagChanged(cmd, "retry") || flagChanged(cmd, "no-retry") {
		return nil
	}
	if *value {
		return cmd.Flags().Set("retry", "true")
	}
	return cmd.Flags().Set("no-retry", "true")
}

func setPublishFlagsFromConfig(cmd *cobra.Command, value *bool) error {
	if value == nil || flagChanged(cmd, "publish") || flagChanged(cmd, "no-publish") {
		return nil
	}
	if *value {
		return cmd.Flags().Set("publish", "true")
	}
	return cmd.Flags().Set("no-publish", "true")
}

func flagChanged(cmd *cobra.Command, name string) bool {
	flag := cmd.Flags().Lookup(name)
	return flag != nil && flag.Changed
}

func stringFlagValue(cmd *cobra.Command, name string) (string, bool) {
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		return "", false
	}
	value, _ := cmd.Flags().GetString(name)
	return value, flag.Changed
}

func expandWebTTYPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "~" || !strings.HasPrefix(value, "~/") {
		if value == "~" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			return home, nil
		}
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(value, "~/")), nil
}

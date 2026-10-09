// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/configdecode"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/tunnelconfig"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type webTTYServerRuntimeConfig struct {
	Version    int                             `yaml:"version,omitempty"`
	Server     webTTYServerRuntimeServerConfig `yaml:"server,omitempty"`
	E2E        webTTYServerRuntimeE2EConfig    `yaml:"e2e,omitempty"`
	TLS        webTTYServerRuntimeTLSConfig    `yaml:"tls,omitempty"`
	Filesystem webTTYServerRuntimeFSConfig     `yaml:"filesystem,omitempty"`
}

type webTTYServerRuntimeServerConfig struct {
	Host                 *string           `yaml:"host,omitempty"`
	Rstream              *bool             `yaml:"rstream,omitempty"`
	Listen               *string           `yaml:"listen,omitempty"`
	Name                 *string           `yaml:"name,omitempty"`
	ServerID             *string           `yaml:"serverId,omitempty"`
	ServerEnrollment     *string           `yaml:"serverEnrollment,omitempty"`
	Transport            *string           `yaml:"transport,omitempty"`
	ExecutionMode        *string           `yaml:"executionMode,omitempty"`
	LoginUser            *string           `yaml:"loginUser,omitempty"`
	AllowClientUser      *bool             `yaml:"allowClientUser,omitempty"`
	Retry                *bool             `yaml:"retry,omitempty"`
	RetryIntervalMS      *int64            `yaml:"retryIntervalMs,omitempty"`
	ShutdownTimeoutMS    *int64            `yaml:"shutdownTimeoutMs,omitempty"`
	Publish              *bool             `yaml:"publish,omitempty"`
	AuthTokenFile        *string           `yaml:"authTokenFile,omitempty"`
	AllowUnauthenticated *bool             `yaml:"allowUnauthenticated,omitempty"`
	AllowedOrigins       []string          `yaml:"allowedOrigins,omitempty"`
	Labels               map[string]string `yaml:"labels,omitempty"`
}

type webTTYServerRuntimeE2EConfig struct {
	Enabled               *bool    `yaml:"enabled,omitempty"`
	Identity              *string  `yaml:"identity,omitempty"`
	IdentityFile          *string  `yaml:"identityFile,omitempty"`
	AuthorizedClientsFile *string  `yaml:"authorizedClientsFile,omitempty"`
	AuthorizedClientKeys  []string `yaml:"authorizedClientKeys,omitempty"`
}

type webTTYServerRuntimeTLSConfig struct {
	CertFile *string `yaml:"certFile,omitempty"`
	KeyFile  *string `yaml:"keyFile,omitempty"`
}

type webTTYServerRuntimeFSConfig struct {
	Backend            *string `yaml:"backend,omitempty"`
	Root               *string `yaml:"root,omitempty"`
	ReadOnly           *bool   `yaml:"readOnly,omitempty"`
	MaxUploadSizeBytes *int64  `yaml:"maxUploadSizeBytes,omitempty"`
}

func applyWebTTYServerRuntimeConfig(cmd *cobra.Command) error {
	path, explicit, err := webTTYServerRuntimeConfigPath(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return applyWebTTYServerRuntimeConfigValues(cmd, nil)
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
	var cfg webTTYServerRuntimeConfig
	if err := configdecode.YAML(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid WebTTY runtime config YAML: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported WebTTY runtime config version %d", cfg.Version)
	}
	return &cfg, nil
}

// Each binding is the single CLI/YAML correspondence for a runtime option.
// Pointers distinguish absent values from explicit false, zero and empty values.
type webTTYBinding[T any] struct {
	flag  string
	path  string
	value *T
}

type webTTYRuntimeBindings struct {
	strings  []webTTYBinding[*string]
	bools    []webTTYBinding[*bool]
	integers []webTTYBinding[*int64]
	arrays   []webTTYBinding[[]string]
	labels   webTTYBinding[map[string]string]
}

func webTTYBindings(cfg *webTTYServerRuntimeConfig) webTTYRuntimeBindings {
	return webTTYRuntimeBindings{
		strings: []webTTYBinding[*string]{
			{"listen", "server.listen", &cfg.Server.Listen},
			{"name", "server.name", &cfg.Server.Name},
			{"host", "server.host", &cfg.Server.Host},
			{"server-id", "server.serverId", &cfg.Server.ServerID},
			{"server-enrollment", "server.serverEnrollment", &cfg.Server.ServerEnrollment},
			{"transport", "server.transport", &cfg.Server.Transport},
			{"execution-mode", "server.executionMode", &cfg.Server.ExecutionMode},
			{"login-user", "server.loginUser", &cfg.Server.LoginUser},
			{"auth-token-file", "server.authTokenFile", &cfg.Server.AuthTokenFile},
			{"identity", "e2e.identity", &cfg.E2E.Identity},
			{"identity-file", "e2e.identityFile", &cfg.E2E.IdentityFile},
			{"authorized-clients-file", "e2e.authorizedClientsFile", &cfg.E2E.AuthorizedClientsFile},
			{"tls-cert-file", "tls.certFile", &cfg.TLS.CertFile},
			{"tls-key-file", "tls.keyFile", &cfg.TLS.KeyFile},
			{"fs-root", "filesystem.root", &cfg.Filesystem.Root},
			{"fs-backend", "filesystem.backend", &cfg.Filesystem.Backend},
		},
		bools: []webTTYBinding[*bool]{
			{"rstream", "server.rstream", &cfg.Server.Rstream},
			{"allow-client-user", "server.allowClientUser", &cfg.Server.AllowClientUser},
			{"retry", "server.retry", &cfg.Server.Retry},
			{"publish", "server.publish", &cfg.Server.Publish},
			{"allow-unauthenticated", "server.allowUnauthenticated", &cfg.Server.AllowUnauthenticated},
			{"e2e", "e2e.enabled", &cfg.E2E.Enabled},
			{"fs-read-only", "filesystem.readOnly", &cfg.Filesystem.ReadOnly},
		},
		integers: []webTTYBinding[*int64]{
			{"retry-interval", "server.retryIntervalMs", &cfg.Server.RetryIntervalMS},
			{"shutdown-timeout", "server.shutdownTimeoutMs", &cfg.Server.ShutdownTimeoutMS},
			{"fs-max-upload-size", "filesystem.maxUploadSizeBytes", &cfg.Filesystem.MaxUploadSizeBytes},
		},
		arrays: []webTTYBinding[[]string]{
			{"allowed-origin", "server.allowedOrigins", &cfg.Server.AllowedOrigins},
			{"authorized-client-key", "e2e.authorizedClientKeys", &cfg.E2E.AuthorizedClientKeys},
		},
		labels: webTTYBinding[map[string]string]{"label", "server.labels", &cfg.Server.Labels},
	}
}

func applyWebTTYServerRuntimeConfigValues(cmd *cobra.Command, cfg *webTTYServerRuntimeConfig) error {
	if cfg == nil {
		cfg = &webTTYServerRuntimeConfig{}
	}
	resolved := *cfg
	// An explicit selector replaces the alternative selector from the file.
	for _, pair := range []struct {
		a, b   string
		av, bv **string
	}{
		{"identity", "identity-file", &resolved.E2E.Identity, &resolved.E2E.IdentityFile},
		{"server-id", "server-enrollment", &resolved.Server.ServerID, &resolved.Server.ServerEnrollment},
	} {
		if flagChanged(cmd, pair.a) && !flagChanged(cmd, pair.b) {
			*pair.bv = nil
		}
		if flagChanged(cmd, pair.b) && !flagChanged(cmd, pair.a) {
			*pair.av = nil
		}
	}
	bindings := webTTYBindings(&resolved)
	for _, b := range bindings.strings {
		if flagChanged(cmd, b.flag) {
			value, _ := cmd.Flags().GetString(b.flag)
			*b.value = &value
		}
		if *b.value == nil {
			continue
		}
		value := strings.TrimSpace(**b.value)
		switch b.flag {
		case "server-enrollment", "auth-token-file", "identity-file", "authorized-clients-file", "tls-cert-file", "tls-key-file", "fs-root":
			var err error
			value, err = expandWebTTYPath(value)
			if err != nil {
				return err
			}
		}
		*b.value = &value
		if err := cmd.Flags().Set(b.flag, value); err != nil {
			return err
		}
	}
	for _, b := range bindings.bools {
		inverse := ""
		if b.flag == "publish" || b.flag == "retry" {
			inverse = "no-" + b.flag
		}
		value, err := tunnelconfig.Bool(getBoolPtr(cmd, b.flag), getBoolPtr(cmd, inverse))
		if err != nil {
			return fmt.Errorf("--%s: %w", b.flag, err)
		}
		if value != nil {
			*b.value = value
		}
		if *b.value == nil {
			continue
		}
		if inverse != "" {
			flag := cmd.Flags().Lookup(inverse)
			if err := flag.Value.Set("false"); err != nil {
				return err
			}
			flag.Changed = false
		}
		if err := cmd.Flags().Set(b.flag, fmt.Sprint(**b.value)); err != nil {
			return err
		}
	}
	for _, b := range bindings.integers {
		if flagChanged(cmd, b.flag) {
			value, _ := cmd.Flags().GetInt64(b.flag)
			*b.value = &value
		}
		if *b.value != nil {
			if err := cmd.Flags().Set(b.flag, fmt.Sprint(**b.value)); err != nil {
				return err
			}
		}
	}
	for _, b := range bindings.arrays {
		if flagChanged(cmd, b.flag) {
			*b.value, _ = cmd.Flags().GetStringArray(b.flag)
		}
		if *b.value != nil {
			values := make([]string, 0, len(*b.value))
			for _, value := range *b.value {
				if value = strings.TrimSpace(value); value != "" {
					values = append(values, value)
				}
			}
			if err := replaceWebTTYArray(cmd, b.flag, values); err != nil {
				return err
			}
		}
	}
	if resolved.Server.Rstream != nil && !*resolved.Server.Rstream && (webTTYString(resolved.Server.ServerID) != "" || webTTYString(resolved.Server.ServerEnrollment) != "") {
		return fmt.Errorf("server.serverId and server.serverEnrollment imply rstream mode")
	}
	return applyWebTTYLabels(cmd, bindings.labels)
}

func webTTYString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func replaceWebTTYArray(cmd *cobra.Command, name string, values []string) error {
	flag := cmd.Flags().Lookup(name)
	if err := flag.Value.(pflag.SliceValue).Replace(values); err != nil {
		return err
	}
	flag.Changed = true
	return nil
}

func applyWebTTYLabels(cmd *cobra.Command, binding webTTYBinding[map[string]string]) error {
	labels := *binding.value
	if flagChanged(cmd, binding.flag) {
		labels = map[string]string{}
		values, _ := cmd.Flags().GetStringArray(binding.flag)
		for _, value := range values {
			key, val, ok := strings.Cut(value, "=")
			if !ok {
				return fmt.Errorf("--label expects key=value")
			}
			if _, exists := labels[key]; exists {
				return fmt.Errorf("duplicate label key %q", key)
			}
			labels[key] = val
		}
	}
	if labels == nil {
		return nil
	}
	normalized := map[string]string{}
	for key, value := range labels {
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("label key must not be empty")
		}
		if _, exists := normalized[key]; exists {
			return fmt.Errorf("duplicate label key %q", key)
		}
		normalized[key] = strings.TrimSpace(value)
	}
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+normalized[key])
	}
	return replaceWebTTYArray(cmd, binding.flag, values)
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

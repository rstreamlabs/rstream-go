// See LICENSE file in the project root for license information.

//go:build !rstream_fips

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func execAuth(t *testing.T) *Auth {
	t.Helper()
	return &Auth{MTLS: &MTLS{Storage: &MTLSStorage{
		Kind: MTLSStorageExec, CertificateSHA256: strings.Repeat("12", 32),
		Exec: &MTLSExecStorage{Command: filepath.Join(t.TempDir(), "not-installed-helper"), Args: []string{"--identity", "device"}, Timeout: "3s", MaxConcurrency: 2, PassEnv: []string{"DEVICE_PIN"}},
	}}}
}

func TestExecMTLSConfigurationRoundTripWithoutExecution(t *testing.T) {
	cfg := Config{Contexts: []Context{{Name: "device", Engine: "device.example:443", Auth: execAuth(t)}}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Contexts, loaded.Contexts) {
		t.Fatal("exec configuration did not round trip")
	}
	resolved, err := Resolve(ResolveInput{Config: loaded, FlagContext: "device", RequireToken: true, RequireEngine: true})
	if err != nil {
		t.Fatalf("resolution executed the unavailable helper: %v", err)
	}
	if !resolved.HasMTLS() || resolved.Token != "" || resolved.TLSClientConfig.RootCAs != nil {
		t.Fatal("external identity was not selected independently of server trust")
	}
	if _, err := resolved.CheckExternalMTLS(t.Context()); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("explicit probe did not execute helper: %v", err)
	}
}

func TestExecMTLSValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*MTLS)
		want   string
	}{
		{"missing-exec", func(m *MTLS) { m.Storage.Exec = nil }, "requires exec settings"},
		{"missing-pin", func(m *MTLS) { m.Storage.CertificateSHA256 = "" }, "SHA-256"},
		{"relative-command", func(m *MTLS) { m.Storage.Exec.Command = "helper" }, "absolute"},
		{"mixed-alias", func(m *MTLS) { m.KeyFile = "key.pem" }, "mixed"},
		{"mixed-backend", func(m *MTLS) { m.Storage.Module = "module.so" }, "another backend"},
		{"invalid-timeout", func(m *MTLS) { m.Storage.Exec.Timeout = "-1s" }, "timeout"},
		{"invalid-concurrency", func(m *MTLS) { m.Storage.Exec.MaxConcurrency = 99 }, "maxConcurrency"},
		{"invalid-env", func(m *MTLS) { m.Storage.Exec.PassEnv = []string{"VALUE=secret"} }, "passEnv"},
		{"pkcs11-exec", func(m *MTLS) { m.Storage.Kind = MTLSStoragePKCS11 }, "cannot include exec"},
		{"keychain-exec", func(m *MTLS) { m.Storage.Kind = MTLSStorageKeychain }, "cannot include exec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := execAuth(t)
			tc.change(auth.MTLS)
			if _, _, err := MTLSConfigFromAuth(auth); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestAuthSelectionDoesNotMixContextAndEnvironment(t *testing.T) {
	contextAuth := execAuth(t)
	envAuth := &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "administrative-token"}}}
	cfg := Config{
		Contexts:     []Context{{Name: "device", APIURL: "https://api.example", Engine: "device.example:443", Auth: contextAuth}},
		Environments: []Environment{{APIURL: "https://api.example", Auth: envAuth}},
	}
	input := ResolveInput{Config: cfg, FlagContext: "device", ResolveToken: true, RequireToken: true}
	resolved, err := Resolve(input)
	if err != nil || !resolved.HasMTLS() || resolved.Token != "" {
		t.Fatalf("mTLS inherited an environment token: %v", err)
	}
	input.TokenOnly = true
	resolved, err = Resolve(input)
	if err != nil || resolved.HasMTLS() || resolved.Token != "administrative-token" {
		t.Fatalf("administrative API did not resolve its separate token: %v", err)
	}
	input.EnvEngine = "other.example:443"
	if _, err := Resolve(input); err == nil || !strings.Contains(err.Error(), "stored token") {
		t.Fatalf("administrative token accepted for another Engine: %v", err)
	}
	input.EnvEngine = ""
	input.TokenOnly = false
	input.Config.Contexts[0].Auth, input.Config.Environments[0].Auth = envAuth, contextAuth
	resolved, err = Resolve(input)
	if err != nil || resolved.HasMTLS() || resolved.Token != "administrative-token" {
		t.Fatalf("token context inherited environment mTLS: %v", err)
	}
	input.Config.Contexts[0].Auth = nil
	resolved, err = Resolve(input)
	if err != nil || !resolved.HasMTLS() {
		t.Fatalf("environment mTLS fallback failed: %v", err)
	}
	input.EnvToken = "explicit-token"
	if _, err = Resolve(input); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("explicit conflict accepted: %v", err)
	}
}

func TestExecMTLSOverridesAndEngineBinding(t *testing.T) {
	cfg := Config{Contexts: []Context{{Name: "device", Engine: "device.example:443", Auth: execAuth(t)}}}
	input := ResolveInput{Config: cfg, FlagContext: "device", EnvEngine: "other.example:443"}
	if _, err := Resolve(input); err == nil || !strings.Contains(err.Error(), "stored mTLS") {
		t.Fatalf("stored identity accepted for another Engine: %v", err)
	}
	cert, key := writeTestClientCertificate(t)
	input.EnvMTLSCert, input.EnvMTLSKey = cert, key
	resolved, err := Resolve(input)
	if err != nil || resolved.TLSClientConfig.GetClientCertificate != nil || len(resolved.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("explicit file identity did not replace exec: %v", err)
	}
	if cert, err := resolved.CheckExternalMTLS(t.Context()); err != nil || cert != nil {
		t.Fatal("diagnostic probed a shadowed exec credential")
	}
	input.EnvMTLSKey = ""
	if _, err := Resolve(input); err == nil {
		t.Fatal("incomplete explicit file override fell back to exec")
	}
}

func TestExecMTLSUnknownYAMLSettingRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "contexts:\n  - name: device\n    auth:\n      mtls:\n        storage:\n          kind: exec\n          exec:\n            command: /helper\n            timout: 2s\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "timout") {
		t.Fatalf("unknown exec YAML field accepted: %v", err)
	}
}

// See LICENSE file in the project root for license information.

package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
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
	if resolved.StableDomainEndpoint() != "engine.example:443" {
		t.Fatalf("expected Stable domain endpoint from context, got %q", resolved.StableDomainEndpoint())
	}
	if resolved.Token != "env-var-token" {
		t.Fatalf("expected env token override, got %q", resolved.Token)
	}
}

func TestResolveRegionPrecedence(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "global"}},
		Contexts: []Context{{
			Name:            "global",
			Engine:          "project.global.example.test:443",
			ProjectEndpoint: "project",
			Region:          "eu-west-3",
		}},
	}
	resolved, err := Resolve(ResolveInput{Config: cfg, EnvRegion: "us-east-1", FlagRegion: "EU-CENTRAL-1", RequireEngine: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Region != "eu-central-1" {
		t.Fatalf("region = %q, want eu-central-1", resolved.Region)
	}
	resolved, err = Resolve(ResolveInput{Config: cfg, RequireEngine: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Region != "eu-west-3" {
		t.Fatalf("region = %q, want eu-west-3", resolved.Region)
	}
}

func TestResolveRegionRejectsUnsafeConfigurations(t *testing.T) {
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "global"}},
		Contexts: []Context{{Name: "global", ProjectEndpoint: "project"}},
	}
	_, err := Resolve(ResolveInput{Config: cfg, FlagEngine: "direct.example.test:443", FlagRegion: "eu-west-3"})
	if err == nil || !strings.Contains(err.Error(), "explicit engine override") {
		t.Fatalf("expected engine conflict, got %v", err)
	}
	_, err = Resolve(ResolveInput{Config: Config{}, FlagRegion: "eu-west-3"})
	if err == nil || !strings.Contains(err.Error(), "managed project endpoint") {
		t.Fatalf("expected managed endpoint error, got %v", err)
	}
	_, err = Resolve(ResolveInput{Config: cfg, FlagRegion: "eu west 3"})
	if err == nil || !strings.Contains(err.Error(), "can only contain") {
		t.Fatalf("expected invalid region error, got %v", err)
	}
}

func TestResolveControlPlaneHeaders(t *testing.T) {
	cfg := Config{Environments: []Environment{{APIURL: "https://rstream.io", Headers: map[string]string{"X-Environment": "stored", "X-Override": "stored"}}}}
	resolved, err := Resolve(ResolveInput{Config: cfg, EnvControlPlaneHeaders: `{"x-override":"runtime"}`})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(resolved.ControlPlaneHeaders) != 2 || resolved.ControlPlaneHeaders["X-Environment"] != "stored" || resolved.ControlPlaneHeaders["X-Override"] != "runtime" {
		t.Fatalf("headers = %#v", resolved.ControlPlaneHeaders)
	}
	if _, err := Resolve(ResolveInput{Config: cfg, EnvControlPlaneHeaders: `{`}); err == nil || !strings.Contains(err.Error(), "invalid RSTREAM_CONTROL_PLANE_HEADERS JSON") {
		t.Fatalf("Resolve() error = %v, want JSON error", err)
	}
	if _, err := Resolve(ResolveInput{Config: Config{Environments: []Environment{{APIURL: "https://rstream.io", Headers: map[string]string{"Authorization": "bad"}}}}}); err == nil || !strings.Contains(err.Error(), "reserved control plane header") {
		t.Fatalf("Resolve() error = %v, want reserved header error", err)
	}
	if _, err := Resolve(ResolveInput{Config: Config{}, EnvControlPlaneHeaders: `{"X-Test":"first","x-test":"second"}`}); err == nil || !strings.Contains(err.Error(), "duplicate control plane header") {
		t.Fatalf("Resolve() error = %v, want duplicate header error", err)
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

func TestResolveContextEngineTLSConfig(t *testing.T) {
	certFile, _ := writeTestClientCertificate(t)
	cfg := Config{
		Defaults: Defaults{
			Context: &DefaultContext{
				Name: "local",
			},
		},
		Contexts: []Context{
			{
				Name:   "local",
				Engine: "engine.local:8443",
				Auth:   &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "token"}}},
				Transport: &TransportConfig{
					TLS: &TLSConfig{
						CAFile:     certFile,
						ServerName: "engine.local",
					},
				},
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
	if resolved.TLSClientConfig == nil || resolved.TLSClientConfig.RootCAs == nil {
		t.Fatalf("expected engine CA roots, got %#v", resolved.TLSClientConfig)
	}
	if resolved.TLSClientConfig.ServerName != "engine.local" {
		t.Fatalf("engine TLS server name = %q", resolved.TLSClientConfig.ServerName)
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

func TestResolveSupportsMTLSAuthAndRejectsTokenConflict(t *testing.T) {
	certFile, keyFile := writeTestClientCertificate(t)
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "primary"}},
		Contexts: []Context{{
			Name:   "primary",
			Engine: "engine.example.com:443",
			Auth:   &Auth{MTLS: &MTLS{CertificateFile: certFile, KeyFile: keyFile}},
		}},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		RequireToken:  true,
	})
	if err != nil {
		t.Fatalf("Resolve(mTLS) error = %v", err)
	}
	if resolved.Token != "" || resolved.TLSClientConfig == nil || len(resolved.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("unexpected mTLS resolution: %#v", resolved)
	}
	cfg.Contexts[0].Auth = &Auth{MTLS: &MTLS{Certificate: string(certPEM), Key: string(keyPEM)}}
	resolved, err = Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		RequireToken:  true,
	})
	if err != nil {
		t.Fatalf("Resolve(inline mTLS) error = %v", err)
	}
	if resolved.Token != "" || resolved.TLSClientConfig == nil || len(resolved.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("unexpected inline mTLS resolution: %#v", resolved)
	}
	_, err = Resolve(ResolveInput{
		Config:       cfg,
		FlagToken:    "token",
		ResolveToken: true,
	})
	if err == nil || !strings.Contains(err.Error(), "token and mTLS authentication cannot be used together") {
		t.Fatalf("expected token/mTLS conflict, got %v", err)
	}
}

func TestResolveExplicitMTLSSuppressesStoredToken(t *testing.T) {
	certFile, keyFile := writeTestClientCertificate(t)
	cfg := Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "primary"}},
		Contexts: []Context{{
			Name:   "primary",
			Engine: "engine.example.com:443",
			Auth: &Auth{Token: &Token{Storage: &TokenStorage{
				Kind:  TokenStorageInline,
				Value: "stored-token",
			}}},
		}},
	}
	resolved, err := Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		RequireToken:  true,
		EnvMTLSCert:   certFile,
		EnvMTLSKey:    keyFile,
	})
	if err != nil {
		t.Fatalf("Resolve(explicit mTLS) error = %v", err)
	}
	if resolved.Token != "" || resolved.TLSClientConfig == nil {
		t.Fatalf("explicit mTLS should suppress stored token: %#v", resolved)
	}
	_, err = Resolve(ResolveInput{
		Config:        cfg,
		RequireEngine: true,
		RequireToken:  true,
		EnvToken:      "env-token",
		EnvMTLSCert:   certFile,
		EnvMTLSKey:    keyFile,
	})
	if err == nil || !strings.Contains(err.Error(), "token and mTLS authentication cannot be used together") {
		t.Fatalf("expected explicit token/mTLS conflict, got %v", err)
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
		{name: "keychain missing provider", auth: &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageKeychain}}}},
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

func TestMacOSKeychainTokenStorageValidation(t *testing.T) {
	if got := DefaultMacOSKeychainTokenAccount(" https://api.example.com/ "); got != "api:https://api.example.com" {
		t.Fatalf("DefaultMacOSKeychainTokenAccount() = %q", got)
	}
	if got := DefaultMacOSKeychainContextTokenAccount("prod", " https://api.example.com/ "); got != "context:https://api.example.com:prod" {
		t.Fatalf("DefaultMacOSKeychainContextTokenAccount(linked) = %q", got)
	}
	if got := DefaultMacOSKeychainContextTokenAccount("local", ""); got != "context:local" {
		t.Fatalf("DefaultMacOSKeychainContextTokenAccount(unlinked) = %q", got)
	}
	storage := NewMacOSKeychainTokenStorage("https://api.example.com")
	if storage.Kind != TokenStorageKeychain || storage.Provider != CredentialProviderMacOS ||
		storage.Service != DefaultMacOSKeychainTokenService || storage.Account != "api:https://api.example.com" {
		t.Fatalf("unexpected storage: %#v", storage)
	}
	tests := []struct {
		name    string
		storage TokenStorage
		wantErr string
	}{
		{name: "missing provider", storage: TokenStorage{Kind: TokenStorageKeychain, Service: "svc", Account: "acct"}, wantErr: "provider is required"},
		{name: "unknown provider", storage: TokenStorage{Kind: TokenStorageKeychain, Provider: "windows", Service: "svc", Account: "acct"}, wantErr: "not supported"},
		{name: "missing service", storage: TokenStorage{Kind: TokenStorageKeychain, Provider: CredentialProviderMacOS, Account: "acct"}, wantErr: "service is required"},
		{name: "missing account", storage: TokenStorage{Kind: TokenStorageKeychain, Provider: CredentialProviderMacOS, Service: "svc"}, wantErr: "account is required"},
		{name: "inline value", storage: TokenStorage{Kind: TokenStorageKeychain, Provider: CredentialProviderMacOS, Service: "svc", Account: "acct", Value: "token"}, wantErr: "inline value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateMacOSKeychainTokenStorage(&tc.storage); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateMacOSKeychainTokenStorage() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestMTLSStorageValidation(t *testing.T) {
	certFile, _ := writeTestClientCertificate(t)
	if err := validatePKCS11MTLSStorage(&MTLSStorage{
		Kind:            MTLSStoragePKCS11,
		Module:          "/tmp/pkcs11.so",
		OpenSSLProvider: "pkcs11",
		TokenLabel:      "token",
		KeyLabel:        "key",
		CertificateFile: certFile,
		PINEnv:          "RSTREAM_TEST_PIN",
	}); err != nil {
		t.Fatalf("validatePKCS11MTLSStorage() error = %v", err)
	}
	tests := []struct {
		name    string
		mtls    *MTLS
		wantErr string
	}{
		{
			name: "storage mixed with alias",
			mtls: &MTLS{
				CertificateFile: certFile,
				Storage:         &MTLSStorage{Kind: MTLSStoragePKCS11},
			},
			wantErr: "cannot be mixed",
		},
		{
			name:    "missing kind",
			mtls:    &MTLS{Storage: &MTLSStorage{}},
			wantErr: "kind is required",
		},
		{
			name: "pkcs11 missing token selector",
			mtls: &MTLS{Storage: &MTLSStorage{
				Kind:            MTLSStoragePKCS11,
				Module:          "/tmp/pkcs11.so",
				KeyLabel:        "key",
				CertificateFile: certFile,
				PINEnv:          "RSTREAM_TEST_PIN",
			}},
			wantErr: "exactly one token selector",
		},
		{
			name: "pkcs11 mixed certificate sources",
			mtls: &MTLS{Storage: &MTLSStorage{
				Kind:             MTLSStoragePKCS11,
				Module:           "/tmp/pkcs11.so",
				TokenLabel:       "token",
				KeyLabel:         "key",
				CertificateFile:  certFile,
				CertificateLabel: "cert",
				PINEnv:           "RSTREAM_TEST_PIN",
			}},
			wantErr: "cannot mix PEM and token certificate sources",
		},
		{
			name: "keychain missing provider",
			mtls: &MTLS{Storage: &MTLSStorage{
				Kind:              MTLSStorageKeychain,
				CertificateSHA256: strings.Repeat("a", 64),
			}},
			wantErr: "provider is required",
		},
		{
			name: "keychain invalid fingerprint",
			mtls: &MTLS{Storage: &MTLSStorage{
				Kind:              MTLSStorageKeychain,
				Provider:          CredentialProviderMacOS,
				CertificateSHA256: "not-hex",
			}},
			wantErr: "must be hexadecimal",
		},
		{
			name: "keychain pkcs11 fields",
			mtls: &MTLS{Storage: &MTLSStorage{
				Kind:              MTLSStorageKeychain,
				Provider:          CredentialProviderMacOS,
				CertificateSHA256: strings.Repeat("a", 64),
				Module:            "/tmp/pkcs11.so",
			}},
			wantErr: "contains pkcs11 fields",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, err := MTLSConfigFromAuth(&Auth{MTLS: tc.mtls})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || ok {
				t.Fatalf("MTLSConfigFromAuth() ok=%v err=%v, want %q", ok, err, tc.wantErr)
			}
		})
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

func writeTestClientCertificate(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "rstream-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	if err := os.WriteFile(certFile, pemBlock("CERTIFICATE", der), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, pemBlock("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func pemBlock(kind string, der []byte) []byte {
	return []byte("-----BEGIN " + kind + "-----\n" +
		base64.StdEncoding.EncodeToString(der) +
		"\n-----END " + kind + "-----\n")
}

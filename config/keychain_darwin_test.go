// See LICENSE file in the project root for license information.

//go:build darwin && cgo

package config

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMacOSKeychainAlgorithmSelection(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	tests := []struct {
		name    string
		pub     crypto.PublicKey
		opts    crypto.SignerOpts
		wantErr string
	}{
		{name: "rsa pkcs1", pub: &rsaKey.PublicKey, opts: crypto.SHA256},
		{name: "rsa pss", pub: &rsaKey.PublicKey, opts: &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA384}},
		{name: "ecdsa", pub: &ecdsaKey.PublicKey, opts: crypto.SHA256},
		{name: "unsupported pss salt", pub: &rsaKey.PublicKey, opts: &rsa.PSSOptions{SaltLength: 12, Hash: crypto.SHA256}, wantErr: "hash-length salt"},
		{name: "unsupported hash", pub: &ecdsaKey.PublicKey, opts: crypto.MD5, wantErr: "does not support ECDSA"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := macOSKeychainAlgorithm(tc.pub, tc.opts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("macOSKeychainAlgorithm() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("macOSKeychainAlgorithm() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestMacOSKeychainTokenIntegration(t *testing.T) {
	if os.Getenv("RSTREAM_TEST_MACOS_KEYCHAIN") != "1" {
		t.Skip("set RSTREAM_TEST_MACOS_KEYCHAIN=1 to run macOS keychain integration")
	}
	storage := TokenStorage{
		Kind:     TokenStorageKeychain,
		Provider: CredentialProviderMacOS,
		Service:  fmt.Sprintf("io.rstream.test.%d", time.Now().UnixNano()),
		Account:  "integration",
	}
	t.Cleanup(func() {
		_ = DeleteToken(storage)
	})
	if err := StoreToken(storage, "integration-token"); err != nil {
		t.Fatalf("StoreToken() error = %v", err)
	}
	token, ok, err := TokenFromAuth(&Auth{Token: &Token{Storage: &storage}})
	if err != nil || !ok || token != "integration-token" {
		t.Fatalf("TokenFromAuth() token=%q ok=%v err=%v", token, ok, err)
	}
	if err := DeleteToken(storage); err != nil {
		t.Fatalf("DeleteToken() error = %v", err)
	}
	if _, _, err := TokenFromAuth(&Auth{Token: &Token{Storage: &storage}}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("TokenFromAuth(deleted) error = %v, want not found", err)
	}
}

func TestMacOSKeychainMTLSIntegration(t *testing.T) {
	fingerprint := os.Getenv("RSTREAM_TEST_MACOS_KEYCHAIN_MTLS_SHA256")
	if fingerprint == "" {
		t.Skip("set RSTREAM_TEST_MACOS_KEYCHAIN_MTLS_SHA256 to run macOS keychain mTLS integration")
	}
	cfg, err := loadMTLSStorageConfig(&MTLSStorage{
		Kind:              MTLSStorageKeychain,
		Provider:          CredentialProviderMacOS,
		CertificateSHA256: fingerprint,
	})
	if err != nil {
		t.Fatalf("loadMTLSStorageConfig(keychain) error = %v", err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 || cfg.Certificates[0].PrivateKey == nil || cfg.Certificates[0].Leaf == nil {
		t.Fatalf("unexpected keychain TLS config: %#v", cfg)
	}
	signer, ok := cfg.Certificates[0].PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatalf("keychain private key does not implement crypto.Signer: %T", cfg.Certificates[0].PrivateKey)
	}
	digest := sha256.Sum256([]byte("rstream keychain integration"))
	signature, err := signer.Sign(nil, digest[:], crypto.SHA256)
	if err != nil {
		t.Fatalf("keychain signer failed: %v", err)
	}
	rsaPublicKey, ok := cfg.Certificates[0].Leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("keychain integration currently verifies RSA keys, got %T", cfg.Certificates[0].Leaf.PublicKey)
	}
	if err := rsa.VerifyPKCS1v15(rsaPublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("keychain signature verification failed: %v", err)
	}
}

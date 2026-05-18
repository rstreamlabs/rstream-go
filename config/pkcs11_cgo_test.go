// See LICENSE file in the project root for license information.

//go:build cgo

package config

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestPKCS11MTLSIntegration(t *testing.T) {
	if os.Getenv("RSTREAM_TEST_PKCS11") != "1" {
		t.Skip("set RSTREAM_TEST_PKCS11=1 to run PKCS#11 integration")
	}
	storage := MTLSStorage{
		Kind:             MTLSStoragePKCS11,
		Module:           requiredTestEnv(t, "RSTREAM_TEST_PKCS11_MODULE"),
		TokenLabel:       os.Getenv("RSTREAM_TEST_PKCS11_TOKEN_LABEL"),
		TokenSerial:      os.Getenv("RSTREAM_TEST_PKCS11_TOKEN_SERIAL"),
		KeyLabel:         os.Getenv("RSTREAM_TEST_PKCS11_KEY_LABEL"),
		KeyIDHex:         os.Getenv("RSTREAM_TEST_PKCS11_KEY_ID_HEX"),
		CertificateFile:  os.Getenv("RSTREAM_TEST_PKCS11_CERTIFICATE_FILE"),
		CertificateLabel: os.Getenv("RSTREAM_TEST_PKCS11_CERTIFICATE_LABEL"),
		CertificateIDHex: os.Getenv("RSTREAM_TEST_PKCS11_CERTIFICATE_ID_HEX"),
		PINEnv:           "RSTREAM_TEST_PKCS11_PIN",
	}
	if value := strings.TrimSpace(os.Getenv("RSTREAM_TEST_PKCS11_SLOT")); value != "" {
		slot, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("RSTREAM_TEST_PKCS11_SLOT must be an integer: %v", err)
		}
		storage.Slot = &slot
	}
	requiredTestEnv(t, storage.PINEnv)
	cfg, err := loadMTLSStorageConfig(&storage)
	if err != nil {
		t.Fatalf("loadMTLSStorageConfig(PKCS#11) error = %v", err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 || cfg.Certificates[0].PrivateKey == nil || cfg.Certificates[0].Leaf == nil {
		t.Fatalf("unexpected PKCS#11 TLS config: %#v", cfg)
	}
	signer, ok := cfg.Certificates[0].PrivateKey.(crypto.Signer)
	if !ok {
		t.Fatalf("PKCS#11 private key does not implement crypto.Signer: %T", cfg.Certificates[0].PrivateKey)
	}
	digest := sha256.Sum256([]byte("rstream pkcs11 integration"))
	signature, err := signer.Sign(nil, digest[:], crypto.SHA256)
	if err != nil {
		t.Fatalf("PKCS#11 signer failed: %v", err)
	}
	rsaPublicKey, ok := cfg.Certificates[0].Leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("PKCS#11 integration currently verifies RSA keys, got %T", cfg.Certificates[0].Leaf.PublicKey)
	}
	if err := rsa.VerifyPKCS1v15(rsaPublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("PKCS#11 signature verification failed: %v", err)
	}
}

func requiredTestEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

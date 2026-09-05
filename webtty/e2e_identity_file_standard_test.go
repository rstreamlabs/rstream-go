//go:build !rstream_fips

// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestKnownServerKeysFileSupportsLegacyAndFIPSCompatibleServers(t *testing.T) {
	legacy, err := GenerateE2EIdentityForSuite(KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM)
	if err != nil {
		t.Fatalf("GenerateE2EIdentityForSuite(legacy) error = %v", err)
	}
	fipsCompatible, err := GenerateE2EIdentityForSuite(KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce)
	if err != nil {
		t.Fatalf("GenerateE2EIdentityForSuite(FIPS-compatible) error = %v", err)
	}
	entry := func(name string, identity *E2EIdentity) KnownServerKeyEntry {
		return KnownServerKeyEntry{
			Name:      name,
			KeyID:     EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: EncodeE2EKeyMaterial(identity.PublicKey),
		}
	}
	path := filepath.Join(t.TempDir(), "known_servers.json")
	if err := WriteKnownServerKeysFile(path, KnownServerKeysFile{
		KnownServers: []KnownServerKeyEntry{
			entry("legacy", legacy),
			entry("fips-compatible", fipsCompatible),
		},
	}); err != nil {
		t.Fatalf("WriteKnownServerKeysFile() error = %v", err)
	}
	doc, err := ReadKnownServerKeysFile(path)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if doc.CryptoSuite != E2EMixedKeyFileCryptoSuite {
		t.Fatalf("crypto suite = %q, want %q", doc.CryptoSuite, E2EMixedKeyFileCryptoSuite)
	}
	keys, err := LoadKnownServerKeysFile(path)
	if err != nil {
		t.Fatalf("LoadKnownServerKeysFile() error = %v", err)
	}
	if len(keys) != 2 || !bytes.Equal(keys[0].KeyID, legacy.KeyID) || !bytes.Equal(keys[1].KeyID, fipsCompatible.KeyID) {
		t.Fatalf("loaded keys do not preserve both suites: %#v", keys)
	}
}

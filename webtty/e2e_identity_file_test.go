// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestE2EIdentityFileRoundTrip(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "identity.json")
	if err := WriteE2EIdentityFile(path, *identity); err != nil {
		t.Fatalf("WriteE2EIdentityFile() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode = %o, want 0600", info.Mode().Perm())
	}
	got, err := LoadE2EIdentityFile(path)
	if err != nil {
		t.Fatalf("LoadE2EIdentityFile() error = %v", err)
	}
	if !bytes.Equal(got.KeyID, identity.KeyID) ||
		!bytes.Equal(got.PublicKey, identity.PublicKey) ||
		!bytes.Equal(got.PrivateKey, identity.PrivateKey) {
		t.Fatalf("loaded identity does not match written identity")
	}
}

func TestLoadOrCreateE2EIdentityFileIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	first, err := LoadOrCreateE2EIdentityFile(path)
	if err != nil {
		t.Fatalf("LoadOrCreateE2EIdentityFile() error = %v", err)
	}
	second, err := LoadOrCreateE2EIdentityFile(path)
	if err != nil {
		t.Fatalf("LoadOrCreateE2EIdentityFile() second error = %v", err)
	}
	if !bytes.Equal(first.KeyID, second.KeyID) {
		t.Fatalf("LoadOrCreateE2EIdentityFile() generated different identities")
	}
}

func TestLoadOrCreateE2EIdentityFileConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	const goroutines = 8
	results := make(chan []byte, goroutines)
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, err := LoadOrCreateE2EIdentityFile(path)
			if err != nil {
				errs <- err
				return
			}
			results <- identity.KeyID
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("LoadOrCreateE2EIdentityFile() concurrent error = %v", err)
	}
	var first []byte
	for keyID := range results {
		if first == nil {
			first = keyID
			continue
		}
		if !bytes.Equal(first, keyID) {
			t.Fatalf("concurrent LoadOrCreateE2EIdentityFile() returned different identities")
		}
	}
}

func TestDecodeE2EIdentityRejectsMismatchedMaterial(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	other, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() second error = %v", err)
	}
	doc := E2EIdentityFile{
		Version:     E2EIdentityFileVersion,
		CryptoSuite: E2EKeyFileCryptoSuite,
		KeyID:       EncodeE2EKeyMaterial(identity.KeyID),
		PublicKey:   EncodeE2EKeyMaterial(other.PublicKey),
		PrivateKey:  EncodeE2EKeyMaterial(identity.PrivateKey),
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if _, err := DecodeE2EIdentityJSON(data); err == nil || !strings.Contains(err.Error(), "public key") {
		t.Fatalf("expected mismatched public key error, got %v", err)
	}
}

func TestDecodeE2EIdentityRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	doc := E2EIdentityFile{
		Version:     E2EIdentityFileVersion,
		CryptoSuite: E2EKeyFileCryptoSuite,
		KeyID:       EncodeE2EKeyMaterial(identity.KeyID),
		PublicKey:   EncodeE2EKeyMaterial(identity.PublicKey),
		PrivateKey:  EncodeE2EKeyMaterial(identity.PrivateKey),
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	object["unexpected"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("Marshal(unknown) error = %v", err)
	}
	if _, err := DecodeE2EIdentityJSON(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
	if _, err := DecodeE2EIdentityJSON(append(data, []byte("\n{}")...)); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("expected trailing JSON value error, got %v", err)
	}
}

func TestLoadE2EIdentityFileRejectsWeakPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by POSIX mode bits")
	}
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	data, err := EncodeE2EIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeE2EIdentityJSON() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "identity.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadE2EIdentityFile(path); err == nil || !strings.Contains(err.Error(), "must not be readable") {
		t.Fatalf("expected weak permissions error, got %v", err)
	}
}

func TestParseKnownServerKeyValidatesKeyID(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	publicKey := EncodeE2EKeyMaterial(identity.PublicKey)
	serverKey, err := ParseKnownServerKey(publicKey)
	if err != nil {
		t.Fatalf("ParseKnownServerKey(public) error = %v", err)
	}
	if !bytes.Equal(serverKey.KeyID, identity.KeyID) {
		t.Fatalf("ParseKnownServerKey(public) derived wrong key id")
	}
	keyID := EncodeE2EKeyMaterial(identity.KeyID)
	serverKey, err = ParseKnownServerKey(keyID + ":" + publicKey)
	if err != nil {
		t.Fatalf("ParseKnownServerKey(key:public) error = %v", err)
	}
	if !bytes.Equal(serverKey.PublicKey, identity.PublicKey) {
		t.Fatalf("ParseKnownServerKey(key:public) decoded wrong public key")
	}
	wrong, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() wrong error = %v", err)
	}
	wrongKeyID := EncodeE2EKeyMaterial(wrong.KeyID)
	if _, err := ParseKnownServerKey(wrongKeyID + ":" + publicKey); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatched key id error, got %v", err)
	}
}

func TestParseKnownServerEndpointIdentityValidatesSigningKeyID(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	public := identity.Public()
	encoded := KnownServerEndpointIdentityString(public)
	parsed, err := ParseKnownServerEndpointIdentity(encoded)
	if err != nil {
		t.Fatalf("ParseKnownServerEndpointIdentity() error = %v", err)
	}
	if !bytes.Equal(parsed.EncryptionKeyID, public.EncryptionKeyID) ||
		!bytes.Equal(parsed.EncryptionPublicKey, public.EncryptionPublicKey) ||
		!bytes.Equal(parsed.SigningKeyID, public.SigningKeyID) ||
		!bytes.Equal(parsed.SigningPublicKey, public.SigningPublicKey) {
		t.Fatalf("parsed endpoint identity mismatch: %#v", parsed)
	}
	wrong, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(wrong) error = %v", err)
	}
	parts := strings.Split(encoded, ":")
	parts[2] = EncodeE2EKeyMaterial(wrong.Signing.KeyID)
	if _, err := ParseKnownServerEndpointIdentity(strings.Join(parts, ":")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatched signing key id error, got %v", err)
	}
}

func TestDecodeE2EKeyMaterialRejectsNonCanonicalBase64URL(t *testing.T) {
	got, err := DecodeE2EKeyMaterial("AA", 1, "test key")
	if err != nil {
		t.Fatalf("DecodeE2EKeyMaterial(canonical) error = %v", err)
	}
	if !bytes.Equal(got, []byte{0}) {
		t.Fatalf("DecodeE2EKeyMaterial(canonical) = %v, want [0]", got)
	}
	for _, value := range []string{"AB", "AA==", "AA+"} {
		if _, err := DecodeE2EKeyMaterial(value, 1, "test key"); err == nil {
			t.Fatalf("DecodeE2EKeyMaterial(%q) expected error", value)
		}
	}
}

func TestLoadKnownServerKeysFile(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	doc := KnownServerKeysFile{
		Version:     E2EIdentityFileVersion,
		CryptoSuite: E2EKeyFileCryptoSuite,
		KnownServers: []KnownServerKeyEntry{{
			Name:      "server",
			KeyID:     EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "known_servers.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	serverKeys, err := LoadKnownServerKeysFile(path)
	if err != nil {
		t.Fatalf("LoadKnownServerKeysFile() error = %v", err)
	}
	if len(serverKeys) != 1 || !bytes.Equal(serverKeys[0].KeyID, identity.KeyID) {
		t.Fatalf("unexpected known server keys: %#v", serverKeys)
	}
}

func TestLoadKnownServerKeysFileRejectsWeakPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by POSIX mode bits")
	}
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	doc := KnownServerKeysFile{
		Version:     E2EIdentityFileVersion,
		CryptoSuite: E2EKeyFileCryptoSuite,
		KnownServers: []KnownServerKeyEntry{{
			Name:      "server",
			KeyID:     EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "known_servers.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadKnownServerKeysFile(path); err == nil || !strings.Contains(err.Error(), "must not be readable by group or others") {
		t.Fatalf("expected weak permissions error, got %v", err)
	}
}

func TestLoadKnownServerKeysFileRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	doc := KnownServerKeysFile{
		Version:     E2EIdentityFileVersion,
		CryptoSuite: E2EKeyFileCryptoSuite,
		KnownServers: []KnownServerKeyEntry{{
			Name:      "server",
			KeyID:     EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	object["unexpected"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("Marshal(unknown) error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "known_servers.json")
	if err := os.WriteFile(path, unknown, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadKnownServerKeysFile(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n{}")...), 0o600); err != nil {
		t.Fatalf("WriteFile(trailing) error = %v", err)
	}
	if _, err := LoadKnownServerKeysFile(path); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("expected trailing JSON value error, got %v", err)
	}
}

func TestUpdateKnownServerKeysFileConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_servers.json")
	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			identity, err := GenerateE2EIdentity()
			if err != nil {
				errs <- err
				return
			}
			_, err = UpdateKnownServerKeysFile(path, func(doc *KnownServerKeysFile) error {
				doc.KnownServers = append(doc.KnownServers, KnownServerKeyEntry{
					Name:      "server-" + EncodeE2EKeyMaterial([]byte{byte(index)}),
					KeyID:     EncodeE2EKeyMaterial(identity.KeyID),
					PublicKey: EncodeE2EKeyMaterial(identity.PublicKey),
				})
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("UpdateKnownServerKeysFile() concurrent error = %v", err)
	}
	doc, err := ReadKnownServerKeysFile(path)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if len(doc.KnownServers) != goroutines {
		t.Fatalf("known server count = %d, want %d: %#v", len(doc.KnownServers), goroutines, doc.KnownServers)
	}
}

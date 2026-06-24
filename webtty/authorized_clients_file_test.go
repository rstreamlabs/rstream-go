// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAuthorizedClientKeysFileAddReadAndResolve(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() client error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "authorized_clients.json")
	entry, err := NewAuthorizedClientKeyEntry("operator-workstation", KnownServerEndpointIdentityString(clientIdentity.Public()))
	if err != nil {
		t.Fatalf("NewAuthorizedClientKeyEntry() error = %v", err)
	}
	if _, err := UpdateAuthorizedClientKeysFile(path, func(doc *AuthorizedClientKeysFile) error {
		doc.AuthorizedClients = append(doc.AuthorizedClients, entry)
		return nil
	}); err != nil {
		t.Fatalf("UpdateAuthorizedClientKeysFile() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat authorized clients file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("authorized clients file mode = %o, want 0600", info.Mode().Perm())
	}
	keys, err := LoadAuthorizedClientSigningKeysFile(path)
	if err != nil {
		t.Fatalf("LoadAuthorizedClientSigningKeysFile() error = %v", err)
	}
	if !bytes.Equal(keys[string(clientIdentity.Signing.KeyID)], clientIdentity.Signing.PublicKey) {
		t.Fatalf("authorized client key was not loaded")
	}
	resolver := NewAuthorizedClientSigningKeyFileResolver(path)
	got, err := resolver(context.Background(), clientIdentity.Signing.KeyID)
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	if !bytes.Equal(got, clientIdentity.Signing.PublicKey) {
		t.Fatalf("resolver returned wrong key")
	}
	missing, err := resolver(context.Background(), serverIdentity.Signing.KeyID)
	if err != nil {
		t.Fatalf("resolver missing key error = %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("resolver returned key for missing client")
	}
}

func TestAuthorizedClientSigningKeyParserAcceptsEndpointIdentity(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	keyID, publicKey, err := ParseAuthorizedClientSigningKey(KnownServerEndpointIdentityString(identity.Public()))
	if err != nil {
		t.Fatalf("ParseAuthorizedClientSigningKey() error = %v", err)
	}
	if !bytes.Equal(keyID, identity.Signing.KeyID) || !bytes.Equal(publicKey, identity.Signing.PublicKey) {
		t.Fatalf("parser did not extract the signing identity")
	}
}

func TestAuthorizedClientSigningKeyResolverMissingFileIsNotFatal(t *testing.T) {
	resolver := NewAuthorizedClientSigningKeyFileResolver(filepath.Join(t.TempDir(), "missing.json"))
	got, err := resolver(context.Background(), []byte("missing"))
	if err != nil {
		t.Fatalf("resolver missing file error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("resolver returned key from missing file")
	}
}

func TestAuthorizedClientKeysRejectsLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by POSIX mode bits")
	}
	path := filepath.Join(t.TempDir(), "authorized_clients.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write authorized clients file: %v", err)
	}
	_, err := ReadAuthorizedClientKeysFile(path)
	if err == nil {
		t.Fatalf("expected loose permissions error")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected permissions error, got %v", err)
	}
}

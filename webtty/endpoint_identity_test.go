// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestWebTTYEndpointIdentityJSONRoundTrip(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	if len(identity.Encryption.KeyID) != E2EPayloadKeyIDSize {
		t.Fatalf("encryption key id length = %d, want %d", len(identity.Encryption.KeyID), E2EPayloadKeyIDSize)
	}
	if len(identity.Signing.KeyID) != WebTTYSigningKeyIDSize {
		t.Fatalf("signing key id length = %d, want %d", len(identity.Signing.KeyID), WebTTYSigningKeyIDSize)
	}
	data, err := EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	decoded, err := DecodeWebTTYEndpointIdentityJSON(data)
	if err != nil {
		t.Fatalf("DecodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	if !bytes.Equal(decoded.Encryption.PrivateKey, identity.Encryption.PrivateKey) {
		t.Fatal("decoded encryption private key does not match")
	}
	if !bytes.Equal(decoded.Signing.PrivateKey, identity.Signing.PrivateKey) {
		t.Fatal("decoded signing private key does not match")
	}
	if !bytes.Equal(decoded.Public().SigningKeyID, identity.Public().SigningKeyID) {
		t.Fatal("decoded signing key id does not match")
	}
}

func TestWebTTYEndpointSigningIdentitySignsClientProof(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	privateKey, err := ParseWebTTYSigningPrivateKey(identity.Signing.PrivateKey)
	if err != nil {
		t.Fatalf("ParseWebTTYSigningPrivateKey() error = %v", err)
	}
	transcript := testClientProofTranscript(t)
	transcript.ClientSigningKeyID = identity.Signing.KeyID
	hash, signature, err := SignWebTTYClientProofTranscript(nil, privateKey, transcript)
	if err != nil {
		t.Fatalf("SignWebTTYClientProofTranscript() error = %v", err)
	}
	if len(hash) != 32 {
		t.Fatalf("transcript hash length = %d, want 32", len(hash))
	}
	if err := VerifyWebTTYClientProofTranscript(identity.Signing.PublicKey, transcript, signature); err != nil {
		t.Fatalf("VerifyWebTTYClientProofTranscript() error = %v", err)
	}
	transcript.ClientPrincipalID = "other"
	if err := VerifyWebTTYClientProofTranscript(identity.Signing.PublicKey, transcript, signature); err == nil {
		t.Fatal("VerifyWebTTYClientProofTranscript() succeeded after principal changed")
	}
}

func TestLoadOrCreateWebTTYEndpointIdentityFileCreatesAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.identity.json")
	created, err := LoadOrCreateWebTTYEndpointIdentityFile(path)
	if err != nil {
		t.Fatalf("LoadOrCreateWebTTYEndpointIdentityFile() error = %v", err)
	}
	if len(created.Encryption.PrivateKey) == 0 {
		t.Fatal("created identity has no encryption private key")
	}
	reloaded, err := LoadWebTTYEndpointIdentityFile(path)
	if err != nil {
		t.Fatalf("LoadWebTTYEndpointIdentityFile() error = %v", err)
	}
	if !bytes.Equal(reloaded.Encryption.KeyID, created.Encryption.KeyID) {
		t.Fatal("reloaded encryption key id does not match created identity")
	}
	if !bytes.Equal(reloaded.Signing.KeyID, created.Signing.KeyID) {
		t.Fatal("reloaded signing key id does not match created identity")
	}
}

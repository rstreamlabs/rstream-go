// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestE2EPayloadCryptoRoundTrip(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("workspace/session"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	serverCrypto, err := NewE2EServerPayloadCrypto(clientCrypto.SessionKeyGrant, *serverIdentity)
	if err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto() error = %v", err)
	}
	stdin, err := clientCrypto.EncryptStdin(t.Context(), []byte("typed"))
	if err != nil {
		t.Fatalf("EncryptStdin() error = %v", err)
	}
	if bytes.Contains(stdin.Ciphertext, []byte("typed")) {
		t.Fatalf("ciphertext contains plaintext")
	}
	if stdin.PayloadCrypto == nil || len(stdin.PayloadCrypto.Nonce) != e2eAESGCMNonceSize {
		t.Fatalf("missing E2E payload nonce: %#v", stdin.PayloadCrypto)
	}
	plaintext, err := serverCrypto.DecryptStdin(t.Context(), stdin)
	if err != nil {
		t.Fatalf("DecryptStdin() error = %v", err)
	}
	if string(plaintext) != "typed" {
		t.Fatalf("DecryptStdin() = %q", plaintext)
	}
	stdout, err := serverCrypto.EncryptStdout(t.Context(), []byte("output"))
	if err != nil {
		t.Fatalf("EncryptStdout() error = %v", err)
	}
	if _, err := clientCrypto.DecryptStderr(t.Context(), stdout); err == nil {
		t.Fatalf("expected stream AAD mismatch to fail")
	}
	tamperedSuite := *stdout
	tamperedCrypto := *stdout.PayloadCrypto
	tamperedCrypto.PayloadSuite = PayloadCipherSuiteChaCha20Poly1305
	tamperedSuite.PayloadCrypto = &tamperedCrypto
	if _, err := clientCrypto.DecryptStdout(t.Context(), &tamperedSuite); err == nil || !strings.Contains(err.Error(), "payload suite") {
		t.Fatalf("expected payload suite mismatch to fail, got %v", err)
	}
	plaintext, err = clientCrypto.DecryptStdout(t.Context(), stdout)
	if err != nil {
		t.Fatalf("DecryptStdout() error = %v", err)
	}
	if string(plaintext) != "output" {
		t.Fatalf("DecryptStdout() = %q", plaintext)
	}
}

func TestE2EPayloadCryptoBuildsTypedKeyContext(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	deviceIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		WorkspaceID: "workspace-1",
		ProjectID:   "project-1",
		ServerID:    "server-1",
		Recipients: []E2ERecipient{{
			ID:        "server-1",
			Kind:      E2ERecipientKindServer,
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}, {
			ID:        "device-1",
			Kind:      E2ERecipientKindWorkspaceDevice,
			KeyID:     deviceIdentity.KeyID,
			PublicKey: deviceIdentity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	if clientCrypto.SessionKeyGrant == nil || len(clientCrypto.SessionKeyGrant.KeyContext) == 0 {
		t.Fatalf("expected typed key context")
	}
	var raw map[string]any
	if err := json.Unmarshal(clientCrypto.SessionKeyGrant.KeyContext, &raw); err != nil {
		t.Fatalf("decode key context: %v", err)
	}
	if raw["type"] != "rstream.webtty.session_key_grant" || raw["workspace_id"] != "workspace-1" || raw["server_id"] != "server-1" {
		t.Fatalf("unexpected key context: %#v", raw)
	}
	recipients, ok := raw["recipients"].([]any)
	if !ok || len(recipients) != 2 {
		t.Fatalf("unexpected recipients: %#v", raw["recipients"])
	}
	serverCrypto, err := NewE2EServerPayloadCrypto(clientCrypto.SessionKeyGrant, *serverIdentity)
	if err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto(server) error = %v", err)
	}
	deviceCrypto, err := NewE2EServerPayloadCrypto(clientCrypto.SessionKeyGrant, *deviceIdentity)
	if err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto(device) error = %v", err)
	}
	payload, err := clientCrypto.EncryptStdin(t.Context(), []byte("hello"))
	if err != nil {
		t.Fatalf("EncryptStdin() error = %v", err)
	}
	if _, err := serverCrypto.DecryptStdin(t.Context(), payload); err != nil {
		t.Fatalf("server decrypt: %v", err)
	}
	if _, err := deviceCrypto.DecryptStdin(t.Context(), payload); err != nil {
		t.Fatalf("device decrypt: %v", err)
	}
}

func TestE2EPayloadCryptoRejectsWrongIdentity(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	wrongIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	_, err = NewE2EServerPayloadCrypto(clientCrypto.SessionKeyGrant, *wrongIdentity)
	if err == nil {
		t.Fatalf("expected wrong identity to fail")
	}
	if !strings.Contains(err.Error(), "does not contain a key envelope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestE2EPayloadCryptoRejectsInvalidKeyIDs(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	_, err = NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		PayloadKeyID: []byte("short"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "payload key id") {
		t.Fatalf("expected invalid payload key id error, got %v", err)
	}
	_, err = NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		Recipients: []E2ERecipient{{
			KeyID:     []byte("short"),
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "recipient key id") {
		t.Fatalf("expected invalid recipient key id error, got %v", err)
	}
	crypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	sessionKeyGrant := *crypto.SessionKeyGrant
	sessionKeyGrant.PayloadKeyID = []byte("short")
	if _, err := NewE2EServerPayloadCrypto(&sessionKeyGrant, *serverIdentity); err == nil || !strings.Contains(err.Error(), "payload key id") {
		t.Fatalf("expected invalid session payload key id error, got %v", err)
	}
}

func TestE2EPayloadCryptoRejectsUnsupportedSuites(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	_, err = NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		PayloadSuite: PayloadCipherSuiteChaCha20Poly1305,
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "payload suite") {
		t.Fatalf("expected unsupported payload suite error, got %v", err)
	}
	_, err = NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyEnvelopeSuite: KeyEnvelopeSuiteHPKEX25519HKDFSHA256ChaCha20Poly1305,
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "key envelope suite") {
		t.Fatalf("expected unsupported key envelope suite error, got %v", err)
	}
}

func TestE2EPayloadCryptoPayloadMetadata(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	defaultCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	defaultPayload, err := defaultCrypto.EncryptStdin(t.Context(), []byte("input"))
	if err != nil {
		t.Fatalf("EncryptStdin() error = %v", err)
	}
	if defaultPayload.PayloadCrypto == nil || len(defaultPayload.PayloadCrypto.Nonce) != e2eAESGCMNonceSize {
		t.Fatalf("payload crypto metadata should include a nonce: %#v", defaultPayload.PayloadCrypto)
	}
	if len(defaultPayload.PayloadCrypto.AADContext) == 0 {
		t.Fatalf("payload crypto metadata should include AAD context: %#v", defaultPayload.PayloadCrypto)
	}
}

func TestE2EPayloadCryptoRejectsTamperedPayloadMetadata(t *testing.T) {
	serverIdentity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("workspace/session"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.KeyID,
			PublicKey: serverIdentity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	serverCrypto, err := NewE2EServerPayloadCrypto(clientCrypto.SessionKeyGrant, *serverIdentity)
	if err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto() error = %v", err)
	}
	payload, err := clientCrypto.EncryptStdin(t.Context(), []byte("typed"))
	if err != nil {
		t.Fatalf("EncryptStdin() error = %v", err)
	}
	tamperedContext := *payload
	tamperedContextCrypto := *payload.PayloadCrypto
	tamperedContextCrypto.AADContext = []byte("other/session")
	tamperedContext.PayloadCrypto = &tamperedContextCrypto
	if _, err := serverCrypto.DecryptStdin(t.Context(), &tamperedContext); err == nil || !strings.Contains(err.Error(), "key context") {
		t.Fatalf("expected tampered key context error, got %v", err)
	}
	tamperedNonce := *payload
	tamperedNonceCrypto := *payload.PayloadCrypto
	tamperedNonceCrypto.Nonce = []byte("short")
	tamperedNonce.PayloadCrypto = &tamperedNonceCrypto
	if _, err := serverCrypto.DecryptStdin(t.Context(), &tamperedNonce); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected tampered nonce error, got %v", err)
	}
}

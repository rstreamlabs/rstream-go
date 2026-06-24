// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	E2EX25519PublicKeySize  = 32
	E2EX25519PrivateKeySize = 32
	E2EPayloadKeySize       = 32
	E2EPayloadKeyIDSize     = 16
	e2eAESGCMNonceSize      = 12
	e2eHPKEInfoDomain       = "rstream-webtty-e2e-key/v1"
	e2ePayloadAADDomain     = "rstream-webtty-e2e-payload/v1"
	e2eKeyIDDomain          = "rstream-webtty-e2e-key-id/v1"
)

type E2EIdentity struct {
	KeyID      []byte
	PublicKey  []byte
	PrivateKey []byte
}

type E2ERecipient struct {
	ID        string
	Kind      string
	KeyID     []byte
	PublicKey []byte
}

type E2EPayloadCryptoConfig struct {
	WorkspaceID      string
	ProjectID        string
	ServerID         string
	PayloadSuite     PayloadCipherSuite
	PayloadKey       []byte
	PayloadKeyID     []byte
	KeyContext       []byte
	KeyEnvelopeSuite KeyEnvelopeSuite
	Recipients       []E2ERecipient
	Random           io.Reader
}

type e2ePayloadCipher struct {
	payloadSuite     PayloadCipherSuite
	payloadKey       []byte
	payloadKeyID     []byte
	keyContext       []byte
	keyEnvelopeSuite KeyEnvelopeSuite
	keyEnvelopes     []KeyEnvelope
	random           io.Reader
}

const (
	E2ERecipientKindPublicKey       = "public_key"
	E2ERecipientKindUser            = "user"
	E2ERecipientKindWorkspaceDevice = "workspace_device"
	E2ERecipientKindWorkspaceKeyset = "workspace_keyset"
	E2ERecipientKindServer          = "server"
)

func GenerateE2EIdentity() (*E2EIdentity, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 key: %w", err)
	}
	publicKey := privateKey.PublicKey().Bytes()
	return &E2EIdentity{
		KeyID:      E2EKeyID(publicKey),
		PublicKey:  cloneBytes(publicKey),
		PrivateKey: cloneBytes(privateKey.Bytes()),
	}, nil
}

func E2EKeyID(publicKey []byte) []byte {
	digest := sha256.Sum256(append([]byte(e2eKeyIDDomain), publicKey...))
	return cloneBytes(digest[:E2EPayloadKeyIDSize])
}

func NewE2EClientPayloadCrypto(cfg E2EPayloadCryptoConfig) (*PayloadCrypto, error) {
	cipher, err := newE2EClientPayloadCipher(cfg)
	if err != nil {
		return nil, err
	}
	return cipher.payloadCrypto(), nil
}

func NewE2EServerPayloadCrypto(sessionKeyGrant *SessionKeyGrant, identity E2EIdentity) (*PayloadCrypto, error) {
	cipher, err := newE2EServerPayloadCipher(sessionKeyGrant, identity)
	if err != nil {
		return nil, err
	}
	return cipher.payloadCrypto(), nil
}

func NewE2EServerPayloadCryptoResolver(identity E2EIdentity) PayloadCryptoResolver {
	return func(_ context.Context, sessionKeyGrant *SessionKeyGrant) (*PayloadCrypto, error) {
		return NewE2EServerPayloadCrypto(sessionKeyGrant, identity)
	}
}

func newE2EClientPayloadCipher(cfg E2EPayloadCryptoConfig) (*e2ePayloadCipher, error) {
	payloadSuite := cfg.PayloadSuite
	if payloadSuite == 0 {
		payloadSuite = PayloadCipherSuiteAES256GCM
	}
	keyEnvelopeSuite := cfg.KeyEnvelopeSuite
	if keyEnvelopeSuite == 0 {
		keyEnvelopeSuite = KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM
	}
	if err := validateE2ESuites(payloadSuite, keyEnvelopeSuite); err != nil {
		return nil, err
	}
	random := cfg.Random
	if random == nil {
		random = rand.Reader
	}
	payloadKey := cloneBytes(cfg.PayloadKey)
	if len(payloadKey) == 0 {
		payloadKey = make([]byte, E2EPayloadKeySize)
		if _, err := io.ReadFull(random, payloadKey); err != nil {
			return nil, fmt.Errorf("generate E2E payload key: %w", err)
		}
	}
	if len(payloadKey) != E2EPayloadKeySize {
		return nil, fmt.Errorf("E2E payload key must be %d bytes", E2EPayloadKeySize)
	}
	payloadKeyID := cloneBytes(cfg.PayloadKeyID)
	if len(payloadKeyID) == 0 {
		payloadKeyID = make([]byte, E2EPayloadKeyIDSize)
		if _, err := io.ReadFull(random, payloadKeyID); err != nil {
			return nil, fmt.Errorf("generate E2E payload key id: %w", err)
		}
	}
	if len(payloadKeyID) != E2EPayloadKeyIDSize {
		return nil, fmt.Errorf("E2E payload key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	if len(cfg.Recipients) == 0 {
		return nil, fmt.Errorf("E2E client payload crypto requires at least one recipient")
	}
	keyContext := cloneBytes(cfg.KeyContext)
	if len(keyContext) == 0 {
		var err error
		keyContext, err = implicitE2EKeyContextFromRecipients(cfg)
		if err != nil {
			return nil, err
		}
	}
	keyEnvelopes := make([]KeyEnvelope, 0, len(cfg.Recipients))
	for _, recipient := range cfg.Recipients {
		envelope, err := wrapE2EPayloadKey(payloadKey, payloadSuite, payloadKeyID, keyContext, keyEnvelopeSuite, recipient)
		if err != nil {
			return nil, err
		}
		keyEnvelopes = append(keyEnvelopes, envelope)
	}
	return &e2ePayloadCipher{
		payloadSuite:     payloadSuite,
		payloadKey:       payloadKey,
		payloadKeyID:     payloadKeyID,
		keyContext:       keyContext,
		keyEnvelopeSuite: keyEnvelopeSuite,
		keyEnvelopes:     keyEnvelopes,
		random:           random,
	}, nil
}

func implicitE2EKeyContextFromRecipients(cfg E2EPayloadCryptoConfig) ([]byte, error) {
	type keyContextRecipient struct {
		KeyID string `json:"key_id"`
		Kind  string `json:"kind"`
		ID    string `json:"id"`
	}
	type keyContext struct {
		Version    int                   `json:"v"`
		Type       string                `json:"type"`
		Workspace  string                `json:"workspace_id,omitempty"`
		ProjectID  string                `json:"project_id,omitempty"`
		ServerID   string                `json:"server_id,omitempty"`
		Recipients []keyContextRecipient `json:"recipients"`
	}
	recipients := make([]keyContextRecipient, 0, len(cfg.Recipients))
	for _, recipient := range cfg.Recipients {
		kind := strings.TrimSpace(recipient.Kind)
		id := strings.TrimSpace(recipient.ID)
		keyID := cloneBytes(recipient.KeyID)
		if len(keyID) == 0 {
			keyID = E2EKeyID(recipient.PublicKey)
		}
		if len(keyID) != E2EPayloadKeyIDSize {
			return nil, fmt.Errorf("E2E recipient key id must be %d bytes", E2EPayloadKeyIDSize)
		}
		if kind == "" && id == "" {
			kind = E2ERecipientKindPublicKey
			id = EncodeE2EKeyMaterial(keyID)
		}
		if kind == "" || id == "" {
			return nil, fmt.Errorf("E2E typed recipients require both kind and id")
		}
		if !isKnownE2ERecipientKind(kind) {
			return nil, fmt.Errorf("unsupported E2E recipient kind %q", kind)
		}
		recipients = append(recipients, keyContextRecipient{
			KeyID: EncodeE2EKeyMaterial(keyID),
			Kind:  kind,
			ID:    id,
		})
	}
	data, err := json.Marshal(keyContext{
		Version:    1,
		Type:       "rstream.webtty.session_key_grant",
		Workspace:  strings.TrimSpace(cfg.WorkspaceID),
		ProjectID:  strings.TrimSpace(cfg.ProjectID),
		ServerID:   strings.TrimSpace(cfg.ServerID),
		Recipients: recipients,
	})
	if err != nil {
		return nil, fmt.Errorf("encode E2E key context: %w", err)
	}
	return data, nil
}

func isKnownE2ERecipientKind(kind string) bool {
	switch kind {
	case E2ERecipientKindPublicKey,
		E2ERecipientKindUser,
		E2ERecipientKindWorkspaceDevice,
		E2ERecipientKindWorkspaceKeyset,
		E2ERecipientKindServer:
		return true
	default:
		return false
	}
}

func newE2EServerPayloadCipher(sessionKeyGrant *SessionKeyGrant, identity E2EIdentity) (*e2ePayloadCipher, error) {
	if sessionKeyGrant == nil {
		return nil, fmt.Errorf("missing E2E session key grant")
	}
	if err := validateE2ESuites(sessionKeyGrant.PayloadSuite, sessionKeyGrant.KeyEnvelopeSuite); err != nil {
		return nil, err
	}
	if len(sessionKeyGrant.PayloadKeyID) != E2EPayloadKeyIDSize {
		return nil, fmt.Errorf("E2E payload key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	recipientKeyID, privateKey, err := e2eIdentityPrivateKey(identity)
	if err != nil {
		return nil, err
	}
	var matched *KeyEnvelope
	for i := range sessionKeyGrant.KeyEnvelopes {
		if bytes.Equal(sessionKeyGrant.KeyEnvelopes[i].RecipientKeyID, recipientKeyID) {
			matched = &sessionKeyGrant.KeyEnvelopes[i]
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("E2E session key grant does not contain a key envelope for this identity")
	}
	payloadKey, err := unwrapE2EPayloadKey(*matched, sessionKeyGrant.PayloadSuite, sessionKeyGrant.PayloadKeyID, sessionKeyGrant.KeyContext, sessionKeyGrant.KeyEnvelopeSuite, privateKey)
	if err != nil {
		return nil, err
	}
	return &e2ePayloadCipher{
		payloadSuite:     sessionKeyGrant.PayloadSuite,
		payloadKey:       payloadKey,
		payloadKeyID:     cloneBytes(sessionKeyGrant.PayloadKeyID),
		keyContext:       cloneBytes(sessionKeyGrant.KeyContext),
		keyEnvelopeSuite: sessionKeyGrant.KeyEnvelopeSuite,
		random:           rand.Reader,
	}, nil
}

func (c *e2ePayloadCipher) payloadCrypto() *PayloadCrypto {
	return &PayloadCrypto{
		Capabilities:    []OpenCapability{OpenCapabilityEncryptedPayload, OpenCapabilitySessionKeyGrant},
		SessionKeyGrant: c.sessionKeyGrant(),
		EncryptStdin:    c.encryptFunc("stdin"),
		DecryptStdin:    c.decryptFunc("stdin"),
		EncryptStdout:   c.encryptFunc("stdout"),
		DecryptStdout:   c.decryptFunc("stdout"),
		EncryptStderr:   c.encryptFunc("stderr"),
		DecryptStderr:   c.decryptFunc("stderr"),
	}
}

func (c *e2ePayloadCipher) sessionKeyGrant() *SessionKeyGrant {
	return &SessionKeyGrant{
		PayloadSuite:     c.payloadSuite,
		PayloadKeyID:     cloneBytes(c.payloadKeyID),
		KeyEnvelopes:     cloneKeyEnvelopes(c.keyEnvelopes),
		KeyContext:       cloneBytes(c.keyContext),
		KeyEnvelopeSuite: c.keyEnvelopeSuite,
	}
}

func (c *e2ePayloadCipher) encryptFunc(stream string) PayloadEncryptFunc {
	return func(_ context.Context, payload []byte) (*EncryptedPayload, error) {
		if uint64(len(payload)) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("E2E payload is too large: %d bytes", len(payload))
		}
		aead, err := c.payloadAEAD()
		if err != nil {
			return nil, err
		}
		nonce := make([]byte, e2eAESGCMNonceSize)
		if _, err := io.ReadFull(c.random, nonce); err != nil {
			return nil, fmt.Errorf("generate E2E nonce: %w", err)
		}
		plainLen := uint32(len(payload))
		metadata := &PayloadCryptoMetadata{
			PayloadSuite: c.payloadSuite,
			PayloadKeyID: cloneBytes(c.payloadKeyID),
			Nonce:        nonce,
			AADContext:   cloneBytes(c.keyContext),
		}
		aad := e2ePayloadAAD(stream, metadata.PayloadSuite, metadata.PayloadKeyID, metadata.AADContext, metadata.Nonce, plainLen)
		return &EncryptedPayload{
			Ciphertext:      aead.Seal(nil, metadata.Nonce, payload, aad),
			PlaintextLength: plainLen,
			PayloadCrypto:   metadata,
		}, nil
	}
}

func (c *e2ePayloadCipher) decryptFunc(stream string) PayloadDecryptFunc {
	return func(_ context.Context, payload *EncryptedPayload) ([]byte, error) {
		if payload == nil {
			return nil, fmt.Errorf("missing E2E encrypted payload")
		}
		if payload.PayloadCrypto == nil {
			return nil, fmt.Errorf("missing E2E payload crypto metadata")
		}
		if err := c.validatePayloadEnvelope(payload.PayloadCrypto); err != nil {
			return nil, err
		}
		aead, err := c.payloadAEAD()
		if err != nil {
			return nil, err
		}
		aad := e2ePayloadAAD(stream, payload.PayloadCrypto.PayloadSuite, payload.PayloadCrypto.PayloadKeyID, payload.PayloadCrypto.AADContext, payload.PayloadCrypto.Nonce, payload.PlaintextLength)
		plaintext, err := aead.Open(nil, payload.PayloadCrypto.Nonce, payload.Ciphertext, aad)
		if err != nil {
			return nil, fmt.Errorf("decrypt E2E %s payload: %w", stream, err)
		}
		if uint32(len(plaintext)) != payload.PlaintextLength {
			return nil, fmt.Errorf("E2E %s payload length mismatch: got %d want %d", stream, len(plaintext), payload.PlaintextLength)
		}
		return plaintext, nil
	}
}

func (c *e2ePayloadCipher) validatePayloadEnvelope(envelope *PayloadCryptoMetadata) error {
	if envelope.PayloadSuite != c.payloadSuite {
		return fmt.Errorf("unexpected E2E payload suite %d", envelope.PayloadSuite)
	}
	if !bytes.Equal(envelope.PayloadKeyID, c.payloadKeyID) {
		return fmt.Errorf("unexpected E2E payload key id")
	}
	if !bytes.Equal(envelope.AADContext, c.keyContext) {
		return fmt.Errorf("unexpected E2E key context")
	}
	if len(envelope.Nonce) != e2eAESGCMNonceSize {
		return fmt.Errorf("E2E AES-GCM nonce must be %d bytes", e2eAESGCMNonceSize)
	}
	return nil
}

func (c *e2ePayloadCipher) payloadAEAD() (cipher.AEAD, error) {
	if c.payloadSuite != PayloadCipherSuiteAES256GCM {
		return nil, fmt.Errorf("unsupported E2E payload suite %d", c.payloadSuite)
	}
	block, err := aes.NewCipher(c.payloadKey)
	if err != nil {
		return nil, fmt.Errorf("create E2E AES cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func validateE2ESuites(payloadSuite PayloadCipherSuite, keyEnvelopeSuite KeyEnvelopeSuite) error {
	if payloadSuite != PayloadCipherSuiteAES256GCM {
		return fmt.Errorf("unsupported E2E payload suite %d", payloadSuite)
	}
	if keyEnvelopeSuite != KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM {
		return fmt.Errorf("unsupported E2E key envelope suite %d", keyEnvelopeSuite)
	}
	return nil
}

func wrapE2EPayloadKey(payloadKey []byte, payloadSuite PayloadCipherSuite, payloadKeyID []byte, keyContext []byte, suite KeyEnvelopeSuite, recipient E2ERecipient) (KeyEnvelope, error) {
	if len(recipient.PublicKey) != E2EX25519PublicKeySize {
		return KeyEnvelope{}, fmt.Errorf("E2E recipient public key must be %d bytes", E2EX25519PublicKeySize)
	}
	keyID := cloneBytes(recipient.KeyID)
	if len(keyID) == 0 {
		keyID = E2EKeyID(recipient.PublicKey)
	}
	if len(keyID) != E2EPayloadKeyIDSize {
		return KeyEnvelope{}, fmt.Errorf("E2E recipient key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	publicKey, err := ecdh.X25519().NewPublicKey(recipient.PublicKey)
	if err != nil {
		return KeyEnvelope{}, fmt.Errorf("parse E2E recipient public key: %w", err)
	}
	hpkePublicKey, err := hpke.NewDHKEMPublicKey(publicKey)
	if err != nil {
		return KeyEnvelope{}, fmt.Errorf("create E2E HPKE public key: %w", err)
	}
	enc, sender, err := hpke.NewSender(hpkePublicKey, hpke.HKDFSHA256(), hpke.AES256GCM(), e2eHPKEInfo(payloadSuite, payloadKeyID, keyContext, suite))
	if err != nil {
		return KeyEnvelope{}, fmt.Errorf("create E2E HPKE sender: %w", err)
	}
	wrappedKey, err := sender.Seal(e2eHPKEAAD(keyID, payloadSuite, payloadKeyID, keyContext, suite), payloadKey)
	if err != nil {
		return KeyEnvelope{}, fmt.Errorf("wrap E2E payload key: %w", err)
	}
	return KeyEnvelope{
		RecipientKeyID:  keyID,
		EncapsulatedKey: enc,
		WrappedKey:      wrappedKey,
	}, nil
}

func unwrapE2EPayloadKey(envelope KeyEnvelope, payloadSuite PayloadCipherSuite, payloadKeyID []byte, keyContext []byte, suite KeyEnvelopeSuite, privateKey *ecdh.PrivateKey) ([]byte, error) {
	hpkePrivateKey, err := hpke.NewDHKEMPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("create E2E HPKE private key: %w", err)
	}
	recipient, err := hpke.NewRecipient(envelope.EncapsulatedKey, hpkePrivateKey, hpke.HKDFSHA256(), hpke.AES256GCM(), e2eHPKEInfo(payloadSuite, payloadKeyID, keyContext, suite))
	if err != nil {
		return nil, fmt.Errorf("create E2E HPKE recipient: %w", err)
	}
	payloadKey, err := recipient.Open(e2eHPKEAAD(envelope.RecipientKeyID, payloadSuite, payloadKeyID, keyContext, suite), envelope.WrappedKey)
	if err != nil {
		return nil, fmt.Errorf("unwrap E2E payload key: %w", err)
	}
	if len(payloadKey) != E2EPayloadKeySize {
		return nil, fmt.Errorf("E2E unwrapped payload key must be %d bytes", E2EPayloadKeySize)
	}
	return payloadKey, nil
}

func e2eIdentityPrivateKey(identity E2EIdentity) ([]byte, *ecdh.PrivateKey, error) {
	if len(identity.PrivateKey) != E2EX25519PrivateKeySize {
		return nil, nil, fmt.Errorf("E2E identity private key must be %d bytes", E2EX25519PrivateKeySize)
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(identity.PrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("parse E2E identity private key: %w", err)
	}
	keyID := cloneBytes(identity.KeyID)
	if len(keyID) == 0 {
		publicKey := cloneBytes(identity.PublicKey)
		if len(publicKey) == 0 {
			publicKey = privateKey.PublicKey().Bytes()
		}
		if len(publicKey) != E2EX25519PublicKeySize {
			return nil, nil, fmt.Errorf("E2E identity public key must be %d bytes", E2EX25519PublicKeySize)
		}
		keyID = E2EKeyID(publicKey)
	}
	if len(keyID) != E2EPayloadKeyIDSize {
		return nil, nil, fmt.Errorf("E2E identity key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	return keyID, privateKey, nil
}

func e2eHPKEInfo(payloadSuite PayloadCipherSuite, payloadKeyID []byte, keyContext []byte, suite KeyEnvelopeSuite) []byte {
	var out []byte
	out = appendLengthPrefixed(out, []byte(e2eHPKEInfoDomain))
	out = appendUint32(out, uint32(payloadSuite))
	out = appendUint32(out, uint32(suite))
	out = appendLengthPrefixed(out, payloadKeyID)
	out = appendLengthPrefixed(out, keyContext)
	return out
}

func e2eHPKEAAD(recipientKeyID []byte, payloadSuite PayloadCipherSuite, payloadKeyID []byte, keyContext []byte, suite KeyEnvelopeSuite) []byte {
	var out []byte
	out = appendLengthPrefixed(out, []byte("key-wrap"))
	out = appendUint32(out, uint32(payloadSuite))
	out = appendUint32(out, uint32(suite))
	out = appendLengthPrefixed(out, recipientKeyID)
	out = appendLengthPrefixed(out, payloadKeyID)
	out = appendLengthPrefixed(out, keyContext)
	return out
}

func e2ePayloadAAD(stream string, suite PayloadCipherSuite, payloadKeyID []byte, keyContext []byte, nonce []byte, plaintextLength uint32) []byte {
	var out []byte
	out = appendLengthPrefixed(out, []byte(e2ePayloadAADDomain))
	out = appendLengthPrefixed(out, []byte(stream))
	out = appendUint32(out, uint32(suite))
	out = appendLengthPrefixed(out, payloadKeyID)
	out = appendLengthPrefixed(out, keyContext)
	out = appendLengthPrefixed(out, nonce)
	out = appendUint32(out, plaintextLength)
	return out
}

func appendLengthPrefixed(dst []byte, value []byte) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendUint32(dst []byte, value uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	return append(dst, buf[:]...)
}

func cloneKeyEnvelopes(src []KeyEnvelope) []KeyEnvelope {
	if len(src) == 0 {
		return nil
	}
	out := make([]KeyEnvelope, 0, len(src))
	for _, envelope := range src {
		out = append(out, KeyEnvelope{
			RecipientKeyID:  cloneBytes(envelope.RecipientKeyID),
			EncapsulatedKey: cloneBytes(envelope.EncapsulatedKey),
			WrappedKey:      cloneBytes(envelope.WrappedKey),
		})
	}
	return out
}

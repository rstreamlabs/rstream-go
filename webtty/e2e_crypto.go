// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
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
	E2EP256PublicKeySize    = 65
	E2EP256PrivateKeySize   = 32
	E2EPayloadKeySize       = 32
	E2EPayloadKeyIDSize     = 16
	e2eAESGCMNonceSize      = 12
	e2eHPKEInfoDomain       = "rstream-webtty-e2e-key/v1"
	e2ePayloadAADDomain     = "rstream-webtty-e2e-payload/v1"
	e2eKeyIDDomain          = "rstream-webtty-e2e-key-id/v1"
)

type E2EIdentity struct {
	KeyEnvelopeSuite KeyEnvelopeSuite
	KeyID            []byte
	PublicKey        []byte
	PrivateKey       []byte
}

type E2ERecipient struct {
	ID               string
	Kind             string
	KeyEnvelopeSuite KeyEnvelopeSuite
	KeyID            []byte
	PublicKey        []byte
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
	return GenerateE2EIdentityForSuite(defaultE2EKeyEnvelopeSuite())
}

// GenerateE2EIdentityForSuite creates an identity for an explicit WebTTY key
// envelope suite. Standard builds support both suites; FIPS builds accept only
// the P-256 suite.
func GenerateE2EIdentityForSuite(suite KeyEnvelopeSuite) (*E2EIdentity, error) {
	if err := requireFIPSWebTTYRuntime(); err != nil {
		return nil, err
	}
	if err := validateProfileKeyEnvelopeSuite(suite); err != nil {
		return nil, err
	}
	curve, _, _, err := e2eCurveParameters(suite)
	if err != nil {
		return nil, err
	}
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate E2E key: %w", err)
	}
	publicKey := privateKey.PublicKey().Bytes()
	return &E2EIdentity{
		KeyEnvelopeSuite: suite,
		KeyID:            E2EKeyID(publicKey),
		PublicKey:        cloneBytes(publicKey),
		PrivateKey:       cloneBytes(privateKey.Bytes()),
	}, nil
}

func E2EKeyID(publicKey []byte) []byte {
	digest := sha256.Sum256(append([]byte(e2eKeyIDDomain), publicKey...))
	return cloneBytes(digest[:E2EPayloadKeyIDSize])
}

func NewE2EClientPayloadCrypto(cfg E2EPayloadCryptoConfig) (*PayloadCrypto, error) {
	if err := requireFIPSWebTTYRuntime(); err != nil {
		return nil, err
	}
	cipher, err := newE2EClientPayloadCipher(cfg)
	if err != nil {
		return nil, err
	}
	return cipher.payloadCrypto(), nil
}

func NewE2EServerPayloadCrypto(sessionKeyGrant *SessionKeyGrant, identity E2EIdentity) (*PayloadCrypto, error) {
	if err := requireFIPSWebTTYRuntime(); err != nil {
		return nil, err
	}
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
	keyEnvelopeSuite := cfg.KeyEnvelopeSuite
	if keyEnvelopeSuite == 0 {
		var err error
		keyEnvelopeSuite, err = inferE2ERecipientSuite(cfg.Recipients)
		if err != nil {
			return nil, err
		}
		if keyEnvelopeSuite == 0 {
			keyEnvelopeSuite = defaultE2EKeyEnvelopeSuite()
		}
	}
	payloadSuite := cfg.PayloadSuite
	if payloadSuite == 0 {
		var err error
		payloadSuite, err = e2ePayloadSuiteForKeyEnvelopeSuite(keyEnvelopeSuite)
		if err != nil {
			return nil, err
		}
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
	recipientKeyID, privateKey, err := e2eIdentityPrivateKey(identity, sessionKeyGrant.KeyEnvelopeSuite)
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
		plainLen := uint32(len(payload))
		metadata := &PayloadCryptoMetadata{
			PayloadSuite: c.payloadSuite,
			PayloadKeyID: cloneBytes(c.payloadKeyID),
			AADContext:   cloneBytes(c.keyContext),
		}
		nonce, err := newE2EPayloadNonce(c.payloadSuite, c.random)
		if err != nil {
			return nil, err
		}
		metadata.Nonce = nonce
		aad := e2ePayloadAAD(stream, metadata.PayloadSuite, metadata.PayloadKeyID, metadata.AADContext, metadata.Nonce, plainLen)
		ciphertext, err := sealE2EPayload(aead, c.payloadSuite, metadata.Nonce, payload, aad)
		if err != nil {
			return nil, err
		}
		return &EncryptedPayload{
			Ciphertext:      ciphertext,
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
		plaintext, err := openE2EPayload(aead, c.payloadSuite, payload.PayloadCrypto.Nonce, payload.Ciphertext, aad)
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
	expectedNonceSize, err := e2ePayloadNonceSize(c.payloadSuite)
	if err != nil {
		return err
	}
	if len(envelope.Nonce) != expectedNonceSize {
		return fmt.Errorf("E2E AES-GCM nonce must be %d bytes", expectedNonceSize)
	}
	return nil
}

func (c *e2ePayloadCipher) payloadAEAD() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.payloadKey)
	if err != nil {
		return nil, fmt.Errorf("create E2E AES cipher: %w", err)
	}
	switch c.payloadSuite {
	case PayloadCipherSuiteAES256GCM:
		return cipher.NewGCM(block)
	case PayloadCipherSuiteAES256GCMRandomNonce:
		return cipher.NewGCMWithRandomNonce(block)
	default:
		return nil, fmt.Errorf("unsupported E2E payload suite %d", c.payloadSuite)
	}
}

func validateE2ESuites(payloadSuite PayloadCipherSuite, keyEnvelopeSuite KeyEnvelopeSuite) error {
	if err := validateProfileKeyEnvelopeSuite(keyEnvelopeSuite); err != nil {
		return err
	}
	expectedPayloadSuite, err := e2ePayloadSuiteForKeyEnvelopeSuite(keyEnvelopeSuite)
	if err != nil {
		return err
	}
	if payloadSuite != expectedPayloadSuite {
		return fmt.Errorf("E2E payload suite %d does not match key envelope suite %d", payloadSuite, keyEnvelopeSuite)
	}
	return nil
}

func wrapE2EPayloadKey(payloadKey []byte, payloadSuite PayloadCipherSuite, payloadKeyID []byte, keyContext []byte, suite KeyEnvelopeSuite, recipient E2ERecipient) (KeyEnvelope, error) {
	curve, publicKeySize, _, err := e2eCurveParameters(suite)
	if err != nil {
		return KeyEnvelope{}, err
	}
	recipientSuite := recipient.KeyEnvelopeSuite
	if recipientSuite == 0 {
		recipientSuite, err = inferE2EKeyEnvelopeSuite(recipient.PublicKey)
		if err != nil {
			return KeyEnvelope{}, err
		}
	}
	if recipientSuite != suite {
		return KeyEnvelope{}, fmt.Errorf("E2E recipient suite %d does not match key envelope suite %d", recipientSuite, suite)
	}
	if len(recipient.PublicKey) != publicKeySize {
		return KeyEnvelope{}, fmt.Errorf("E2E recipient public key must be %d bytes", publicKeySize)
	}
	keyID := cloneBytes(recipient.KeyID)
	if len(keyID) == 0 {
		keyID = E2EKeyID(recipient.PublicKey)
	}
	if len(keyID) != E2EPayloadKeyIDSize {
		return KeyEnvelope{}, fmt.Errorf("E2E recipient key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	publicKey, err := curve.NewPublicKey(recipient.PublicKey)
	if err != nil {
		return KeyEnvelope{}, fmt.Errorf("parse E2E recipient public key: %w", err)
	}
	enc, wrappedKey, err := sealE2EKeyEnvelope(publicKey, suite, e2eHPKEInfo(payloadSuite, payloadKeyID, keyContext, suite), e2eHPKEAAD(keyID, payloadSuite, payloadKeyID, keyContext, suite), payloadKey)
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
	payloadKey, err := openE2EKeyEnvelope(privateKey, suite, envelope.EncapsulatedKey, envelope.WrappedKey, e2eHPKEInfo(payloadSuite, payloadKeyID, keyContext, suite), e2eHPKEAAD(envelope.RecipientKeyID, payloadSuite, payloadKeyID, keyContext, suite))
	if err != nil {
		return nil, fmt.Errorf("unwrap E2E payload key: %w", err)
	}
	if len(payloadKey) != E2EPayloadKeySize {
		return nil, fmt.Errorf("E2E unwrapped payload key must be %d bytes", E2EPayloadKeySize)
	}
	return payloadKey, nil
}

func e2eIdentityPrivateKey(identity E2EIdentity, suite KeyEnvelopeSuite) ([]byte, *ecdh.PrivateKey, error) {
	identitySuite := identity.KeyEnvelopeSuite
	if identitySuite == 0 {
		identitySuite = defaultE2EKeyEnvelopeSuite()
	}
	if identitySuite != suite {
		return nil, nil, fmt.Errorf("E2E identity suite %d does not match key envelope suite %d", identitySuite, suite)
	}
	curve, publicKeySize, privateKeySize, err := e2eCurveParameters(suite)
	if err != nil {
		return nil, nil, err
	}
	if len(identity.PrivateKey) != privateKeySize {
		return nil, nil, fmt.Errorf("E2E identity private key must be %d bytes", privateKeySize)
	}
	privateKey, err := curve.NewPrivateKey(identity.PrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("parse E2E identity private key: %w", err)
	}
	keyID := cloneBytes(identity.KeyID)
	if len(keyID) == 0 {
		publicKey := cloneBytes(identity.PublicKey)
		if len(publicKey) == 0 {
			publicKey = privateKey.PublicKey().Bytes()
		}
		if len(publicKey) != publicKeySize {
			return nil, nil, fmt.Errorf("E2E identity public key must be %d bytes", publicKeySize)
		}
		keyID = E2EKeyID(publicKey)
	}
	if len(keyID) != E2EPayloadKeyIDSize {
		return nil, nil, fmt.Errorf("E2E identity key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	return keyID, privateKey, nil
}

func e2eCurveParameters(suite KeyEnvelopeSuite) (ecdh.Curve, int, int, error) {
	if err := validateProfileKeyEnvelopeSuite(suite); err != nil {
		return nil, 0, 0, err
	}
	switch suite {
	case KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM:
		return ecdh.X25519(), E2EX25519PublicKeySize, E2EX25519PrivateKeySize, nil
	case KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce:
		return ecdh.P256(), E2EP256PublicKeySize, E2EP256PrivateKeySize, nil
	default:
		return nil, 0, 0, fmt.Errorf("unsupported E2E key envelope suite %d", suite)
	}
}

func e2ePayloadSuiteForKeyEnvelopeSuite(suite KeyEnvelopeSuite) (PayloadCipherSuite, error) {
	if err := validateProfileKeyEnvelopeSuite(suite); err != nil {
		return 0, err
	}
	switch suite {
	case KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM:
		return PayloadCipherSuiteAES256GCM, nil
	case KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce:
		return PayloadCipherSuiteAES256GCMRandomNonce, nil
	default:
		return 0, fmt.Errorf("unsupported E2E key envelope suite %d", suite)
	}
}

func inferE2ERecipientSuite(recipients []E2ERecipient) (KeyEnvelopeSuite, error) {
	var selected KeyEnvelopeSuite
	for _, recipient := range recipients {
		suite := recipient.KeyEnvelopeSuite
		var err error
		if suite == 0 {
			suite, err = inferE2EKeyEnvelopeSuite(recipient.PublicKey)
			if err != nil {
				return 0, err
			}
		}
		if err := validateProfileKeyEnvelopeSuite(suite); err != nil {
			return 0, err
		}
		if selected == 0 {
			selected = suite
			continue
		}
		if selected != suite {
			return 0, fmt.Errorf("E2E recipients use multiple key envelope suites")
		}
	}
	return selected, nil
}

func inferE2EKeyEnvelopeSuite(publicKey []byte) (KeyEnvelopeSuite, error) {
	var suite KeyEnvelopeSuite
	switch len(publicKey) {
	case E2EX25519PublicKeySize:
		suite = KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM
	case E2EP256PublicKeySize:
		suite = KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce
	default:
		return 0, fmt.Errorf("unsupported E2E public key length %d", len(publicKey))
	}
	if err := validateProfileKeyEnvelopeSuite(suite); err != nil {
		return 0, err
	}
	return suite, nil
}

func e2ePayloadNonceSize(suite PayloadCipherSuite) (int, error) {
	switch suite {
	case PayloadCipherSuiteAES256GCM:
		return e2eAESGCMNonceSize, nil
	case PayloadCipherSuiteAES256GCMRandomNonce:
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported E2E payload suite %d", suite)
	}
}

func newE2EPayloadNonce(suite PayloadCipherSuite, random io.Reader) ([]byte, error) {
	switch suite {
	case PayloadCipherSuiteAES256GCM:
		nonce := make([]byte, e2eAESGCMNonceSize)
		if _, err := io.ReadFull(random, nonce); err != nil {
			return nil, fmt.Errorf("generate E2E nonce: %w", err)
		}
		return nonce, nil
	case PayloadCipherSuiteAES256GCMRandomNonce:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported E2E payload suite %d", suite)
	}
}

func sealE2EPayload(aead cipher.AEAD, suite PayloadCipherSuite, nonce, plaintext, aad []byte) ([]byte, error) {
	switch suite {
	case PayloadCipherSuiteAES256GCM:
		return aead.Seal(nil, nonce, plaintext, aad), nil
	case PayloadCipherSuiteAES256GCMRandomNonce:
		return aead.Seal(nil, nil, plaintext, aad), nil
	default:
		return nil, fmt.Errorf("unsupported E2E payload suite %d", suite)
	}
}

func openE2EPayload(aead cipher.AEAD, suite PayloadCipherSuite, nonce, ciphertext, aad []byte) ([]byte, error) {
	switch suite {
	case PayloadCipherSuiteAES256GCM:
		return aead.Open(nil, nonce, ciphertext, aad)
	case PayloadCipherSuiteAES256GCMRandomNonce:
		if len(nonce) != 0 {
			return nil, fmt.Errorf("FIPS WebTTY AES-GCM nonce must be carried inside the ciphertext")
		}
		return aead.Open(nil, nil, ciphertext, aad)
	default:
		return nil, fmt.Errorf("unsupported E2E payload suite %d", suite)
	}
}

func sealE2EKeyEnvelope(publicKey *ecdh.PublicKey, suite KeyEnvelopeSuite, info, aad, payloadKey []byte) ([]byte, []byte, error) {
	switch suite {
	case KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM:
		hpkePublicKey, err := hpke.NewDHKEMPublicKey(publicKey)
		if err != nil {
			return nil, nil, fmt.Errorf("create E2E HPKE public key: %w", err)
		}
		enc, sender, err := hpke.NewSender(hpkePublicKey, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
		if err != nil {
			return nil, nil, fmt.Errorf("create E2E HPKE sender: %w", err)
		}
		wrappedKey, err := sender.Seal(aad, payloadKey)
		if err != nil {
			return nil, nil, fmt.Errorf("wrap E2E payload key: %w", err)
		}
		return enc, wrappedKey, nil
	case KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce:
		ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, fmt.Errorf("generate E2E P-256 ephemeral key: %w", err)
		}
		sharedSecret, err := ephemeral.ECDH(publicKey)
		if err != nil {
			return nil, nil, fmt.Errorf("derive E2E P-256 shared secret: %w", err)
		}
		wrappingKey, err := hkdf.Key(sha256.New, sharedSecret, nil, string(info), E2EPayloadKeySize)
		if err != nil {
			return nil, nil, fmt.Errorf("derive E2E wrapping key: %w", err)
		}
		block, err := aes.NewCipher(wrappingKey)
		if err != nil {
			return nil, nil, fmt.Errorf("create E2E wrapping cipher: %w", err)
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, nil, fmt.Errorf("create E2E wrapping AEAD: %w", err)
		}
		return ephemeral.PublicKey().Bytes(), aead.Seal(nil, nil, payloadKey, aad), nil
	default:
		return nil, nil, fmt.Errorf("unsupported E2E key envelope suite %d", suite)
	}
}

func openE2EKeyEnvelope(privateKey *ecdh.PrivateKey, suite KeyEnvelopeSuite, encapsulatedKey, wrappedKey, info, aad []byte) ([]byte, error) {
	switch suite {
	case KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM:
		hpkePrivateKey, err := hpke.NewDHKEMPrivateKey(privateKey)
		if err != nil {
			return nil, fmt.Errorf("create E2E HPKE private key: %w", err)
		}
		recipient, err := hpke.NewRecipient(encapsulatedKey, hpkePrivateKey, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
		if err != nil {
			return nil, fmt.Errorf("create E2E HPKE recipient: %w", err)
		}
		return recipient.Open(aad, wrappedKey)
	case KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce:
		ephemeral, err := ecdh.P256().NewPublicKey(encapsulatedKey)
		if err != nil {
			return nil, fmt.Errorf("parse E2E P-256 ephemeral key: %w", err)
		}
		sharedSecret, err := privateKey.ECDH(ephemeral)
		if err != nil {
			return nil, fmt.Errorf("derive E2E P-256 shared secret: %w", err)
		}
		wrappingKey, err := hkdf.Key(sha256.New, sharedSecret, nil, string(info), E2EPayloadKeySize)
		if err != nil {
			return nil, fmt.Errorf("derive E2E wrapping key: %w", err)
		}
		block, err := aes.NewCipher(wrappingKey)
		if err != nil {
			return nil, fmt.Errorf("create E2E wrapping cipher: %w", err)
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, fmt.Errorf("create E2E wrapping AEAD: %w", err)
		}
		return aead.Open(nil, nil, wrappedKey, aad)
	default:
		return nil, fmt.Errorf("unsupported E2E key envelope suite %d", suite)
	}
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

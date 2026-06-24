// See LICENSE file in the project root for license information.

package webtty

import (
	"context"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type PayloadEncryptFunc func(context.Context, []byte) (*EncryptedPayload, error)
type PayloadDecryptFunc func(context.Context, *EncryptedPayload) ([]byte, error)
type PayloadCryptoResolver func(context.Context, *SessionKeyGrant) (*PayloadCrypto, error)

type OpenCapability pb.OpenCapability

const (
	OpenCapabilityEncryptedPayload OpenCapability = OpenCapability(pb.OpenCapability_OPEN_CAPABILITY_ENCRYPTED_PAYLOAD)
	OpenCapabilitySessionKeyGrant  OpenCapability = OpenCapability(pb.OpenCapability_OPEN_CAPABILITY_SESSION_CRYPTO)
)

type PayloadCipherSuite pb.PayloadCipherSuite

const (
	PayloadCipherSuiteAES256GCM        PayloadCipherSuite = PayloadCipherSuite(pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_AES_256_GCM)
	PayloadCipherSuiteChaCha20Poly1305 PayloadCipherSuite = PayloadCipherSuite(pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_CHACHA20_POLY1305)
)

type KeyEnvelopeSuite pb.KeyEnvelopeSuite

const (
	KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM        KeyEnvelopeSuite = KeyEnvelopeSuite(pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_AES_256_GCM)
	KeyEnvelopeSuiteHPKEX25519HKDFSHA256ChaCha20Poly1305 KeyEnvelopeSuite = KeyEnvelopeSuite(pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_CHACHA20_POLY1305)
)

type KeyEnvelope struct {
	RecipientKeyID  []byte
	EncapsulatedKey []byte
	WrappedKey      []byte
}

type SessionKeyGrant struct {
	PayloadSuite     PayloadCipherSuite
	PayloadKeyID     []byte
	KeyEnvelopes     []KeyEnvelope
	KeyContext       []byte
	KeyEnvelopeSuite KeyEnvelopeSuite
}

type PayloadCryptoMetadata struct {
	PayloadSuite PayloadCipherSuite
	PayloadKeyID []byte
	Nonce        []byte
	AADContext   []byte
}

type EncryptedPayload struct {
	Ciphertext      []byte
	PlaintextLength uint32
	PayloadCrypto   *PayloadCryptoMetadata
}

type PayloadCrypto struct {
	Capabilities    []OpenCapability
	SessionKeyGrant *SessionKeyGrant
	EncryptStdin    PayloadEncryptFunc
	DecryptStdin    PayloadDecryptFunc
	EncryptStdout   PayloadEncryptFunc
	DecryptStdout   PayloadDecryptFunc
	EncryptStderr   PayloadEncryptFunc
	DecryptStderr   PayloadDecryptFunc
}

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return append([]byte(nil), value...)
}

func keyEnvelopeToProto(src KeyEnvelope) *pb.KeyEnvelope {
	return &pb.KeyEnvelope{
		RecipientKeyId:  cloneBytes(src.RecipientKeyID),
		EncapsulatedKey: cloneBytes(src.EncapsulatedKey),
		WrappedKey:      cloneBytes(src.WrappedKey),
	}
}

func keyEnvelopeFromProto(src *pb.KeyEnvelope) KeyEnvelope {
	if src == nil {
		return KeyEnvelope{}
	}
	return KeyEnvelope{
		RecipientKeyID:  cloneBytes(src.RecipientKeyId),
		EncapsulatedKey: cloneBytes(src.EncapsulatedKey),
		WrappedKey:      cloneBytes(src.WrappedKey),
	}
}

func sessionKeyGrantToProto(src *SessionKeyGrant) *pb.SessionKeyGrant {
	if src == nil {
		return nil
	}
	keyEnvelopes := make([]*pb.KeyEnvelope, 0, len(src.KeyEnvelopes))
	for _, envelope := range src.KeyEnvelopes {
		keyEnvelopes = append(keyEnvelopes, keyEnvelopeToProto(envelope))
	}
	return &pb.SessionKeyGrant{
		PayloadSuite:     pb.PayloadCipherSuite(src.PayloadSuite),
		PayloadKeyId:     cloneBytes(src.PayloadKeyID),
		KeyEnvelopes:     keyEnvelopes,
		KeyContext:       cloneBytes(src.KeyContext),
		KeyEnvelopeSuite: pb.KeyEnvelopeSuite(src.KeyEnvelopeSuite),
	}
}

func sessionKeyGrantFromProto(src *pb.SessionKeyGrant) *SessionKeyGrant {
	if src == nil {
		return nil
	}
	keyEnvelopes := make([]KeyEnvelope, 0, len(src.KeyEnvelopes))
	for _, envelope := range src.KeyEnvelopes {
		keyEnvelopes = append(keyEnvelopes, keyEnvelopeFromProto(envelope))
	}
	return &SessionKeyGrant{
		PayloadSuite:     PayloadCipherSuite(src.PayloadSuite),
		PayloadKeyID:     cloneBytes(src.PayloadKeyId),
		KeyEnvelopes:     keyEnvelopes,
		KeyContext:       cloneBytes(src.KeyContext),
		KeyEnvelopeSuite: KeyEnvelopeSuite(src.KeyEnvelopeSuite),
	}
}

func payloadCryptoToProto(src *PayloadCryptoMetadata) *pb.PayloadCrypto {
	if src == nil {
		return nil
	}
	return &pb.PayloadCrypto{
		PayloadSuite: pb.PayloadCipherSuite(src.PayloadSuite),
		PayloadKeyId: cloneBytes(src.PayloadKeyID),
		Nonce:        cloneBytes(src.Nonce),
		AadContext:   cloneBytes(src.AADContext),
	}
}

func payloadCryptoFromProto(src *pb.PayloadCrypto) *PayloadCryptoMetadata {
	if src == nil {
		return nil
	}
	return &PayloadCryptoMetadata{
		PayloadSuite: PayloadCipherSuite(src.PayloadSuite),
		PayloadKeyID: cloneBytes(src.PayloadKeyId),
		Nonce:        cloneBytes(src.Nonce),
		AADContext:   cloneBytes(src.AadContext),
	}
}

func encryptedPayloadToProto(src *EncryptedPayload) *pb.EncryptedPayload {
	if src == nil {
		return nil
	}
	return &pb.EncryptedPayload{
		Ciphertext:      cloneBytes(src.Ciphertext),
		PlaintextLength: src.PlaintextLength,
		PayloadCrypto:   payloadCryptoToProto(src.PayloadCrypto),
	}
}

func encryptedPayloadFromProto(src *pb.EncryptedPayload) *EncryptedPayload {
	if src == nil {
		return nil
	}
	return &EncryptedPayload{
		Ciphertext:      cloneBytes(src.Ciphertext),
		PlaintextLength: src.PlaintextLength,
		PayloadCrypto:   payloadCryptoFromProto(src.PayloadCrypto),
	}
}

func payloadCryptoCapabilities(crypto *PayloadCrypto) []pb.OpenCapability {
	if crypto == nil {
		return nil
	}
	shouldAdvertiseEncryptedPayload := crypto.SessionKeyGrant != nil ||
		crypto.EncryptStdin != nil ||
		crypto.DecryptStdin != nil ||
		crypto.EncryptStdout != nil ||
		crypto.DecryptStdout != nil ||
		crypto.EncryptStderr != nil ||
		crypto.DecryptStderr != nil
	capabilities := make([]OpenCapability, 0, len(crypto.Capabilities)+2)
	if shouldAdvertiseEncryptedPayload {
		capabilities = append(capabilities, OpenCapabilityEncryptedPayload)
	}
	if crypto.SessionKeyGrant != nil {
		capabilities = append(capabilities, OpenCapabilitySessionKeyGrant)
	}
	capabilities = append(capabilities, crypto.Capabilities...)
	seen := make(map[OpenCapability]struct{}, len(capabilities))
	out := make([]pb.OpenCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability == 0 {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, pb.OpenCapability(capability))
	}
	return out
}

func payloadCryptoSessionKeyGrant(crypto *PayloadCrypto) *pb.SessionKeyGrant {
	if crypto == nil {
		return nil
	}
	return sessionKeyGrantToProto(crypto.SessionKeyGrant)
}

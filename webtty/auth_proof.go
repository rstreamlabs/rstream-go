// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

const (
	WebTTYClientAuthTranscriptDomain = "rstream-webtty-client-auth-v1"
	WebTTYServerAuthTranscriptDomain = "rstream-webtty-server-auth-v1"
	WebTTYSigningKeyIDSize           = 32
	webTTYSigningKeyIDDomain         = "rstream-webtty-signing-key-id-v1"
)

type ProtocolVersion pb.ProtocolVersion

const (
	ProtocolVersionWebTTY1 ProtocolVersion = ProtocolVersion(pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1)
)

type SignatureSuite pb.SignatureSuite

const (
	SignatureSuiteECDSAP256SHA256 SignatureSuite = SignatureSuite(pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256)
)

type AuthRequirement pb.AuthRequirement

const (
	AuthRequirementNone        AuthRequirement = AuthRequirement(pb.AuthRequirement_AUTH_REQUIREMENT_NONE)
	AuthRequirementClientProof AuthRequirement = AuthRequirement(pb.AuthRequirement_AUTH_REQUIREMENT_CLIENT_PROOF)
)

type ClientProofTranscript struct {
	ProtocolVersion       ProtocolVersion
	Transport             string
	WorkspaceID           string
	ProjectID             string
	ServerID              string
	SessionID             string
	ServerSigningKeyID    []byte
	ServerEncryptionKeyID []byte
	ServerNonce           []byte
	AuthRequirement       AuthRequirement
	PayloadSuite          PayloadCipherSuite
	KeyEnvelopeSuite      KeyEnvelopeSuite
	SessionKeyGrantHash   []byte
	CommandConfigHash     []byte
	AttachGrantHash       []byte
	RequestedRole         string
	ClientPrincipalID     string
	ClientSigningKeyID    []byte
	ClientCredentialHash  []byte
	IssuedAt              string
	ExpiresAt             string
}

func (t ClientProofTranscript) CanonicalBytes() []byte {
	var out []byte
	out = appendLengthPrefixed(out, []byte(WebTTYClientAuthTranscriptDomain))
	out = appendUint32(out, uint32(t.ProtocolVersion))
	out = appendLengthPrefixedString(out, t.Transport)
	out = appendLengthPrefixedString(out, t.WorkspaceID)
	out = appendLengthPrefixedString(out, t.ProjectID)
	out = appendLengthPrefixedString(out, t.ServerID)
	out = appendLengthPrefixedString(out, t.SessionID)
	out = appendLengthPrefixed(out, t.ServerSigningKeyID)
	out = appendLengthPrefixed(out, t.ServerEncryptionKeyID)
	out = appendLengthPrefixed(out, t.ServerNonce)
	out = appendUint32(out, uint32(t.AuthRequirement))
	out = appendUint32(out, uint32(t.PayloadSuite))
	out = appendUint32(out, uint32(t.KeyEnvelopeSuite))
	out = appendLengthPrefixed(out, t.SessionKeyGrantHash)
	out = appendLengthPrefixed(out, t.CommandConfigHash)
	out = appendLengthPrefixed(out, t.AttachGrantHash)
	out = appendLengthPrefixedString(out, t.RequestedRole)
	out = appendLengthPrefixedString(out, t.ClientPrincipalID)
	out = appendLengthPrefixed(out, t.ClientSigningKeyID)
	out = appendLengthPrefixed(out, t.ClientCredentialHash)
	out = appendLengthPrefixedString(out, t.IssuedAt)
	out = appendLengthPrefixedString(out, t.ExpiresAt)
	return out
}

func (t ClientProofTranscript) Hash() []byte {
	digest := sha256.Sum256(t.CanonicalBytes())
	return cloneBytes(digest[:])
}

type ServerProofTranscript struct {
	ProtocolVersion       ProtocolVersion
	Transport             string
	WorkspaceID           string
	ProjectID             string
	ServerID              string
	SessionID             string
	ServerSigningKeyID    []byte
	ServerEncryptionKeyID []byte
	ServerNonce           []byte
	AuthRequirement       AuthRequirement
	PayloadSuites         []PayloadCipherSuite
	KeyEnvelopeSuites     []KeyEnvelopeSuite
	SignatureSuites       []SignatureSuite
}

func (t ServerProofTranscript) CanonicalBytes() []byte {
	var out []byte
	out = appendLengthPrefixed(out, []byte(WebTTYServerAuthTranscriptDomain))
	out = appendUint32(out, uint32(t.ProtocolVersion))
	out = appendLengthPrefixedString(out, t.Transport)
	out = appendLengthPrefixedString(out, t.WorkspaceID)
	out = appendLengthPrefixedString(out, t.ProjectID)
	out = appendLengthPrefixedString(out, t.ServerID)
	out = appendLengthPrefixedString(out, t.SessionID)
	out = appendLengthPrefixed(out, t.ServerSigningKeyID)
	out = appendLengthPrefixed(out, t.ServerEncryptionKeyID)
	out = appendLengthPrefixed(out, t.ServerNonce)
	out = appendUint32(out, uint32(t.AuthRequirement))
	out = appendPayloadSuites(out, t.PayloadSuites)
	out = appendKeyEnvelopeSuites(out, t.KeyEnvelopeSuites)
	out = appendSignatureSuites(out, t.SignatureSuites)
	return out
}

func (t ServerProofTranscript) Hash() []byte {
	digest := sha256.Sum256(t.CanonicalBytes())
	return cloneBytes(digest[:])
}

func GenerateWebTTYSigningKey() (*ecdsa.PrivateKey, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate WebTTY signing key: %w", err)
	}
	return privateKey, nil
}

func MarshalWebTTYSigningPublicKey(publicKey *ecdsa.PublicKey) ([]byte, error) {
	if err := validateWebTTYSigningPublicKey(publicKey); err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal WebTTY signing public key: %w", err)
	}
	return der, nil
}

func MarshalWebTTYSigningPrivateKey(privateKey *ecdsa.PrivateKey) ([]byte, error) {
	if err := validateWebTTYSigningPrivateKey(privateKey); err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal WebTTY signing private key: %w", err)
	}
	return der, nil
}

func ParseWebTTYSigningPublicKey(der []byte) (*ecdsa.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse WebTTY signing public key: %w", err)
	}
	publicKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("WebTTY signing public key is not ECDSA")
	}
	if err := validateWebTTYSigningPublicKey(publicKey); err != nil {
		return nil, err
	}
	return publicKey, nil
}

func ParseWebTTYSigningPrivateKey(der []byte) (*ecdsa.PrivateKey, error) {
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse WebTTY signing private key: %w", err)
	}
	privateKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("WebTTY signing private key is not ECDSA")
	}
	if err := validateWebTTYSigningPrivateKey(privateKey); err != nil {
		return nil, err
	}
	return privateKey, nil
}

func WebTTYSigningKeyID(publicKeyDER []byte) []byte {
	digest := sha256.Sum256(append([]byte(webTTYSigningKeyIDDomain), publicKeyDER...))
	return cloneBytes(digest[:])
}

func SignWebTTYClientProofTranscript(random io.Reader, privateKey *ecdsa.PrivateKey, transcript ClientProofTranscript) ([]byte, []byte, error) {
	return signWebTTYProofTranscript(random, privateKey, transcript.Hash())
}

func VerifyWebTTYClientProofTranscript(publicKeyDER []byte, transcript ClientProofTranscript, signature []byte) error {
	return verifyWebTTYProofTranscript(publicKeyDER, transcript.Hash(), signature)
}

func SignWebTTYServerProofTranscript(random io.Reader, privateKey *ecdsa.PrivateKey, transcript ServerProofTranscript) ([]byte, []byte, error) {
	return signWebTTYProofTranscript(random, privateKey, transcript.Hash())
}

func VerifyWebTTYServerProofTranscript(publicKeyDER []byte, transcript ServerProofTranscript, signature []byte) error {
	return verifyWebTTYProofTranscript(publicKeyDER, transcript.Hash(), signature)
}

func HashWebTTYConfig(config *pb.Config) ([]byte, error) {
	return hashWebTTYProtoMessage(config, "WebTTY config")
}

func HashWebTTYSessionKeyGrant(grant *pb.SessionKeyGrant) ([]byte, error) {
	return hashWebTTYProtoMessage(grant, "WebTTY session key grant")
}

func HashWebTTYAttachGrant(grant []byte) []byte {
	digest := sha256.Sum256(grant)
	return cloneBytes(digest[:])
}

func HashWebTTYClientCredential(credential []byte) []byte {
	digest := sha256.Sum256(credential)
	return cloneBytes(digest[:])
}

func signWebTTYProofTranscript(random io.Reader, privateKey *ecdsa.PrivateKey, transcriptHash []byte) ([]byte, []byte, error) {
	if err := validateWebTTYSigningPrivateKey(privateKey); err != nil {
		return nil, nil, err
	}
	if len(transcriptHash) != sha256.Size {
		return nil, nil, fmt.Errorf("WebTTY proof transcript hash must be %d bytes", sha256.Size)
	}
	if random == nil {
		random = rand.Reader
	}
	signature, err := ecdsa.SignASN1(random, privateKey, transcriptHash)
	if err != nil {
		return nil, nil, fmt.Errorf("sign WebTTY proof transcript: %w", err)
	}
	return cloneBytes(transcriptHash), signature, nil
}

func verifyWebTTYProofTranscript(publicKeyDER []byte, transcriptHash []byte, signature []byte) error {
	if len(transcriptHash) != sha256.Size {
		return fmt.Errorf("WebTTY proof transcript hash must be %d bytes", sha256.Size)
	}
	publicKey, err := ParseWebTTYSigningPublicKey(publicKeyDER)
	if err != nil {
		return err
	}
	if !ecdsa.VerifyASN1(publicKey, transcriptHash, signature) {
		return errors.New("invalid WebTTY proof signature")
	}
	return nil
}

func hashWebTTYProtoMessage(message proto.Message, label string) ([]byte, error) {
	if message == nil {
		digest := sha256.Sum256(nil)
		return cloneBytes(digest[:]), nil
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", label, err)
	}
	digest := sha256.Sum256(data)
	return cloneBytes(digest[:]), nil
}

func validateWebTTYSigningPrivateKey(privateKey *ecdsa.PrivateKey) error {
	if privateKey == nil {
		return errors.New("missing WebTTY signing private key")
	}
	if privateKey.Curve != elliptic.P256() {
		return errors.New("WebTTY signing private key is not P-256 ECDSA")
	}
	return validateWebTTYSigningPublicKey(&privateKey.PublicKey)
}

func validateWebTTYSigningPublicKey(publicKey *ecdsa.PublicKey) error {
	if publicKey == nil {
		return errors.New("missing WebTTY signing public key")
	}
	if publicKey.Curve != elliptic.P256() {
		return errors.New("WebTTY signing public key is not P-256 ECDSA")
	}
	if _, err := publicKey.Bytes(); err != nil {
		return errors.New("WebTTY signing public key is not on P-256")
	}
	return nil
}

func appendLengthPrefixedString(dst []byte, value string) []byte {
	return appendLengthPrefixed(dst, []byte(strings.TrimSpace(value)))
}

func appendPayloadSuites(dst []byte, values []PayloadCipherSuite) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint32(dst, uint32(value))
	}
	return dst
}

func appendKeyEnvelopeSuites(dst []byte, values []KeyEnvelopeSuite) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint32(dst, uint32(value))
	}
	return dst
}

func appendSignatureSuites(dst []byte, values []SignatureSuite) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint32(dst, uint32(value))
	}
	return dst
}

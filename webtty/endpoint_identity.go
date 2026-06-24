// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
)

const (
	WebTTYEndpointIdentityFileVersion = 1
	WebTTYEndpointIdentityCryptoSuite = "webtty-endpoint-x25519-ecdsa-p256-v1"
)

type WebTTYSigningIdentity struct {
	KeyID      []byte
	PublicKey  []byte
	PrivateKey []byte
}

type WebTTYEndpointIdentity struct {
	Encryption E2EIdentity
	Signing    WebTTYSigningIdentity
}

type WebTTYEndpointIdentityPublic struct {
	EncryptionKeyID     []byte
	EncryptionPublicKey []byte
	SigningKeyID        []byte
	SigningPublicKey    []byte
}

type WebTTYEndpointIdentityFile struct {
	Version              int       `json:"version"`
	CryptoSuite          string    `json:"crypto_suite"`
	EncryptionKeyID      string    `json:"encryption_key_id"`
	EncryptionPublicKey  string    `json:"encryption_public_key"`
	EncryptionPrivateKey string    `json:"encryption_private_key"`
	SigningKeyID         string    `json:"signing_key_id"`
	SigningPublicKey     string    `json:"signing_public_key"`
	SigningPrivateKey    string    `json:"signing_private_key"`
	CreatedAt            time.Time `json:"created_at"`
}

func GenerateWebTTYEndpointIdentity() (*WebTTYEndpointIdentity, error) {
	encryption, err := GenerateE2EIdentity()
	if err != nil {
		return nil, err
	}
	signingPrivateKey, err := GenerateWebTTYSigningKey()
	if err != nil {
		return nil, err
	}
	signing, err := webTTYSigningIdentityFromPrivateKey(signingPrivateKey)
	if err != nil {
		return nil, err
	}
	return &WebTTYEndpointIdentity{
		Encryption: *encryption,
		Signing:    *signing,
	}, nil
}

func (identity WebTTYEndpointIdentity) Public() WebTTYEndpointIdentityPublic {
	return WebTTYEndpointIdentityPublic{
		EncryptionKeyID:     cloneBytes(identity.Encryption.KeyID),
		EncryptionPublicKey: cloneBytes(identity.Encryption.PublicKey),
		SigningKeyID:        cloneBytes(identity.Signing.KeyID),
		SigningPublicKey:    cloneBytes(identity.Signing.PublicKey),
	}
}

func EncodeWebTTYEndpointIdentityJSON(identity WebTTYEndpointIdentity) ([]byte, error) {
	if err := validateWebTTYEndpointIdentity(identity); err != nil {
		return nil, err
	}
	doc := WebTTYEndpointIdentityFile{
		Version:              WebTTYEndpointIdentityFileVersion,
		CryptoSuite:          WebTTYEndpointIdentityCryptoSuite,
		EncryptionKeyID:      EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		EncryptionPublicKey:  EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		EncryptionPrivateKey: EncodeE2EKeyMaterial(identity.Encryption.PrivateKey),
		SigningKeyID:         EncodeE2EKeyMaterial(identity.Signing.KeyID),
		SigningPublicKey:     EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		SigningPrivateKey:    EncodeE2EKeyMaterial(identity.Signing.PrivateKey),
		CreatedAt:            time.Now().UTC().Truncate(time.Second),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode WebTTY endpoint identity: %w", err)
	}
	return append(data, '\n'), nil
}

func DecodeWebTTYEndpointIdentityJSON(data []byte) (*WebTTYEndpointIdentity, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc WebTTYEndpointIdentityFile
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode WebTTY endpoint identity: %w", err)
	}
	if err := ensureSingleJSONValue(decoder, "decode WebTTY endpoint identity"); err != nil {
		return nil, err
	}
	if doc.Version != WebTTYEndpointIdentityFileVersion {
		return nil, fmt.Errorf("unsupported WebTTY endpoint identity version %d", doc.Version)
	}
	if doc.CryptoSuite != WebTTYEndpointIdentityCryptoSuite {
		return nil, fmt.Errorf("unsupported WebTTY endpoint identity crypto suite %q", doc.CryptoSuite)
	}
	encryptionPrivateKey, err := DecodeE2EKeyMaterial(doc.EncryptionPrivateKey, E2EX25519PrivateKeySize, "WebTTY endpoint encryption private key")
	if err != nil {
		return nil, err
	}
	encryption, err := E2EIdentityFromPrivateKey(encryptionPrivateKey)
	if err != nil {
		return nil, err
	}
	if err := validateEncodedE2EPublicMaterial(*encryption, doc.EncryptionKeyID, doc.EncryptionPublicKey); err != nil {
		return nil, err
	}
	signingPrivateKey, err := DecodeE2EKeyMaterial(doc.SigningPrivateKey, 0, "WebTTY endpoint signing private key")
	if err != nil {
		return nil, err
	}
	signing, err := webTTYSigningIdentityFromPrivateKeyDER(signingPrivateKey)
	if err != nil {
		return nil, err
	}
	if err := validateEncodedWebTTYSigningPublicMaterial(*signing, doc.SigningKeyID, doc.SigningPublicKey); err != nil {
		return nil, err
	}
	return &WebTTYEndpointIdentity{
		Encryption: *encryption,
		Signing:    *signing,
	}, nil
}

func LoadWebTTYEndpointIdentityFile(path string) (*WebTTYEndpointIdentity, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultE2EIdentityPath()
		if err != nil {
			return nil, err
		}
	}
	if err := checkSensitiveFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	identity, err := DecodeWebTTYEndpointIdentityJSON(data)
	if err == nil {
		return identity, nil
	}
	return nil, fmt.Errorf("load WebTTY endpoint identity %s: %w", path, err)
}

func LoadOrCreateWebTTYEndpointIdentityFile(path string) (*WebTTYEndpointIdentity, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultE2EIdentityPath()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	unlockLocal := lockKnownServerKeysFile(path)
	defer unlockLocal()
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.Unlock()
	identity, err := loadWebTTYEndpointIdentityFileUnlocked(path)
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	identity, err = GenerateWebTTYEndpointIdentity()
	if err != nil {
		return nil, err
	}
	if err := writeWebTTYEndpointIdentityFileUnlocked(path, *identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func loadWebTTYEndpointIdentityFileUnlocked(path string) (*WebTTYEndpointIdentity, error) {
	if err := checkSensitiveFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	identity, err := DecodeWebTTYEndpointIdentityJSON(data)
	if err == nil {
		return identity, nil
	}
	return nil, fmt.Errorf("load WebTTY endpoint identity %s: %w", path, err)
}

func writeWebTTYEndpointIdentityFileUnlocked(path string, identity WebTTYEndpointIdentity) error {
	data, err := EncodeWebTTYEndpointIdentityJSON(identity)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".webtty-endpoint-identity-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func validateWebTTYEndpointIdentity(identity WebTTYEndpointIdentity) error {
	if err := validateE2EIdentity(identity.Encryption); err != nil {
		return fmt.Errorf("invalid WebTTY endpoint encryption identity: %w", err)
	}
	if err := validateWebTTYSigningIdentity(identity.Signing); err != nil {
		return fmt.Errorf("invalid WebTTY endpoint signing identity: %w", err)
	}
	return nil
}

func webTTYSigningIdentityFromPrivateKey(privateKey *ecdsa.PrivateKey) (*WebTTYSigningIdentity, error) {
	privateDER, err := MarshalWebTTYSigningPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return webTTYSigningIdentityFromPrivateKeyDER(privateDER)
}

func webTTYSigningIdentityFromPrivateKeyDER(privateDER []byte) (*WebTTYSigningIdentity, error) {
	privateKey, err := ParseWebTTYSigningPrivateKey(privateDER)
	if err != nil {
		return nil, err
	}
	publicDER, err := MarshalWebTTYSigningPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, err
	}
	return &WebTTYSigningIdentity{
		KeyID:      WebTTYSigningKeyID(publicDER),
		PublicKey:  cloneBytes(publicDER),
		PrivateKey: cloneBytes(privateDER),
	}, nil
}

func validateWebTTYSigningIdentity(identity WebTTYSigningIdentity) error {
	privateKey, err := ParseWebTTYSigningPrivateKey(identity.PrivateKey)
	if err != nil {
		return err
	}
	publicDER, err := MarshalWebTTYSigningPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(identity.PublicKey, publicDER) {
		return fmt.Errorf("WebTTY signing public key does not match private key")
	}
	keyID := WebTTYSigningKeyID(publicDER)
	if !bytes.Equal(identity.KeyID, keyID) {
		return fmt.Errorf("WebTTY signing key id does not match public key")
	}
	return nil
}

func validateEncodedE2EPublicMaterial(identity E2EIdentity, encodedKeyID string, encodedPublicKey string) error {
	keyID, err := DecodeE2EKeyMaterial(encodedKeyID, E2EPayloadKeyIDSize, "WebTTY endpoint encryption key id")
	if err != nil {
		return err
	}
	publicKey, err := DecodeE2EKeyMaterial(encodedPublicKey, E2EX25519PublicKeySize, "WebTTY endpoint encryption public key")
	if err != nil {
		return err
	}
	if !bytes.Equal(identity.KeyID, keyID) {
		return fmt.Errorf("WebTTY endpoint encryption key id does not match private key")
	}
	if !bytes.Equal(identity.PublicKey, publicKey) {
		return fmt.Errorf("WebTTY endpoint encryption public key does not match private key")
	}
	return nil
}

func validateEncodedWebTTYSigningPublicMaterial(identity WebTTYSigningIdentity, encodedKeyID string, encodedPublicKey string) error {
	keyID, err := DecodeE2EKeyMaterial(encodedKeyID, WebTTYSigningKeyIDSize, "WebTTY endpoint signing key id")
	if err != nil {
		return err
	}
	publicKey, err := DecodeE2EKeyMaterial(encodedPublicKey, 0, "WebTTY endpoint signing public key")
	if err != nil {
		return err
	}
	if !bytes.Equal(identity.KeyID, keyID) {
		return fmt.Errorf("WebTTY endpoint signing key id does not match private key")
	}
	if !bytes.Equal(identity.PublicKey, publicKey) {
		return fmt.Errorf("WebTTY endpoint signing public key does not match private key")
	}
	return nil
}

func ReadWebTTYEndpointIdentityPublic(data []byte) (*WebTTYEndpointIdentityPublic, error) {
	identity, err := DecodeWebTTYEndpointIdentityJSON(data)
	if err != nil {
		return nil, err
	}
	public := identity.Public()
	return &public, nil
}

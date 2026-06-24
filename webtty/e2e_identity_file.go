// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
)

const (
	E2EIdentityFileVersion = 1
	E2EKeyFileCryptoSuite  = "webtty-e2e-x25519-hpke-aes-256-gcm-v1"
)

var knownServerKeysFileLocks sync.Map

type E2EIdentityFile struct {
	Version     int       `json:"version"`
	CryptoSuite string    `json:"crypto_suite"`
	KeyID       string    `json:"key_id"`
	PublicKey   string    `json:"public_key"`
	PrivateKey  string    `json:"private_key"`
	CreatedAt   time.Time `json:"created_at"`
}

type KnownServerKeysFile struct {
	Version      int                   `json:"version"`
	CryptoSuite  string                `json:"crypto_suite"`
	KnownServers []KnownServerKeyEntry `json:"known_servers"`
}

type KnownServerKeyEntry struct {
	Name             string `json:"name"`
	KeyID            string `json:"key_id"`
	PublicKey        string `json:"public_key"`
	SigningKeyID     string `json:"signing_key_id,omitempty"`
	SigningPublicKey string `json:"signing_public_key,omitempty"`
	ClientIdentity   string `json:"client_identity,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
}

func DefaultE2EIdentityPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rstream", "webtty", "identities", "default.identity.json"), nil
}

func DefaultKnownServerKeysPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rstream", "webtty", "known_servers.json"), nil
}

func EncodeE2EKeyMaterial(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func DecodeE2EKeyMaterial(value string, expectedSize int, field string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s is empty", field)
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", field, err)
	}
	if expectedSize > 0 && len(data) != expectedSize {
		return nil, fmt.Errorf("%s must decode to %d bytes", field, expectedSize)
	}
	if EncodeE2EKeyMaterial(data) != value {
		return nil, fmt.Errorf("%s must be canonical base64url without padding", field)
	}
	return data, nil
}

func E2EIdentityFromPrivateKey(privateKey []byte) (*E2EIdentity, error) {
	if len(privateKey) != E2EX25519PrivateKeySize {
		return nil, fmt.Errorf("E2E identity private key must be %d bytes", E2EX25519PrivateKeySize)
	}
	key, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse E2E identity private key: %w", err)
	}
	publicKey := key.PublicKey().Bytes()
	return &E2EIdentity{
		KeyID:      E2EKeyID(publicKey),
		PublicKey:  cloneBytes(publicKey),
		PrivateKey: cloneBytes(privateKey),
	}, nil
}

func ParseE2EIdentityPrivateKey(value string) (*E2EIdentity, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("E2E identity private key is empty")
	}
	if strings.HasPrefix(value, "{") {
		return DecodeE2EIdentityJSON([]byte(value))
	}
	privateKey, err := DecodeE2EKeyMaterial(value, E2EX25519PrivateKeySize, "E2E identity private key")
	if err != nil {
		return nil, err
	}
	return E2EIdentityFromPrivateKey(privateKey)
}

func E2ERecipientFromPublicKey(publicKey []byte) (E2ERecipient, error) {
	if len(publicKey) != E2EX25519PublicKeySize {
		return E2ERecipient{}, fmt.Errorf("E2E public key must be %d bytes", E2EX25519PublicKeySize)
	}
	return E2ERecipient{KeyID: E2EKeyID(publicKey), PublicKey: cloneBytes(publicKey)}, nil
}

func ParseKnownServerKey(value string) (E2ERecipient, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return E2ERecipient{}, fmt.Errorf("known WebTTY server key is empty")
	}
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return E2ERecipient{}, fmt.Errorf("known WebTTY server key must be public_key or key_id:public_key")
	}
	if len(parts) == 1 {
		publicKey, err := DecodeE2EKeyMaterial(parts[0], E2EX25519PublicKeySize, "known WebTTY server public key")
		if err != nil {
			return E2ERecipient{}, err
		}
		return E2ERecipientFromPublicKey(publicKey)
	}
	keyID, err := DecodeE2EKeyMaterial(parts[0], E2EPayloadKeyIDSize, "known WebTTY server key id")
	if err != nil {
		return E2ERecipient{}, err
	}
	publicKey, err := DecodeE2EKeyMaterial(parts[1], E2EX25519PublicKeySize, "known WebTTY server public key")
	if err != nil {
		return E2ERecipient{}, err
	}
	expectedKeyID := E2EKeyID(publicKey)
	if !bytes.Equal(keyID, expectedKeyID) {
		return E2ERecipient{}, fmt.Errorf("known WebTTY server key id does not match public key")
	}
	return E2ERecipient{KeyID: keyID, PublicKey: publicKey}, nil
}

func ParseKnownServerEndpointIdentity(value string) (WebTTYEndpointIdentityPublic, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return WebTTYEndpointIdentityPublic{}, fmt.Errorf("known WebTTY server endpoint identity is empty")
	}
	parts := strings.Split(value, ":")
	if len(parts) != 4 {
		return WebTTYEndpointIdentityPublic{}, fmt.Errorf("known WebTTY server endpoint identity must be encryption_key_id:encryption_public_key:signing_key_id:signing_public_key")
	}
	encryption, err := ParseKnownServerKey(parts[0] + ":" + parts[1])
	if err != nil {
		return WebTTYEndpointIdentityPublic{}, err
	}
	signingKeyID, err := DecodeE2EKeyMaterial(parts[2], WebTTYSigningKeyIDSize, "known WebTTY server signing key id")
	if err != nil {
		return WebTTYEndpointIdentityPublic{}, err
	}
	signingPublicKey, err := DecodeE2EKeyMaterial(parts[3], 0, "known WebTTY server signing public key")
	if err != nil {
		return WebTTYEndpointIdentityPublic{}, err
	}
	if _, err := ParseWebTTYSigningPublicKey(signingPublicKey); err != nil {
		return WebTTYEndpointIdentityPublic{}, fmt.Errorf("known WebTTY server signing public key is invalid: %w", err)
	}
	expectedSigningKeyID := WebTTYSigningKeyID(signingPublicKey)
	if !bytes.Equal(signingKeyID, expectedSigningKeyID) {
		return WebTTYEndpointIdentityPublic{}, fmt.Errorf("known WebTTY server signing key id does not match public key")
	}
	return WebTTYEndpointIdentityPublic{
		EncryptionKeyID:     encryption.KeyID,
		EncryptionPublicKey: encryption.PublicKey,
		SigningKeyID:        signingKeyID,
		SigningPublicKey:    signingPublicKey,
	}, nil
}

func KnownServerEndpointIdentityString(identity WebTTYEndpointIdentityPublic) string {
	return strings.Join([]string{
		EncodeE2EKeyMaterial(identity.EncryptionKeyID),
		EncodeE2EKeyMaterial(identity.EncryptionPublicKey),
		EncodeE2EKeyMaterial(identity.SigningKeyID),
		EncodeE2EKeyMaterial(identity.SigningPublicKey),
	}, ":")
}

func EncodeE2EIdentityJSON(identity E2EIdentity) ([]byte, error) {
	if err := validateE2EIdentity(identity); err != nil {
		return nil, err
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	doc := E2EIdentityFile{
		Version:     E2EIdentityFileVersion,
		CryptoSuite: E2EKeyFileCryptoSuite,
		KeyID:       EncodeE2EKeyMaterial(identity.KeyID),
		PublicKey:   EncodeE2EKeyMaterial(identity.PublicKey),
		PrivateKey:  EncodeE2EKeyMaterial(identity.PrivateKey),
		CreatedAt:   createdAt,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode E2E identity: %w", err)
	}
	return append(data, '\n'), nil
}

func DecodeE2EIdentityJSON(data []byte) (*E2EIdentity, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc E2EIdentityFile
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode E2E identity: %w", err)
	}
	if err := ensureSingleJSONValue(decoder, "decode E2E identity"); err != nil {
		return nil, err
	}
	if doc.Version != E2EIdentityFileVersion {
		return nil, fmt.Errorf("unsupported E2E identity version %d", doc.Version)
	}
	if doc.CryptoSuite != E2EKeyFileCryptoSuite {
		return nil, fmt.Errorf("unsupported E2E identity crypto suite %q", doc.CryptoSuite)
	}
	keyID, err := DecodeE2EKeyMaterial(doc.KeyID, E2EPayloadKeyIDSize, "E2E identity key id")
	if err != nil {
		return nil, err
	}
	publicKey, err := DecodeE2EKeyMaterial(doc.PublicKey, E2EX25519PublicKeySize, "E2E identity public key")
	if err != nil {
		return nil, err
	}
	privateKey, err := DecodeE2EKeyMaterial(doc.PrivateKey, E2EX25519PrivateKeySize, "E2E identity private key")
	if err != nil {
		return nil, err
	}
	identity, err := E2EIdentityFromPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(identity.PublicKey, publicKey) {
		return nil, fmt.Errorf("E2E identity public key does not match private key")
	}
	if !bytes.Equal(identity.KeyID, keyID) {
		return nil, fmt.Errorf("E2E identity key id does not match public key")
	}
	return identity, nil
}

func LoadE2EIdentityFile(path string) (*E2EIdentity, error) {
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
	identity, err := DecodeE2EIdentityJSON(data)
	if err != nil {
		return nil, fmt.Errorf("load E2E identity %s: %w", path, err)
	}
	return identity, nil
}

func LoadOrCreateE2EIdentityFile(path string) (*E2EIdentity, error) {
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
	identity, err := loadE2EIdentityFileUnlocked(path)
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	identity, err = GenerateE2EIdentity()
	if err != nil {
		return nil, err
	}
	if err := writeE2EIdentityFileUnlocked(path, *identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func WriteE2EIdentityFile(path string, identity E2EIdentity) error {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultE2EIdentityPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlockLocal := lockKnownServerKeysFile(path)
	defer unlockLocal()
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	return writeE2EIdentityFileUnlocked(path, identity)
}

func RemoveE2EIdentityFile(path string) error {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultE2EIdentityPath()
		if err != nil {
			return err
		}
	}
	unlockLocal := lockKnownServerKeysFile(path)
	defer unlockLocal()
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func LoadKnownServerKeysFile(path string) ([]E2ERecipient, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultKnownServerKeysPath()
		if err != nil {
			return nil, err
		}
	}
	if err := checkKnownServerKeysFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := DecodeKnownServerKeysFile(data, path)
	if err != nil {
		return nil, err
	}
	keys := make([]E2ERecipient, 0, len(doc.KnownServers))
	for i, entry := range doc.KnownServers {
		key, err := knownServerKeyFromEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("decode known WebTTY server key %d: %w", i, err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func LoadKnownServerEndpointIdentitiesFile(path string) ([]WebTTYEndpointIdentityPublic, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultKnownServerKeysPath()
		if err != nil {
			return nil, err
		}
	}
	if err := checkKnownServerKeysFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := DecodeKnownServerKeysFile(data, path)
	if err != nil {
		return nil, err
	}
	identities := make([]WebTTYEndpointIdentityPublic, 0, len(doc.KnownServers))
	for i, entry := range doc.KnownServers {
		identity, ok, err := knownServerEndpointIdentityFromEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("decode known WebTTY server identity %d: %w", i, err)
		}
		if ok {
			identities = append(identities, identity)
		}
	}
	return identities, nil
}

func ReadKnownServerKeysFile(path string) (*KnownServerKeysFile, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultKnownServerKeysPath()
		if err != nil {
			return nil, err
		}
	}
	if err := checkKnownServerKeysFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeKnownServerKeysFile(data, path)
}

func WriteKnownServerKeysFile(path string, doc KnownServerKeysFile) error {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultKnownServerKeysPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	return writeKnownServerKeysFileUnlocked(path, doc)
}

func UpdateKnownServerKeysFile(path string, update func(*KnownServerKeysFile) error) (*KnownServerKeysFile, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultKnownServerKeysPath()
		if err != nil {
			return nil, err
		}
	}
	if update == nil {
		return nil, fmt.Errorf("known WebTTY server keys update function is nil")
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
	doc, err := readKnownServerKeysFileUnlocked(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		doc = &KnownServerKeysFile{Version: E2EIdentityFileVersion, CryptoSuite: E2EKeyFileCryptoSuite}
	}
	if err := update(doc); err != nil {
		return nil, err
	}
	if err := writeKnownServerKeysFileUnlocked(path, *doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func writeKnownServerKeysFileUnlocked(path string, doc KnownServerKeysFile) error {
	doc.Version = E2EIdentityFileVersion
	doc.CryptoSuite = E2EKeyFileCryptoSuite
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode known WebTTY server keys: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".webtty-known-servers-*.tmp")
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

func readKnownServerKeysFileUnlocked(path string) (*KnownServerKeysFile, error) {
	if err := checkKnownServerKeysFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeKnownServerKeysFile(data, path)
}

func lockKnownServerKeysFile(path string) func() {
	key := filepath.Clean(path)
	value, _ := knownServerKeysFileLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func DecodeKnownServerKeysFile(data []byte, path string) (*KnownServerKeysFile, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc KnownServerKeysFile
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode known WebTTY server keys %s: %w", path, err)
	}
	if err := ensureSingleJSONValue(decoder, "decode known WebTTY server keys "+path); err != nil {
		return nil, err
	}
	if doc.Version != E2EIdentityFileVersion {
		return nil, fmt.Errorf("unsupported known WebTTY server keys version %d", doc.Version)
	}
	if doc.CryptoSuite != E2EKeyFileCryptoSuite {
		return nil, fmt.Errorf("unsupported known WebTTY server keys crypto suite %q", doc.CryptoSuite)
	}
	return &doc, nil
}

func ensureSingleJSONValue(decoder *json.Decoder, prefix string) error {
	var extra struct{}
	err := decoder.Decode(&extra)
	if err == nil {
		return fmt.Errorf("%s: multiple JSON values", prefix)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return nil
}

func loadE2EIdentityFileUnlocked(path string) (*E2EIdentity, error) {
	if err := checkSensitiveFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	identity, err := DecodeE2EIdentityJSON(data)
	if err != nil {
		return nil, fmt.Errorf("load E2E identity %s: %w", path, err)
	}
	return identity, nil
}

func writeE2EIdentityFileUnlocked(path string, identity E2EIdentity) error {
	data, err := EncodeE2EIdentityJSON(identity)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".webtty-identity-*.tmp")
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

func validateE2EIdentity(identity E2EIdentity) error {
	if len(identity.PrivateKey) != E2EX25519PrivateKeySize {
		return fmt.Errorf("E2E identity private key must be %d bytes", E2EX25519PrivateKeySize)
	}
	derived, err := E2EIdentityFromPrivateKey(identity.PrivateKey)
	if err != nil {
		return err
	}
	if len(identity.PublicKey) != E2EX25519PublicKeySize {
		return fmt.Errorf("E2E identity public key must be %d bytes", E2EX25519PublicKeySize)
	}
	if !bytes.Equal(identity.PublicKey, derived.PublicKey) {
		return fmt.Errorf("E2E identity public key does not match private key")
	}
	if len(identity.KeyID) != E2EPayloadKeyIDSize {
		return fmt.Errorf("E2E identity key id must be %d bytes", E2EPayloadKeyIDSize)
	}
	if !bytes.Equal(identity.KeyID, derived.KeyID) {
		return fmt.Errorf("E2E identity key id does not match public key")
	}
	return nil
}

func checkSensitiveFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("E2E identity file %s must not be readable by group or others", path)
	}
	return nil
}

func checkKnownServerKeysFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("known WebTTY server keys file %s must not be readable by group or others", path)
	}
	return nil
}

func knownServerKeyFromEntry(entry KnownServerKeyEntry) (E2ERecipient, error) {
	if strings.TrimSpace(entry.Name) == "" {
		return E2ERecipient{}, fmt.Errorf("known WebTTY server name is required")
	}
	publicKey, err := DecodeE2EKeyMaterial(entry.PublicKey, E2EX25519PublicKeySize, "known WebTTY server public key")
	if err != nil {
		return E2ERecipient{}, err
	}
	keyID := E2EKeyID(publicKey)
	if strings.TrimSpace(entry.KeyID) == "" {
		return E2ERecipient{}, fmt.Errorf("known WebTTY server key id is required")
	}
	decodedKeyID, err := DecodeE2EKeyMaterial(entry.KeyID, E2EPayloadKeyIDSize, "known WebTTY server key id")
	if err != nil {
		return E2ERecipient{}, err
	}
	if !bytes.Equal(decodedKeyID, keyID) {
		return E2ERecipient{}, fmt.Errorf("known WebTTY server key id does not match public key")
	}
	return E2ERecipient{KeyID: decodedKeyID, PublicKey: publicKey}, nil
}

func knownServerEndpointIdentityFromEntry(entry KnownServerKeyEntry) (WebTTYEndpointIdentityPublic, bool, error) {
	encryption, err := knownServerKeyFromEntry(entry)
	if err != nil {
		return WebTTYEndpointIdentityPublic{}, false, err
	}
	if strings.TrimSpace(entry.SigningKeyID) == "" && strings.TrimSpace(entry.SigningPublicKey) == "" {
		return WebTTYEndpointIdentityPublic{}, false, nil
	}
	if strings.TrimSpace(entry.SigningKeyID) == "" || strings.TrimSpace(entry.SigningPublicKey) == "" {
		return WebTTYEndpointIdentityPublic{}, false, fmt.Errorf("known WebTTY server signing key id and public key must be set together")
	}
	signingKeyID, err := DecodeE2EKeyMaterial(entry.SigningKeyID, WebTTYSigningKeyIDSize, "known WebTTY server signing key id")
	if err != nil {
		return WebTTYEndpointIdentityPublic{}, false, err
	}
	signingPublicKey, err := DecodeE2EKeyMaterial(entry.SigningPublicKey, 0, "known WebTTY server signing public key")
	if err != nil {
		return WebTTYEndpointIdentityPublic{}, false, err
	}
	if _, err := ParseWebTTYSigningPublicKey(signingPublicKey); err != nil {
		return WebTTYEndpointIdentityPublic{}, false, fmt.Errorf("known WebTTY server signing public key is invalid: %w", err)
	}
	expectedSigningKeyID := WebTTYSigningKeyID(signingPublicKey)
	if !bytes.Equal(signingKeyID, expectedSigningKeyID) {
		return WebTTYEndpointIdentityPublic{}, false, fmt.Errorf("known WebTTY server signing key id does not match public key")
	}
	return WebTTYEndpointIdentityPublic{
		EncryptionKeyID:     cloneBytes(encryption.KeyID),
		EncryptionPublicKey: cloneBytes(encryption.PublicKey),
		SigningKeyID:        signingKeyID,
		SigningPublicKey:    signingPublicKey,
	}, true, nil
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
)

var authorizedClientKeysFileLocks sync.Map

type AuthorizedClientKeysFile struct {
	Version           int                        `json:"version"`
	CryptoSuite       string                     `json:"crypto_suite"`
	AuthorizedClients []AuthorizedClientKeyEntry `json:"authorized_clients"`
}

type AuthorizedClientKeyEntry struct {
	Name             string `json:"name"`
	SigningKeyID     string `json:"signing_key_id"`
	SigningPublicKey string `json:"signing_public_key"`
	CreatedAt        string `json:"created_at,omitempty"`
}

func DefaultAuthorizedClientKeysPath(identityName string) (string, error) {
	name := strings.TrimSpace(identityName)
	if name == "" {
		name = "default"
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("authorized WebTTY client store name contains unsupported path characters")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rstream", "webtty", "authorized_clients", name+".json"), nil
}

func AuthorizedClientSigningKeyString(identity WebTTYEndpointIdentityPublic) string {
	return strings.Join([]string{
		EncodeE2EKeyMaterial(identity.SigningKeyID),
		EncodeE2EKeyMaterial(identity.SigningPublicKey),
	}, ":")
}

func ParseAuthorizedClientSigningKey(value string) ([]byte, []byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil, fmt.Errorf("authorized WebTTY client signing key is empty")
	}
	if strings.Count(value, ":") == 3 {
		identity, err := ParseKnownServerEndpointIdentity(value)
		if err != nil {
			return nil, nil, err
		}
		return cloneBytes(identity.SigningKeyID), cloneBytes(identity.SigningPublicKey), nil
	}
	parts := strings.Split(value, ":")
	var encodedKeyID string
	var encodedPublicKey string
	switch len(parts) {
	case 1:
		encodedPublicKey = strings.TrimSpace(parts[0])
	case 2:
		encodedKeyID = strings.TrimSpace(parts[0])
		encodedPublicKey = strings.TrimSpace(parts[1])
	default:
		return nil, nil, fmt.Errorf("authorized WebTTY client signing key must be signing_public_key, signing_key_id:signing_public_key, or a WebTTY endpoint identity")
	}
	publicKey, err := DecodeE2EKeyMaterial(encodedPublicKey, 0, "authorized WebTTY client signing public key")
	if err != nil {
		return nil, nil, err
	}
	if _, err := ParseWebTTYSigningPublicKey(publicKey); err != nil {
		return nil, nil, fmt.Errorf("authorized WebTTY client signing public key is invalid: %w", err)
	}
	keyID := WebTTYSigningKeyID(publicKey)
	if encodedKeyID != "" {
		decodedKeyID, err := DecodeE2EKeyMaterial(encodedKeyID, WebTTYSigningKeyIDSize, "authorized WebTTY client signing key id")
		if err != nil {
			return nil, nil, err
		}
		if !bytes.Equal(decodedKeyID, keyID) {
			return nil, nil, fmt.Errorf("authorized WebTTY client signing key id does not match public key")
		}
		keyID = decodedKeyID
	}
	return keyID, publicKey, nil
}

func LoadAuthorizedClientSigningKeysFile(path string) (map[string][]byte, error) {
	doc, err := ReadAuthorizedClientKeysFile(path)
	if err != nil {
		return nil, err
	}
	keys := make(map[string][]byte, len(doc.AuthorizedClients))
	for i, entry := range doc.AuthorizedClients {
		keyID, publicKey, err := authorizedClientSigningKeyFromEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("decode authorized WebTTY client %d: %w", i, err)
		}
		keys[string(keyID)] = publicKey
	}
	return keys, nil
}

func ReadAuthorizedClientKeysFile(path string) (*AuthorizedClientKeysFile, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultAuthorizedClientKeysPath("")
		if err != nil {
			return nil, err
		}
	}
	if err := checkAuthorizedClientKeysFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeAuthorizedClientKeysFile(data, path)
}

func UpdateAuthorizedClientKeysFile(path string, update func(*AuthorizedClientKeysFile) error) (*AuthorizedClientKeysFile, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultAuthorizedClientKeysPath("")
		if err != nil {
			return nil, err
		}
	}
	if update == nil {
		return nil, fmt.Errorf("authorized WebTTY client keys update function is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	unlockLocal := lockAuthorizedClientKeysFile(path)
	defer unlockLocal()
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.Unlock()
	doc, err := readAuthorizedClientKeysFileUnlocked(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		doc = &AuthorizedClientKeysFile{Version: E2EIdentityFileVersion, CryptoSuite: E2EKeyFileCryptoSuite}
	}
	if err := update(doc); err != nil {
		return nil, err
	}
	sort.Slice(doc.AuthorizedClients, func(i int, j int) bool {
		return doc.AuthorizedClients[i].Name < doc.AuthorizedClients[j].Name
	})
	if err := writeAuthorizedClientKeysFileUnlocked(path, *doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func DecodeAuthorizedClientKeysFile(data []byte, path string) (*AuthorizedClientKeysFile, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc AuthorizedClientKeysFile
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode authorized WebTTY client keys %s: %w", path, err)
	}
	if err := ensureSingleJSONValue(decoder, "decode authorized WebTTY client keys "+path); err != nil {
		return nil, err
	}
	if doc.Version != E2EIdentityFileVersion {
		return nil, fmt.Errorf("unsupported authorized WebTTY client keys version %d", doc.Version)
	}
	if doc.CryptoSuite != E2EKeyFileCryptoSuite {
		return nil, fmt.Errorf("unsupported authorized WebTTY client keys crypto suite %q", doc.CryptoSuite)
	}
	return &doc, nil
}

func NewAuthorizedClientSigningKeyFileResolver(path string) AuthorizedClientSigningKeyResolver {
	return func(ctx context.Context, signingKeyID []byte) ([]byte, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		keys, err := LoadAuthorizedClientSigningKeysFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		return cloneBytes(keys[string(signingKeyID)]), nil
	}
}

func NewAuthorizedClientKeyEntry(name string, rawKey string) (AuthorizedClientKeyEntry, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AuthorizedClientKeyEntry{}, fmt.Errorf("authorized WebTTY client name is required")
	}
	keyID, publicKey, err := ParseAuthorizedClientSigningKey(rawKey)
	if err != nil {
		return AuthorizedClientKeyEntry{}, err
	}
	return AuthorizedClientKeyEntry{
		Name:             name,
		SigningKeyID:     EncodeE2EKeyMaterial(keyID),
		SigningPublicKey: EncodeE2EKeyMaterial(publicKey),
		CreatedAt:        time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
	}, nil
}

func authorizedClientSigningKeyFromEntry(entry AuthorizedClientKeyEntry) ([]byte, []byte, error) {
	if strings.TrimSpace(entry.Name) == "" {
		return nil, nil, fmt.Errorf("authorized WebTTY client name is required")
	}
	return ParseAuthorizedClientSigningKey(entry.SigningKeyID + ":" + entry.SigningPublicKey)
}

func readAuthorizedClientKeysFileUnlocked(path string) (*AuthorizedClientKeysFile, error) {
	if err := checkAuthorizedClientKeysFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeAuthorizedClientKeysFile(data, path)
}

func writeAuthorizedClientKeysFileUnlocked(path string, doc AuthorizedClientKeysFile) error {
	doc.Version = E2EIdentityFileVersion
	doc.CryptoSuite = E2EKeyFileCryptoSuite
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode authorized WebTTY client keys: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".webtty-authorized-clients-*.tmp")
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

func checkAuthorizedClientKeysFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("authorized WebTTY client keys file %s must not be readable by group or others", path)
	}
	return nil
}

func lockAuthorizedClientKeysFile(path string) func() {
	key := filepath.Clean(path)
	value, _ := authorizedClientKeysFileLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

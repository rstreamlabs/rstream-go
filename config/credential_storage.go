// See LICENSE file in the project root for license information.

package config

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

const DefaultMacOSKeychainTokenService = "io.rstream.auth"

func DefaultMacOSKeychainTokenAccount(apiURL string) string {
	apiURL = NormalizeAPIURL(apiURL)
	if apiURL == "" {
		return ""
	}
	return "api:" + apiURL
}

func DefaultMacOSKeychainContextTokenAccount(name, apiURL string) string {
	name = strings.TrimSpace(name)
	apiURL = NormalizeAPIURL(apiURL)
	if name == "" {
		return ""
	}
	if apiURL == "" {
		return "context:" + name
	}
	return "context:" + apiURL + ":" + name
}

func NewMacOSKeychainTokenStorage(apiURL string) TokenStorage {
	return TokenStorage{
		Kind:     TokenStorageKeychain,
		Provider: CredentialProviderMacOS,
		Service:  DefaultMacOSKeychainTokenService,
		Account:  DefaultMacOSKeychainTokenAccount(apiURL),
	}
}

func StoreToken(storage TokenStorage, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("token is empty")
	}
	switch strings.TrimSpace(storage.Kind) {
	case TokenStorageInline:
		return errors.New("inline token storage is represented in config and does not require StoreToken")
	case TokenStorageKeychain:
		if err := validateMacOSKeychainTokenStorage(&storage); err != nil {
			return err
		}
		return storeMacOSKeychainToken(&storage, token)
	case "":
		return errors.New("token storage kind is required")
	default:
		return fmt.Errorf("token storage kind %q is not supported", storage.Kind)
	}
}

func DeleteToken(storage TokenStorage) error {
	switch strings.TrimSpace(storage.Kind) {
	case TokenStorageInline:
		return nil
	case TokenStorageKeychain:
		if err := validateMacOSKeychainTokenStorage(&storage); err != nil {
			return err
		}
		return deleteMacOSKeychainToken(&storage)
	case "":
		return nil
	default:
		return fmt.Errorf("token storage kind %q is not supported", storage.Kind)
	}
}

func tokenFromKeychainStorage(storage *TokenStorage) (string, bool, error) {
	if err := validateMacOSKeychainTokenStorage(storage); err != nil {
		return "", false, err
	}
	token, err := loadMacOSKeychainToken(storage)
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

func validateMacOSKeychainTokenStorage(storage *TokenStorage) error {
	if storage == nil {
		return errors.New("token keychain storage is required")
	}
	if strings.TrimSpace(storage.Provider) == "" {
		return errors.New("token keychain storage provider is required")
	}
	if strings.TrimSpace(storage.Provider) != CredentialProviderMacOS {
		return fmt.Errorf("token keychain provider %q is not supported", storage.Provider)
	}
	if strings.TrimSpace(storage.Service) == "" {
		return errors.New("macOS keychain token service is required")
	}
	if strings.TrimSpace(storage.Account) == "" {
		return errors.New("macOS keychain token account is required")
	}
	if strings.TrimSpace(storage.Value) != "" {
		return errors.New("macOS keychain token storage cannot include an inline value")
	}
	return nil
}

func loadMTLSStorageConfig(storage *MTLSStorage) (*tls.Config, error) {
	if storage == nil {
		return nil, errors.New("mTLS storage is required")
	}
	switch strings.TrimSpace(storage.Kind) {
	case MTLSStoragePKCS11:
		if err := validatePKCS11MTLSStorage(storage); err != nil {
			return nil, err
		}
		return loadPKCS11MTLSConfig(storage)
	case MTLSStorageKeychain:
		if err := validateMacOSKeychainMTLSStorage(storage); err != nil {
			return nil, err
		}
		return loadMacOSKeychainMTLSConfig(storage)
	case "":
		return nil, errors.New("mTLS storage kind is required")
	default:
		return nil, fmt.Errorf("mTLS storage kind %q is not supported", storage.Kind)
	}
}

func validatePKCS11MTLSStorage(storage *MTLSStorage) error {
	if strings.TrimSpace(storage.Provider) != "" {
		return errors.New("pkcs11 mTLS storage must not set provider")
	}
	if strings.TrimSpace(storage.Module) == "" {
		return errors.New("pkcs11 mTLS module is required")
	}
	if countNonEmpty(strings.TrimSpace(storage.TokenLabel), strings.TrimSpace(storage.TokenSerial))+
		countSet(storage.Slot != nil) != 1 {
		return errors.New("pkcs11 mTLS storage requires exactly one token selector: tokenLabel, tokenSerial, or slot")
	}
	if countNonEmpty(strings.TrimSpace(storage.KeyLabel), strings.TrimSpace(storage.KeyIDHex)) != 1 {
		return errors.New("pkcs11 mTLS storage requires exactly one key selector: keyLabel or keyIdHex")
	}
	if countNonEmpty(strings.TrimSpace(storage.Certificate), strings.TrimSpace(storage.CertificateFile))+
		countNonEmpty(strings.TrimSpace(storage.CertificateLabel), strings.TrimSpace(storage.CertificateIDHex)) == 0 {
		return errors.New("pkcs11 mTLS storage requires a certificate source")
	}
	if countNonEmpty(strings.TrimSpace(storage.Certificate), strings.TrimSpace(storage.CertificateFile)) > 1 {
		return errors.New("pkcs11 mTLS storage can use only one certificate PEM source")
	}
	if countNonEmpty(strings.TrimSpace(storage.CertificateLabel), strings.TrimSpace(storage.CertificateIDHex)) > 1 {
		return errors.New("pkcs11 mTLS storage can use only one token certificate selector")
	}
	if countNonEmpty(strings.TrimSpace(storage.Certificate), strings.TrimSpace(storage.CertificateFile)) > 0 &&
		countNonEmpty(strings.TrimSpace(storage.CertificateLabel), strings.TrimSpace(storage.CertificateIDHex)) > 0 {
		return errors.New("pkcs11 mTLS storage cannot mix PEM and token certificate sources")
	}
	if strings.TrimSpace(storage.PINEnv) == "" {
		return errors.New("pkcs11 mTLS pinEnv is required")
	}
	if storage.MaxSessions < 0 {
		return errors.New("pkcs11 mTLS maxSessions cannot be negative")
	}
	if strings.TrimSpace(storage.CertificateSHA256) != "" {
		return errors.New("pkcs11 mTLS storage contains macOS keychain fields")
	}
	return nil
}

func validateMacOSKeychainMTLSStorage(storage *MTLSStorage) error {
	if strings.TrimSpace(storage.Provider) == "" {
		return errors.New("mTLS keychain storage provider is required")
	}
	if strings.TrimSpace(storage.Provider) != CredentialProviderMacOS {
		return fmt.Errorf("mTLS keychain provider %q is not supported", storage.Provider)
	}
	if strings.TrimSpace(storage.CertificateSHA256) == "" {
		return errors.New("macOS keychain mTLS certificateSHA256 is required")
	}
	if _, err := decodeCertificateSHA256(storage.CertificateSHA256); err != nil {
		return err
	}
	if strings.TrimSpace(storage.Module) != "" ||
		strings.TrimSpace(storage.OpenSSLProvider) != "" ||
		strings.TrimSpace(storage.TokenLabel) != "" ||
		strings.TrimSpace(storage.TokenSerial) != "" ||
		storage.Slot != nil ||
		strings.TrimSpace(storage.KeyLabel) != "" ||
		strings.TrimSpace(storage.KeyIDHex) != "" ||
		strings.TrimSpace(storage.PINEnv) != "" ||
		storage.MaxSessions != 0 {
		return errors.New("macOS keychain mTLS storage contains pkcs11 fields")
	}
	if strings.TrimSpace(storage.Certificate) != "" ||
		strings.TrimSpace(storage.CertificateFile) != "" ||
		strings.TrimSpace(storage.CertificateLabel) != "" ||
		strings.TrimSpace(storage.CertificateIDHex) != "" {
		return errors.New("macOS keychain mTLS storage cannot include a separate certificate source")
	}
	return nil
}

func pkcs11PIN(storage *MTLSStorage) (string, error) {
	name := strings.TrimSpace(storage.PINEnv)
	if name == "" {
		return "", errors.New("pkcs11 mTLS pinEnv is required")
	}
	pin, ok := os.LookupEnv(name)
	if !ok || pin == "" {
		return "", fmt.Errorf("pkcs11 mTLS PIN environment variable %s is not set", name)
	}
	return pin, nil
}

func parsePEMCertificateChain(certPEM []byte) ([][]byte, *x509.Certificate, error) {
	var chain [][]byte
	rest := certPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		chain = append(chain, block.Bytes)
	}
	if len(chain) == 0 {
		return nil, nil, errors.New("certificate PEM does not contain a certificate")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil, nil, fmt.Errorf("parse leaf certificate: %w", err)
	}
	return chain, leaf, nil
}

func loadPEMCertificateChain(certPEM, certFile string) ([][]byte, *x509.Certificate, error) {
	certPEM = strings.TrimSpace(certPEM)
	certFile = strings.TrimSpace(certFile)
	switch {
	case certPEM != "" && certFile != "":
		return nil, nil, errors.New("certificate PEM and certificate file cannot both be set")
	case certPEM != "":
		return parsePEMCertificateChain([]byte(certPEM))
	case certFile != "":
		data, err := os.ReadFile(certFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read certificate file: %w", err)
		}
		return parsePEMCertificateChain(data)
	default:
		return nil, nil, errors.New("certificate source is required")
	}
}

func decodeHexField(name, value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	value = strings.ReplaceAll(value, ":", "")
	value = strings.ReplaceAll(value, " ", "")
	out, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be hexadecimal: %w", name, err)
	}
	return out, nil
}

func decodeCertificateSHA256(value string) ([]byte, error) {
	fingerprint, err := decodeHexField("certificateSHA256", value)
	if err != nil {
		return nil, err
	}
	if len(fingerprint) != 32 {
		return nil, errors.New("certificateSHA256 must be a SHA-256 digest")
	}
	return fingerprint, nil
}

func publicKeysEqual(a, b any) bool {
	aDER, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false
	}
	bDER, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aDER, bDER)
}

func countNonEmpty(values ...string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func countSet(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

// See LICENSE file in the project root for license information.

//go:build !darwin || !cgo

package config

import (
	"crypto/tls"
	"errors"
)

func loadMacOSKeychainToken(_ *TokenStorage) (string, error) {
	return "", errors.New("macOS keychain token storage is not available in this build")
}

func storeMacOSKeychainToken(_ *TokenStorage, _ string) error {
	return errors.New("macOS keychain token storage is not available in this build")
}

func deleteMacOSKeychainToken(_ *TokenStorage) error {
	return errors.New("macOS keychain token storage is not available in this build")
}

func loadMacOSKeychainMTLSConfig(_ *MTLSStorage) (*tls.Config, error) {
	return nil, errors.New("macOS keychain mTLS storage is not available in this build")
}

// See LICENSE file in the project root for license information.

//go:build !cgo

package config

import (
	"crypto/tls"
	"errors"
)

func loadPKCS11MTLSConfig(_ *MTLSStorage) (*tls.Config, error) {
	return nil, errors.New("pkcs11 mTLS storage is not available in this build")
}

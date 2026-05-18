// See LICENSE file in the project root for license information.

//go:build cgo

package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"

	"github.com/ThalesGroup/crypto11"
)

func loadPKCS11MTLSConfig(storage *MTLSStorage) (*tls.Config, error) {
	pin, err := pkcs11PIN(storage)
	if err != nil {
		return nil, err
	}
	cfg := &crypto11.Config{
		Path:        strings.TrimSpace(storage.Module),
		TokenLabel:  strings.TrimSpace(storage.TokenLabel),
		TokenSerial: strings.TrimSpace(storage.TokenSerial),
		Pin:         pin,
		MaxSessions: storage.MaxSessions,
	}
	if storage.Slot != nil {
		slot := *storage.Slot
		cfg.SlotNumber = &slot
	}
	ctx, err := crypto11.Configure(cfg)
	if err != nil {
		return nil, fmt.Errorf("configure pkcs11 module: %w", err)
	}
	keyID, err := decodeHexField("keyIdHex", storage.KeyIDHex)
	if err != nil {
		return nil, err
	}
	var keyLabel []byte
	if strings.TrimSpace(storage.KeyLabel) != "" {
		keyLabel = []byte(strings.TrimSpace(storage.KeyLabel))
	}
	signer, err := ctx.FindKeyPair(keyID, keyLabel)
	if err != nil {
		return nil, fmt.Errorf("load pkcs11 private key: %w", err)
	}
	chain, leaf, err := loadPKCS11Certificate(ctx, storage)
	if err != nil {
		return nil, err
	}
	if !publicKeysEqual(signer.Public(), leaf.PublicKey) {
		return nil, errors.New("mTLS certificate public key does not match private key")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: chain,
			PrivateKey:  signer,
			Leaf:        leaf,
		}},
	}, nil
}

func loadPKCS11Certificate(ctx *crypto11.Context, storage *MTLSStorage) ([][]byte, *x509.Certificate, error) {
	if strings.TrimSpace(storage.Certificate) != "" || strings.TrimSpace(storage.CertificateFile) != "" {
		return loadPEMCertificateChain(storage.Certificate, storage.CertificateFile)
	}
	certID, err := decodeHexField("certificateIdHex", storage.CertificateIDHex)
	if err != nil {
		return nil, nil, err
	}
	var certLabel []byte
	if strings.TrimSpace(storage.CertificateLabel) != "" {
		certLabel = []byte(strings.TrimSpace(storage.CertificateLabel))
	}
	cert, err := ctx.FindCertificate(certID, certLabel, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("load pkcs11 certificate: %w", err)
	}
	if cert == nil {
		return nil, nil, errors.New("pkcs11 certificate was not found")
	}
	return [][]byte{cert.Raw}, cert, nil
}

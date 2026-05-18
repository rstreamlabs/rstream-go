// See LICENSE file in the project root for license information.

//go:build darwin && cgo

package config

/*
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security
#include "keychain_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"unsafe"
)

func loadMacOSKeychainToken(storage *TokenStorage) (string, error) {
	service := C.CString(strings.TrimSpace(storage.Service))
	defer C.free(unsafe.Pointer(service))
	account := C.CString(strings.TrimSpace(storage.Account))
	defer C.free(unsafe.Pointer(account))
	var data C.CFDataRef
	status := C.rs_copy_generic_password(service, account, &data)
	if status != C.errSecSuccess {
		if status == C.errSecItemNotFound {
			return "", fmt.Errorf("macOS keychain token not found for service %q account %q", storage.Service, storage.Account)
		}
		return "", macOSStatusError("read macOS keychain token", status)
	}
	defer C.CFRelease(C.CFTypeRef(data))
	token := cfDataBytes(data)
	if len(token) == 0 {
		return "", fmt.Errorf("macOS keychain token is empty for service %q account %q", storage.Service, storage.Account)
	}
	return string(token), nil
}

func storeMacOSKeychainToken(storage *TokenStorage, token string) error {
	service := C.CString(strings.TrimSpace(storage.Service))
	defer C.free(unsafe.Pointer(service))
	account := C.CString(strings.TrimSpace(storage.Account))
	defer C.free(unsafe.Pointer(account))
	tokenBytes := []byte(token)
	status := C.rs_store_generic_password(
		service,
		account,
		(*C.uchar)(unsafe.Pointer(&tokenBytes[0])),
		C.size_t(len(tokenBytes)),
	)
	if status != C.errSecSuccess {
		return macOSStatusError("store macOS keychain token", status)
	}
	return nil
}

func deleteMacOSKeychainToken(storage *TokenStorage) error {
	service := C.CString(strings.TrimSpace(storage.Service))
	defer C.free(unsafe.Pointer(service))
	account := C.CString(strings.TrimSpace(storage.Account))
	defer C.free(unsafe.Pointer(account))
	status := C.rs_delete_generic_password(service, account)
	if status == C.errSecSuccess || status == C.errSecItemNotFound {
		return nil
	}
	return macOSStatusError("delete macOS keychain token", status)
}

func loadMacOSKeychainMTLSConfig(storage *MTLSStorage) (*tls.Config, error) {
	fingerprint, err := decodeCertificateSHA256(storage.CertificateSHA256)
	if err != nil {
		return nil, err
	}
	var key C.SecKeyRef
	var certData C.CFDataRef
	status := C.rs_copy_identity_key_by_sha256(
		(*C.uchar)(unsafe.Pointer(&fingerprint[0])),
		&key,
		&certData,
	)
	if status != C.errSecSuccess {
		if status == C.errSecItemNotFound {
			return nil, fmt.Errorf("macOS keychain identity with certificateSHA256 %q was not found", storage.CertificateSHA256)
		}
		return nil, macOSStatusError("load macOS keychain identity", status)
	}
	defer C.CFRelease(C.CFTypeRef(certData))
	certDER := cfDataBytes(certData)
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		C.CFRelease(C.CFTypeRef(key))
		return nil, fmt.Errorf("parse macOS keychain certificate: %w", err)
	}
	signer := newMacOSKeychainSigner(key, leaf.PublicKey)
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  signer,
			Leaf:        leaf,
		}},
	}, nil
}

type macOSKeychainSigner struct {
	key       C.SecKeyRef
	publicKey crypto.PublicKey
}

func newMacOSKeychainSigner(key C.SecKeyRef, publicKey crypto.PublicKey) *macOSKeychainSigner {
	signer := &macOSKeychainSigner{key: key, publicKey: publicKey}
	runtime.SetFinalizer(signer, (*macOSKeychainSigner).Close)
	return signer
}

func (s *macOSKeychainSigner) Public() crypto.PublicKey {
	return s.publicKey
}

func (s *macOSKeychainSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	var zeroKey C.SecKeyRef
	if s.key == zeroKey {
		return nil, errors.New("macOS keychain signer is closed")
	}
	if len(digest) == 0 {
		return nil, fmt.Errorf("macOS keychain signer digest is empty")
	}
	algorithm, err := macOSKeychainAlgorithm(s.publicKey, opts)
	if err != nil {
		return nil, err
	}
	var signature C.CFDataRef
	status := C.rs_sec_key_sign(
		s.key,
		algorithm,
		(*C.uchar)(unsafe.Pointer(&digest[0])),
		C.size_t(len(digest)),
		&signature,
	)
	if status != C.errSecSuccess {
		return nil, macOSStatusError("sign with macOS keychain private key", status)
	}
	defer C.CFRelease(C.CFTypeRef(signature))
	return cfDataBytes(signature), nil
}

func (s *macOSKeychainSigner) Close() {
	var zeroKey C.SecKeyRef
	if s.key != zeroKey {
		C.CFRelease(C.CFTypeRef(s.key))
		s.key = zeroKey
	}
}

func macOSKeychainAlgorithm(publicKey crypto.PublicKey, opts crypto.SignerOpts) (C.int, error) {
	if opts == nil {
		return 0, fmt.Errorf("macOS keychain signer options are required")
	}
	switch publicKey.(type) {
	case *rsa.PublicKey:
		if pss, ok := opts.(*rsa.PSSOptions); ok {
			if pss.SaltLength != rsa.PSSSaltLengthEqualsHash && pss.SaltLength != rsa.PSSSaltLengthAuto {
				return 0, fmt.Errorf("macOS keychain signer supports RSA-PSS only with automatic or hash-length salt")
			}
			switch opts.HashFunc() {
			case crypto.SHA256:
				return C.rsAlgRSAPSSSHA256, nil
			case crypto.SHA384:
				return C.rsAlgRSAPSSSHA384, nil
			case crypto.SHA512:
				return C.rsAlgRSAPSSSHA512, nil
			default:
				return 0, fmt.Errorf("macOS keychain signer does not support RSA-PSS with hash %v", opts.HashFunc())
			}
		}
		switch opts.HashFunc() {
		case crypto.SHA1:
			return C.rsAlgRSAPKCS1SHA1, nil
		case crypto.SHA256:
			return C.rsAlgRSAPKCS1SHA256, nil
		case crypto.SHA384:
			return C.rsAlgRSAPKCS1SHA384, nil
		case crypto.SHA512:
			return C.rsAlgRSAPKCS1SHA512, nil
		default:
			return 0, fmt.Errorf("macOS keychain signer does not support RSA PKCS#1 v1.5 with hash %v", opts.HashFunc())
		}
	case *ecdsa.PublicKey:
		switch opts.HashFunc() {
		case crypto.SHA1:
			return C.rsAlgECDSASHA1, nil
		case crypto.SHA256:
			return C.rsAlgECDSASHA256, nil
		case crypto.SHA384:
			return C.rsAlgECDSASHA384, nil
		case crypto.SHA512:
			return C.rsAlgECDSASHA512, nil
		default:
			return 0, fmt.Errorf("macOS keychain signer does not support ECDSA with hash %v", opts.HashFunc())
		}
	default:
		return 0, fmt.Errorf("macOS keychain signer does not support public key type %T", publicKey)
	}
}

func cfDataBytes(data C.CFDataRef) []byte {
	length := C.rs_cfdata_len(data)
	if length == 0 {
		return nil
	}
	ptr := C.rs_cfdata_bytes(data)
	return C.GoBytes(unsafe.Pointer(ptr), C.int(length))
}

func macOSStatusError(operation string, status C.OSStatus) error {
	message := C.SecCopyErrorMessageString(status, nil)
	var zeroMessage C.CFStringRef
	if message == zeroMessage {
		return fmt.Errorf("%s: macOS security error %d", operation, int(status))
	}
	defer C.CFRelease(C.CFTypeRef(message))
	var buffer [512]C.char
	if C.CFStringGetCString(message, &buffer[0], C.CFIndex(len(buffer)), C.kCFStringEncodingUTF8) == 0 {
		return fmt.Errorf("%s: macOS security error %d", operation, int(status))
	}
	return fmt.Errorf("%s: %s (%d)", operation, C.GoString(&buffer[0]), int(status))
}

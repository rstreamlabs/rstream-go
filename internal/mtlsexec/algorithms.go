// See LICENSE file in the project root for license information.

package mtlsexec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/tls"
	"errors"
)

type algorithm struct {
	name   string
	scheme tls.SignatureScheme
	hash   crypto.Hash
	key    string
	bits   int
	pss    bool
}

var algorithms = []algorithm{
	{"ecdsa_secp256r1_sha256", tls.ECDSAWithP256AndSHA256, crypto.SHA256, "ecdsa", 256, false},
	{"ecdsa_secp384r1_sha384", tls.ECDSAWithP384AndSHA384, crypto.SHA384, "ecdsa", 384, false},
	{"ecdsa_secp521r1_sha512", tls.ECDSAWithP521AndSHA512, crypto.SHA512, "ecdsa", 521, false},
	{"rsa_pss_rsae_sha256", tls.PSSWithSHA256, crypto.SHA256, "rsa", 0, true},
	{"rsa_pss_rsae_sha384", tls.PSSWithSHA384, crypto.SHA384, "rsa", 0, true},
	{"rsa_pss_rsae_sha512", tls.PSSWithSHA512, crypto.SHA512, "rsa", 0, true},
	{"rsa_pkcs1_sha256", tls.PKCS1WithSHA256, crypto.SHA256, "rsa", 0, false},
	{"rsa_pkcs1_sha384", tls.PKCS1WithSHA384, crypto.SHA384, "rsa", 0, false},
	{"rsa_pkcs1_sha512", tls.PKCS1WithSHA512, crypto.SHA512, "rsa", 0, false},
	{"ed25519", tls.Ed25519, crypto.Hash(0), "ed25519", 0, false},
}

func (a algorithm) supports(public crypto.PublicKey) bool {
	switch key := public.(type) {
	case *ecdsa.PublicKey:
		return a.key == "ecdsa" && key.Curve.Params().BitSize == a.bits
	case *rsa.PublicKey:
		return a.key == "rsa" && key.N.BitLen() >= 2048 && key.N.BitLen() <= 8192
	case ed25519.PublicKey:
		return a.key == "ed25519" && len(key) == ed25519.PublicKeySize
	default:
		return false
	}
}

func findAlgorithm(name string) (algorithm, bool) {
	for _, a := range algorithms {
		if a.name == name {
			return a, true
		}
	}
	return algorithm{}, false
}

func selectAlgorithm(public crypto.PublicKey, opts crypto.SignerOpts) (algorithm, error) {
	if opts == nil {
		return algorithm{}, errors.New("external mTLS signer requires signing options")
	}
	pss, isPSS := opts.(*rsa.PSSOptions)
	if isPSS && (pss == nil || pss.SaltLength != rsa.PSSSaltLengthEqualsHash) {
		return algorithm{}, errors.New("external mTLS signer requires RSA-PSS salt length equal to hash length")
	}
	if edOpts, ok := opts.(*ed25519.Options); ok && (edOpts == nil || edOpts.Context != "" || edOpts.Hash != 0) {
		return algorithm{}, errors.New("external mTLS signer supports only pure Ed25519")
	}
	for _, a := range algorithms {
		if a.supports(public) && a.hash == opts.HashFunc() && a.pss == isPSS {
			return a, nil
		}
	}
	return algorithm{}, errors.New("external mTLS signer does not support these signing options")
}

func (a algorithm) digest(message []byte) []byte {
	if a.hash == 0 {
		return message
	}
	h := a.hash.New()
	_, _ = h.Write(message)
	return h.Sum(nil)
}

func (a algorithm) verify(public crypto.PublicKey, digest, signature []byte) bool {
	switch key := public.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(key, digest, signature)
	case *rsa.PublicKey:
		if a.pss {
			return rsa.VerifyPSS(key, a.hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: a.hash}) == nil
		}
		return rsa.VerifyPKCS1v15(key, a.hash, digest, signature) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(key, digest, signature)
	default:
		return false
	}
}

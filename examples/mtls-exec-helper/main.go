// See LICENSE file in the project root for license information.

// mtls-exec-helper demonstrates protocol v1 using a SOFTWARE key. A hardware
// implementation replaces the key loading/signing functions with its vendor API.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	_ "crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"io"
	"os"
)

type request struct {
	Version           int    `json:"version"`
	Operation         string `json:"operation"`
	CertificateSHA256 string `json:"certificateSHA256"`
	Algorithm         string `json:"algorithm,omitempty"`
	Input             string `json:"input,omitempty"`
	Data              []byte `json:"data,omitempty"`
	PSSSaltLength     *int   `json:"pssSaltLength,omitempty"`
}

type capability struct {
	Algorithm string   `json:"algorithm"`
	Inputs    []string `json:"inputs"`
}

type response struct {
	Version          int          `json:"version"`
	CertificateChain string       `json:"certificateChain,omitempty"`
	Capabilities     []capability `json:"capabilities,omitempty"`
	Signature        []byte       `json:"signature,omitempty"`
	Error            string       `json:"error,omitempty"`
}

type algorithm struct {
	name string
	hash crypto.Hash
	pss  bool
}

func main() {
	certFile := flag.String("cert", "", "public certificate chain PEM file")
	keyFile := flag.String("key", "", "SOFTWARE private key PEM file, for development only")
	messageOnly := flag.Bool("message-only", false, "simulate a device that only supports signing messages")
	flag.Parse()
	var req request
	dec := json.NewDecoder(io.LimitReader(os.Stdin, (2<<20)+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail("invalid_request")
	}
	if dec.Decode(new(any)) != io.EOF || len(req.Data) > 1<<20 {
		fail("invalid_request")
	}
	if req.Version != 1 {
		fail("unsupported_version")
	}
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		fail("identity_unavailable")
	}
	fingerprint := sha256.Sum256(cert.Certificate[0])
	if req.CertificateSHA256 != hex.EncodeToString(fingerprint[:]) {
		fail("identity_mismatch")
	}
	key, ok := cert.PrivateKey.(crypto.Signer)
	if !ok {
		fail("identity_unavailable")
	}
	supported := supportedAlgorithms(key.Public())
	if len(supported) == 0 {
		fail("unsupported_algorithm")
	}
	out := response{Version: 1}
	switch req.Operation {
	case "identity":
		for _, der := range cert.Certificate {
			out.CertificateChain += string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		}
		for _, a := range supported {
			inputs := []string{"message"}
			if !*messageOnly && a.hash != 0 {
				inputs = append(inputs, "digest")
			}
			out.Capabilities = append(out.Capabilities, capability{Algorithm: a.name, Inputs: inputs})
		}
	case "sign":
		var selected *algorithm
		for i := range supported {
			if supported[i].name == req.Algorithm {
				selected = &supported[i]
				break
			}
		}
		if selected == nil {
			fail("unsupported_algorithm")
		}
		a := *selected
		data := req.Data
		switch req.Input {
		case "message":
			if a.hash != 0 {
				h := a.hash.New()
				_, _ = h.Write(data)
				data = h.Sum(nil)
			}
		case "digest":
			if *messageOnly || a.hash == 0 || len(data) != a.hash.Size() {
				fail("unsupported_algorithm")
			}
		default:
			fail("invalid_request")
		}
		opts := crypto.SignerOpts(a.hash)
		if a.pss {
			if req.PSSSaltLength == nil || *req.PSSSaltLength != a.hash.Size() {
				fail("unsupported_algorithm")
			}
			opts = &rsa.PSSOptions{Hash: a.hash, SaltLength: *req.PSSSaltLength}
		} else if req.PSSSaltLength != nil {
			fail("invalid_request")
		}
		out.Signature, err = key.Sign(rand.Reader, data, opts)
		if err != nil {
			fail("signing_denied")
		}
	default:
		fail("invalid_request")
	}
	respond(out)
}

func supportedAlgorithms(public crypto.PublicKey) []algorithm {
	switch key := public.(type) {
	case *ecdsa.PublicKey:
		switch key.Curve.Params().BitSize {
		case 256:
			return []algorithm{{"ecdsa_secp256r1_sha256", crypto.SHA256, false}}
		case 384:
			return []algorithm{{"ecdsa_secp384r1_sha384", crypto.SHA384, false}}
		case 521:
			return []algorithm{{"ecdsa_secp521r1_sha512", crypto.SHA512, false}}
		}
	case *rsa.PublicKey:
		if key.N.BitLen() < 2048 || key.N.BitLen() > 8192 {
			return nil
		}
		return []algorithm{
			{"rsa_pss_rsae_sha256", crypto.SHA256, true},
			{"rsa_pss_rsae_sha384", crypto.SHA384, true},
			{"rsa_pss_rsae_sha512", crypto.SHA512, true},
			{"rsa_pkcs1_sha256", crypto.SHA256, false},
			{"rsa_pkcs1_sha384", crypto.SHA384, false},
			{"rsa_pkcs1_sha512", crypto.SHA512, false},
		}
	case ed25519.PublicKey:
		return []algorithm{{"ed25519", 0, false}}
	}
	return nil
}

func respond(out response) {
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		os.Exit(1)
	}
}

func fail(code string) {
	respond(response{Version: 1, Error: code})
	os.Exit(0)
}

// See LICENSE file in the project root for license information.

package mtlsexec

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
	"github.com/rstreamlabs/rstream-go/internal/handshakectx"
)

type Options struct {
	Command           string
	Args              []string
	Timeout           string
	MaxConcurrency    int
	PassEnv           []string
	CertificateSHA256 []byte
}

// Provider caches one enrolled public identity. Only the per-handshake signer
// carries a handshake context; copies of tls.Config share the concurrency gate.
type Provider struct {
	options     Options
	timeout     time.Duration
	fingerprint string
	gate        chan struct{}
	loading     chan struct{}
	mu          sync.RWMutex
	identity    *identity
}

type identity struct {
	certificate tls.Certificate
	inputs      map[string][]string
}

func New(options Options) (*Provider, error) {
	if err := fipsprofile.Unavailable("external mTLS signer"); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(options.Command) || strings.ContainsRune(options.Command, 0) {
		return nil, errors.New("exec mTLS command must be an absolute executable path")
	}
	if len(options.CertificateSHA256) != sha256.Size {
		return nil, errors.New("exec mTLS certificateSHA256 must be a SHA-256 digest")
	}
	timeout := 5 * time.Second
	if options.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(options.Timeout)
		if err != nil || timeout <= 0 || timeout > time.Minute {
			return nil, errors.New("exec mTLS timeout must be a duration greater than zero and at most 1m")
		}
	}
	if options.MaxConcurrency < 0 || options.MaxConcurrency > 32 {
		return nil, errors.New("exec mTLS maxConcurrency must be between 1 and 32 (zero selects 1)")
	}
	if options.MaxConcurrency == 0 {
		options.MaxConcurrency = 1
	}
	if len(options.Args) > 128 || len(options.PassEnv) > 128 {
		return nil, errors.New("exec mTLS has too many arguments or environment entries")
	}
	size := 0
	for _, arg := range options.Args {
		size += len(arg)
		if strings.ContainsRune(arg, 0) || size > 64<<10 {
			return nil, errors.New("exec mTLS arguments are invalid or too large")
		}
	}
	for _, name := range options.PassEnv {
		if !validEnvName(name) {
			return nil, errors.New("exec mTLS passEnv contains an invalid variable name")
		}
	}
	options.Args = slices.Clone(options.Args)
	options.PassEnv = slices.Clone(options.PassEnv)
	options.CertificateSHA256 = bytes.Clone(options.CertificateSHA256)
	return &Provider{options: options, timeout: timeout, fingerprint: hex.EncodeToString(options.CertificateSHA256), gate: make(chan struct{}, options.MaxConcurrency), loading: make(chan struct{}, 1)}, nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (p *Provider) GetClientCertificate(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	if request == nil || request.Version < tls.VersionTLS12 {
		return nil, errors.New("external mTLS signer requires TLS 1.2 or later")
	}
	ctx := request.Context()
	if ctx == nil {
		return nil, errors.New("external mTLS signer requires a handshake context")
	}
	ctx = handshakectx.ForSigner(ctx)
	id, err := p.loadIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if err := validLeaf(id.certificate.Leaf); err != nil {
		return nil, err
	}
	cert := id.certificate
	cert.PrivateKey = &signer{provider: p, identity: id, ctx: ctx}
	if err := request.SupportsCertificate(&cert); err != nil {
		return nil, errors.New("external mTLS identity is incompatible with the server certificate request")
	}
	return &cert, nil
}

func (p *Provider) cachedIdentity() *identity {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.identity
}

// Check validates the identity and possession of its key for every advertised
// algorithm. It is an explicit diagnostic operation, independent of TLS.
func (p *Provider) Check(ctx context.Context) (*x509.Certificate, error) {
	id, err := p.loadIdentity(ctx)
	if err != nil {
		return nil, err
	}
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return nil, err
	}
	s := &signer{provider: p, identity: id, ctx: ctx}
	for _, a := range algorithms {
		if _, ok := id.inputs[a.name]; !ok {
			continue
		}
		opts := crypto.SignerOpts(a.hash)
		if a.pss {
			opts = &rsa.PSSOptions{Hash: a.hash, SaltLength: rsa.PSSSaltLengthEqualsHash}
		}
		if _, err := s.SignMessage(rand.Reader, challenge, opts); err != nil {
			return nil, err
		}
	}
	return x509.ParseCertificate(bytes.Clone(id.certificate.Leaf.Raw))
}

func (p *Provider) loadIdentity(parent context.Context) (*identity, error) {
	if id := p.cachedIdentity(); id != nil {
		return id, nil
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	select {
	case p.loading <- struct{}{}:
		defer func() { <-p.loading }()
	case <-ctx.Done():
		return nil, fmt.Errorf("external mTLS identity: %w", ctx.Err())
	}
	if id := p.cachedIdentity(); id != nil {
		return id, nil
	}
	response, err := p.run(ctx, Request{Operation: "identity"})
	if err != nil {
		return nil, err
	}
	id, err := p.parseIdentity(response)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.identity = id
	p.mu.Unlock()
	return id, nil
}

func (p *Provider) parseIdentity(response Response) (*identity, error) {
	if len(response.Signature) != 0 || len(response.Capabilities) == 0 || len(response.Capabilities) > len(algorithms) {
		return nil, errors.New("external mTLS identity has invalid capabilities or unexpected signature")
	}
	var chain [][]byte
	var leaf *x509.Certificate
	rest := []byte(response.CertificateChain)
	for len(bytes.TrimSpace(rest)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(rest), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("external mTLS identity must contain only PEM certificates")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(chain) >= 16 {
			return nil, errors.New("external mTLS identity has an invalid certificate chain")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("external mTLS identity has an invalid certificate")
		}
		if leaf == nil {
			leaf = cert
		}
		chain = append(chain, block.Bytes)
		rest = remaining
	}
	if leaf == nil {
		return nil, errors.New("external mTLS identity has no certificate")
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	if !bytes.Equal(fingerprint[:], p.options.CertificateSHA256) {
		return nil, errors.New("external mTLS certificate differs from the enrolled certificateSHA256")
	}
	if err := validLeaf(leaf); err != nil {
		return nil, err
	}
	id := &identity{certificate: tls.Certificate{Certificate: chain, Leaf: leaf}, inputs: make(map[string][]string)}
	for _, capability := range response.Capabilities {
		a, ok := findAlgorithm(capability.Algorithm)
		if !ok || !a.supports(leaf.PublicKey) {
			return nil, errors.New("external mTLS identity advertises an unsupported or incompatible algorithm")
		}
		if _, duplicate := id.inputs[a.name]; duplicate || len(capability.Inputs) == 0 || len(capability.Inputs) > 2 {
			return nil, errors.New("external mTLS identity has invalid or duplicate capabilities")
		}
		for i, input := range capability.Inputs {
			if (input != "message" && input != "digest") || (a.hash == 0 && input != "message") || slices.Contains(capability.Inputs[:i], input) {
				return nil, errors.New("external mTLS identity has an invalid signing input mode")
			}
		}
		id.inputs[a.name] = capability.Inputs
		id.certificate.SupportedSignatureAlgorithms = append(id.certificate.SupportedSignatureAlgorithms, a.scheme)
	}
	return id, nil
}

func validLeaf(leaf *x509.Certificate) error {
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return errors.New("external mTLS certificate is not currently valid")
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return errors.New("external mTLS certificate does not allow digital signatures")
	}
	if (len(leaf.ExtKeyUsage) > 0 || len(leaf.UnknownExtKeyUsage) > 0) && !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) && !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageAny) {
		return errors.New("external mTLS certificate does not allow client authentication")
	}
	return nil
}

type signer struct {
	provider *Provider
	identity *identity
	ctx      context.Context
}

var _ crypto.MessageSigner = (*signer)(nil)

func (s *signer) Public() crypto.PublicKey {
	// Return a fresh public key; callers must not mutate the verification key.
	cert, err := x509.ParseCertificate(bytes.Clone(s.identity.certificate.Leaf.Raw))
	if err != nil {
		panic("mtlsexec: validated certificate became invalid")
	}
	return cert.PublicKey
}

func (s *signer) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	return s.sign(digest, opts, false)
}

func (s *signer) SignMessage(_ io.Reader, message []byte, opts crypto.SignerOpts) ([]byte, error) {
	return s.sign(message, opts, true)
}

func (s *signer) sign(data []byte, opts crypto.SignerOpts, message bool) ([]byte, error) {
	if len(data) > MaxDataBytes {
		return nil, errors.New("external mTLS signing input exceeds limit")
	}
	a, err := selectAlgorithm(s.identity.certificate.Leaf.PublicKey, opts)
	if err != nil {
		return nil, err
	}
	inputs := s.identity.inputs[a.name]
	digest := data
	if message {
		digest = a.digest(data)
	}
	input := "digest"
	if message || a.hash == 0 {
		input = "message"
		if !slices.Contains(inputs, input) {
			data, input = digest, "digest"
		}
	} else if len(data) != a.hash.Size() {
		return nil, errors.New("external mTLS digest has the wrong length")
	}
	if !slices.Contains(inputs, input) {
		return nil, errors.New("external mTLS signer does not support the requested algorithm/input mode")
	}
	request := Request{Operation: "sign", Algorithm: a.name, Input: input, Data: data}
	if a.pss {
		size := a.hash.Size()
		request.PSSSaltLength = &size
	}
	response, err := s.provider.run(s.ctx, request)
	if err != nil {
		return nil, err
	}
	if response.CertificateChain != "" || len(response.Capabilities) != 0 || len(response.Signature) == 0 || len(response.Signature) > 1024 {
		return nil, errors.New("external mTLS signer returned an invalid signature response")
	}
	if !a.verify(s.identity.certificate.Leaf.PublicKey, digest, response.Signature) {
		return nil, errors.New("external mTLS signature does not verify against the enrolled certificate")
	}
	return response.Signature, nil
}

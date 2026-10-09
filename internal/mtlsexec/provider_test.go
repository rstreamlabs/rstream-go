// See LICENSE file in the project root for license information.

//go:build !rstream_fips

package mtlsexec

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	rstream "github.com/rstreamlabs/rstream-go"
)

// TestExecHelperProcess runs in a separate process with the environment selected
// by the provider, never in the parent test process.
func TestExecHelperProcess(t *testing.T) {
	i := slices.Index(os.Args, "--")
	if i < 0 {
		return
	}
	args := os.Args[i+1:]
	if len(args) != 4 {
		os.Exit(90)
	}
	cert, err := tls.LoadX509KeyPair(args[0], args[1])
	if err != nil {
		os.Exit(91)
	}
	var request Request
	if err := json.NewDecoder(io.LimitReader(os.Stdin, MaxRequestBytes)).Decode(&request); err != nil {
		os.Exit(92)
	}
	mode, events := args[2], args[3]
	if events != "" {
		f, err := os.OpenFile(events, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(93)
		}
		_, _ = fmt.Fprintf(f, "%s %d\n", request.Operation, os.Getpid())
		_ = f.Close()
	}
	if os.Getenv("MTLS_UNPASSED") != "" || (mode == "env" && os.Getenv("MTLS_PASSED") != "expected") {
		os.Exit(94)
	}
	if mode == "sleep" || (mode == "sign-sleep" && request.Operation == "sign") {
		time.Sleep(time.Minute)
		os.Exit(95)
	}
	switch mode {
	case "exit":
		fmt.Fprint(os.Stderr, "SECRET-VENDOR-DIAGNOSTIC")
		os.Exit(42)
	case "stdout":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), MaxResponseBytes+1))
		os.Exit(0)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), 4097))
		os.Exit(0)
	case "malformed":
		fmt.Fprint(os.Stdout, "{broken")
		os.Exit(0)
	case "trailing":
		fmt.Fprint(os.Stdout, `{"version":1}{}`)
		os.Exit(0)
	case "version":
		fmt.Fprint(os.Stdout, `{"version":2}`)
		os.Exit(0)
	case "denied":
		fmt.Fprint(os.Stdout, `{"version":1,"error":"signing_denied"}`)
		os.Exit(0)
	}
	response := Response{Version: Version}
	if request.Operation == "identity" {
		response.CertificateChain = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
		key := cert.PrivateKey.(crypto.Signer)
		for _, a := range algorithms {
			if a.supports(key.Public()) {
				input := "message"
				if mode == "digest" && a.hash != 0 {
					input = "digest"
				}
				response.Capabilities = append(response.Capabilities, Capability{Algorithm: a.name, Inputs: []string{input}})
			}
		}
		if mode == "bad-algorithm" {
			response.Capabilities[0].Algorithm = "unknown"
		}
		if mode == "bad-cert" {
			response.CertificateChain = "invalid PEM"
		}
	} else {
		a, ok := findAlgorithm(request.Algorithm)
		if !ok {
			os.Exit(96)
		}
		digest := request.Data
		if request.Input == "message" {
			digest = a.digest(digest)
		}
		if mode == "double-hash" {
			digest = a.digest(digest)
		}
		opts := crypto.SignerOpts(a.hash)
		if a.pss {
			if request.PSSSaltLength == nil || *request.PSSSaltLength != a.hash.Size() {
				os.Exit(97)
			}
			opts = &rsa.PSSOptions{Hash: a.hash, SaltLength: *request.PSSSaltLength}
		}
		key := cert.PrivateKey.(crypto.Signer)
		if mode == "wrong-key" {
			key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		}
		response.Signature, err = key.Sign(rand.Reader, digest, opts)
		if err != nil {
			os.Exit(98)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(99)
	}
	os.Exit(0)
}

type fixture struct {
	cert     tls.Certificate
	certPath string
	keyPath  string
	options  Options
}

func newFixture(t testing.TB, key crypto.Signer) fixture {
	t.Helper()
	if key == nil {
		var err error
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "exec fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(der)
	return fixture{cert: cert, certPath: certPath, keyPath: keyPath, options: Options{Command: executable, Args: []string{"-test.run=^TestExecHelperProcess$", "--", certPath, keyPath, "message", ""}, CertificateSHA256: fingerprint[:]}}
}

func (f fixture) provider(t testing.TB, mode string) *Provider {
	t.Helper()
	opts := f.options
	opts.Args = slices.Clone(opts.Args)
	opts.Args[4] = mode
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecSigningAlgorithms(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]crypto.Signer{"rsa": rsaKey, "ed25519": edKey}
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keys[curve.Params().Name] = key
	}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, key)
			for _, mode := range []string{"message", "digest"} {
				t.Run(mode, func(t *testing.T) {
					p := f.provider(t, mode)
					id, err := p.loadIdentity(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					s := &signer{provider: p, identity: id, ctx: t.Context()}
					for _, a := range algorithms {
						if !a.supports(key.Public()) {
							continue
						}
						opts := crypto.SignerOpts(a.hash)
						if a.pss {
							opts = &rsa.PSSOptions{Hash: a.hash, SaltLength: rsa.PSSSaltLengthEqualsHash}
						}
						message := []byte("one hash, a real signature")
						signature, err := crypto.SignMessage(s, rand.Reader, message, opts)
						if err != nil || !a.verify(key.Public(), a.digest(message), signature) {
							t.Fatalf("%s: signature failed: %v", a.name, err)
						}
						_, err = s.Sign(rand.Reader, a.digest(message), opts)
						if mode == "message" && a.hash != 0 {
							if err == nil {
								t.Fatal("message-only signer accepted a prehashed digest")
							}
						} else if err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestExecFailuresAndLimits(t *testing.T) {
	f := newFixture(t, nil)
	for mode, want := range map[string]string{"exit": "status 42", "stdout": "output exceeds", "stderr": "output exceeds", "malformed": "invalid JSON", "trailing": "trailing data", "version": "unsupported protocol version", "denied": "signing_denied", "bad-cert": "only PEM", "bad-algorithm": "unsupported or incompatible"} {
		t.Run(mode, func(t *testing.T) {
			_, err := f.provider(t, mode).loadIdentity(t.Context())
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("error = %v, want %q without provider diagnostics", err, want)
			}
		})
	}
	for _, mode := range []string{"double-hash", "wrong-key"} {
		t.Run(mode, func(t *testing.T) {
			p := f.provider(t, mode)
			id, err := p.loadIdentity(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			s := &signer{provider: p, identity: id, ctx: t.Context()}
			if _, err := s.SignMessage(rand.Reader, []byte("message"), crypto.SHA256); err == nil || !strings.Contains(err.Error(), "does not verify") {
				t.Fatalf("invalid signature accepted: %v", err)
			}
			if _, err := s.SignMessage(rand.Reader, make([]byte, MaxDataBytes+1), crypto.SHA256); err == nil {
				t.Fatal("oversized message accepted")
			}
			if _, err := s.SignMessage(rand.Reader, []byte("message"), crypto.SHA1); err == nil {
				t.Fatal("unsupported algorithm accepted")
			}
		})
	}
	p := f.provider(t, "message")
	p.options.CertificateSHA256[0] ^= 1
	if _, err := p.loadIdentity(t.Context()); err == nil || !strings.Contains(err.Error(), "enrolled") {
		t.Fatalf("certificate pin mismatch accepted: %v", err)
	}
	p = f.provider(t, "message")
	p.options.Command = filepath.Join(t.TempDir(), "missing")
	if _, err := p.loadIdentity(t.Context()); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal(err)
	}
}

func TestExecEnvironmentAndCaching(t *testing.T) {
	t.Setenv("MTLS_UNPASSED", "SECRET")
	t.Setenv("MTLS_PASSED", "expected")
	f := newFixture(t, nil)
	f.options.PassEnv = []string{"MTLS_PASSED"}
	events := filepath.Join(t.TempDir(), "events")
	f.options.Args[5] = events
	p := f.provider(t, "env")
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := p.loadIdentity(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	data, err := os.ReadFile(events)
	if err != nil || strings.Count(string(data), "identity") != 1 {
		t.Fatalf("expected exactly one identity process, events=%q error=%v", data, err)
	}
}

func waitEvent(t *testing.T, path, operation string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), operation) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper never started %s", operation)
}

func TestExecCancellationAndConcurrencyBound(t *testing.T) {
	f := newFixture(t, nil)
	events := filepath.Join(t.TempDir(), "events")
	f.options.Args[5] = events
	p := f.provider(t, "sleep")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := p.run(ctx, Request{Operation: "identity"}); done <- err }()
	waitEvent(t, events, "identity")
	queueCtx, cancelQueue := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancelQueue()
	if _, err := p.run(queueCtx, Request{Operation: "identity"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued call did not respect deadline: %v", err)
	}
	data, _ := os.ReadFile(events)
	if strings.Count(string(data), "identity") != 1 {
		t.Fatalf("concurrency exceeded one process: %q", data)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("helper cancellation did not finish")
	}
	p.options.Args[4] = "message"
	if _, err := p.loadIdentity(t.Context()); err != nil {
		t.Fatalf("cancellation leaked a gate or poisoned the provider: %v", err)
	}
	p = f.provider(t, "sleep")
	p.timeout = 100 * time.Millisecond
	if _, err := p.loadIdentity(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("operation timeout: %v", err)
	}
}

func tlsConfigs(t *testing.T, f fixture, mode string) (*tls.Config, *tls.Config) {
	t.Helper()
	p := f.provider(t, mode)
	roots := x509.NewCertPool()
	roots.AddCert(f.cert.Leaf)
	server := &tls.Config{Certificates: []tls.Certificate{f.cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, NextProtos: []string{"mtls-test", "rstrm/1"}}
	client := &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12, GetClientCertificate: p.GetClientCertificate, NextProtos: []string{"mtls-test"}}
	return client, server
}

func TestExecRealTLSAndQUIC(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]crypto.Signer{"rsa": rsaKey, "ed25519": edKey}
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keys[curve.Params().Name] = key
	}
	for name, key := range keys {
		f := newFixture(t, key)
		for _, mode := range []string{"message", "digest"} {
			for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
				t.Run(fmt.Sprintf("%s/%s/TLS%x", name, mode, version), func(t *testing.T) {
					client, server := tlsConfigs(t, f, mode)
					client.MinVersion, client.MaxVersion = version, version
					server.MinVersion, server.MaxVersion = version, version
					ln, err := tls.Listen("tcp", "127.0.0.1:0", server)
					if err != nil {
						t.Fatal(err)
					}
					defer ln.Close()
					done := make(chan error, 1)
					go func() {
						for range 2 {
							conn, err := ln.Accept()
							if err != nil {
								done <- err
								return
							}
							_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
							_, err = conn.Write([]byte("ok"))
							_ = conn.Close()
							if err != nil {
								done <- err
								return
							}
						}
						done <- nil
					}()
					for range 2 {
						dialer := &tls.Dialer{Config: client}
						conn, err := dialer.DialContext(t.Context(), "tcp", ln.Addr().String())
						if err != nil {
							t.Fatal(err)
						}
						data, err := io.ReadAll(conn)
						_ = conn.Close()
						if err != nil || string(data) != "ok" {
							t.Fatalf("exchange: %q, %v", data, err)
						}
					}
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
	f := newFixture(t, nil)
	client, server := tlsConfigs(t, f, "message")
	server.MinVersion = tls.VersionTLS13
	ln, err := quic.ListenAddr("127.0.0.1:0", server, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept(t.Context())
		if err != nil {
			done <- err
			return
		}
		defer conn.CloseWithError(0, "")
		for range 2 {
			stream, err := conn.AcceptStream(t.Context())
			if err != nil {
				done <- err
				return
			}
			buffer := make([]byte, 2)
			_, err = io.ReadFull(stream, buffer)
			if err == nil {
				_, err = stream.Write(buffer)
			}
			_ = stream.Close()
			if err != nil {
				done <- err
				return
			}
		}
		<-conn.Context().Done()
		done <- nil
	}()
	transport := &rstream.QUICTransport{}
	defer transport.Close()
	for range 2 {
		conn, err := transport.Dial(t.Context(), ln.Addr().String(), client)
		if err != nil {
			t.Fatal(err)
		}
		_, err = conn.Write([]byte("ok"))
		if err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 2)
		_, err = io.ReadFull(conn, data)
		_ = conn.Close()
		if err != nil || string(data) != "ok" {
			t.Fatalf("QUIC exchange: %q, %v", data, err)
		}
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExecClientCloseCancelsSigning(t *testing.T) {
	f := newFixture(t, nil)
	events := filepath.Join(t.TempDir(), "events")
	f.options.Args[5] = events
	clientTLS, serverTLS := tlsConfigs(t, f, "sign-sleep")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err == nil {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.Read(make([]byte, 1))
		}
	}()
	client, err := rstream.NewClient(rstream.ClientOptions{Engine: ln.Addr().String(), TLSClientConfig: clientTLS, Transport: &rstream.Transport{}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := client.Connect(t.Context(), nil); done <- err }()
	waitEvent(t, events, "sign ")
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed client connected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Client.Close did not cancel the helper")
	}
	<-serverDone
}

func TestExecQUICClientCloseCancelsSigning(t *testing.T) {
	f := newFixture(t, nil)
	events := filepath.Join(t.TempDir(), "events")
	f.options.Args[5] = events
	clientTLS, serverTLS := tlsConfigs(t, f, "sign-sleep")
	ln, err := quic.ListenAddr("127.0.0.1:0", serverTLS, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := rstream.NewClient(rstream.ClientOptions{Engine: ln.Addr().String(), TLSClientConfig: clientTLS, Transport: &rstream.QUICTransport{}, OwnTransport: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan error, 1)
	go func() { _, err := client.Connect(t.Context(), nil); done <- err }()
	waitEvent(t, events, "sign ")
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed QUIC client connected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Client.Close did not cancel the QUIC helper")
	}
}

func TestExecOptionsValidation(t *testing.T) {
	base := newFixture(t, nil).options
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Command = "relative-helper" },
		func(o *Options) { o.Timeout = "0s" },
		func(o *Options) { o.Timeout = "2m" },
		func(o *Options) { o.MaxConcurrency = -1 },
		func(o *Options) { o.MaxConcurrency = 33 },
		func(o *Options) { o.PassEnv = []string{"BAD=VALUE"} },
		func(o *Options) { o.CertificateSHA256, _ = hex.DecodeString("00") },
	} {
		opts := base
		mutate(&opts)
		if _, err := New(opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestExecIdentityValidityAndCapabilities(t *testing.T) {
	f := newFixture(t, nil)
	key := f.cert.PrivateKey.(crypto.Signer)
	for name, change := range map[string]func(*x509.Certificate){
		"expired":        func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) },
		"future":         func(c *x509.Certificate) { c.NotBefore = time.Now().Add(time.Minute) },
		"key-usage":      func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageKeyEncipherment },
		"extended-usage": func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} },
	} {
		t.Run(name, func(t *testing.T) {
			leaf := *f.cert.Leaf
			change(&leaf)
			der, err := x509.CreateCertificate(rand.Reader, &leaf, &leaf, key.Public(), key)
			if err != nil {
				t.Fatal(err)
			}
			p := f.provider(t, "message")
			pin := sha256.Sum256(der)
			p.options.CertificateSHA256 = pin[:]
			response := Response{CertificateChain: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Capabilities: []Capability{{Algorithm: "ecdsa_secp256r1_sha256", Inputs: []string{"message"}}}}
			if _, err := p.parseIdentity(response); err == nil {
				t.Fatal("invalid certificate was accepted")
			}
		})
	}
	p := f.provider(t, "message")
	response := Response{CertificateChain: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.cert.Certificate[0]})), Capabilities: []Capability{{Algorithm: "ecdsa_secp256r1_sha256", Inputs: []string{"message"}}}}
	for _, capabilities := range [][]Capability{
		{{Algorithm: "rsa_pss_rsae_sha256", Inputs: []string{"message"}}},
		{{Algorithm: "ecdsa_secp256r1_sha256", Inputs: []string{"message", "message"}}},
		{{Algorithm: "ecdsa_secp256r1_sha256", Inputs: []string{"raw"}}},
		{response.Capabilities[0], response.Capabilities[0]},
	} {
		response.Capabilities = capabilities
		if _, err := p.parseIdentity(response); err == nil {
			t.Fatal("invalid capabilities were accepted")
		}
	}
}

func BenchmarkExecMessageSigning(b *testing.B) {
	key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	p := newFixture(b, key).provider(b, "message")
	id, err := p.loadIdentity(b.Context())
	if err != nil {
		b.Fatal(err)
	}
	s := &signer{provider: p, identity: id, ctx: b.Context()}
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.SignMessage(rand.Reader, []byte("TLS handshake message"), crypto.SHA512); err != nil {
			b.Fatal(err)
		}
	}
}

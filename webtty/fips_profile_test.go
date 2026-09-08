//go:build rstream_fips

// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

func TestFIPSProfileWebTTYCryptoRoundTrip(t *testing.T) {
	identity, err := GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	if identity.KeyEnvelopeSuite != KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce {
		t.Fatalf("key envelope suite = %d", identity.KeyEnvelopeSuite)
	}
	if len(identity.PublicKey) != E2EP256PublicKeySize || len(identity.PrivateKey) != E2EP256PrivateKeySize {
		t.Fatalf("P-256 key sizes = public %d, private %d", len(identity.PublicKey), len(identity.PrivateKey))
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("fips-webtty-test"),
		Recipients: []E2ERecipient{{
			KeyEnvelopeSuite: identity.KeyEnvelopeSuite,
			KeyID:            identity.KeyID,
			PublicKey:        identity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	grant := clientCrypto.SessionKeyGrant
	if grant.PayloadSuite != PayloadCipherSuiteAES256GCMRandomNonce || grant.KeyEnvelopeSuite != KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce {
		t.Fatalf("session key grant suites = payload %d, envelope %d", grant.PayloadSuite, grant.KeyEnvelopeSuite)
	}
	if len(grant.KeyEnvelopes) != 1 || len(grant.KeyEnvelopes[0].EncapsulatedKey) != E2EP256PublicKeySize {
		t.Fatalf("unexpected P-256 key envelope: %#v", grant.KeyEnvelopes)
	}
	serverCrypto, err := NewE2EServerPayloadCrypto(grant, *identity)
	if err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto() error = %v", err)
	}
	encrypted, err := clientCrypto.EncryptStdin(t.Context(), []byte("fips-webtty"))
	if err != nil {
		t.Fatalf("EncryptStdin() error = %v", err)
	}
	if encrypted.PayloadCrypto == nil || len(encrypted.PayloadCrypto.Nonce) != 0 {
		t.Fatalf("random nonce must be carried inside ciphertext: %#v", encrypted.PayloadCrypto)
	}
	plaintext, err := serverCrypto.DecryptStdin(t.Context(), encrypted)
	if err != nil {
		t.Fatalf("DecryptStdin() error = %v", err)
	}
	if !bytes.Equal(plaintext, []byte("fips-webtty")) {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestFIPSProfileRejectsLegacyWebTTYCryptoAndTransports(t *testing.T) {
	if _, err := GenerateE2EIdentityForSuite(KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("GenerateE2EIdentityForSuite(legacy) error = %v", err)
	}
	if err := validateFIPSWebTTYTransport(WebTTYTransportWebSocket); err == nil || !strings.Contains(err.Error(), "use webtransport") {
		t.Fatalf("validateFIPSWebTTYTransport(websocket) error = %v", err)
	}
	if err := validateFIPSWebTTYTransport(WebTTYTransportWebTransport); err != nil {
		t.Fatalf("validateFIPSWebTTYTransport(webtransport) error = %v", err)
	}
}

func TestFIPSCompatibleWebTTYMutualAuthWebTransportRoundTrip(t *testing.T) {
	testFIPSWebTransportRoundTrip(t, true, true)
}

func TestFIPSCompatibleWebTTYTransportOnlyRoundTrip(t *testing.T) {
	testFIPSWebTransportRoundTrip(t, false, false)
}

func TestFIPSCompatibleWebTTYTransportOnlyCannotBypassServerPolicy(t *testing.T) {
	testFIPSWebTransportRoundTrip(t, true, false)
}

func testFIPSWebTransportRoundTrip(t *testing.T, serverE2E, clientE2E bool) {
	t.Helper()
	required := true
	zero := time.Duration(0)
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("test/fips-webtransport-mutual-auth"),
		Recipients: []E2ERecipient{{
			KeyEnvelopeSuite: serverIdentity.Encryption.KeyEnvelopeSuite,
			KeyID:            serverIdentity.Encryption.KeyID,
			PublicKey:        serverIdentity.Encryption.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	serverConfig := testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
		ServerID: "fips-shell",
	})
	if !serverE2E {
		serverConfig.PayloadCryptoResolver = nil
		serverConfig.RequireSessionKeyGrant = nil
		serverConfig.EndpointIdentity = nil
		serverConfig.RequireClientProof = nil
		serverConfig.AuthorizedClientSigningKeys = nil
	}
	handler := NewWebTTYHandler(serverConfig)
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer packetConn.Close()
	serverTLS, clientTLS := fipsCompatibleWebTransportTLSConfigs(t)
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       serverTLS,
			EnableDatagrams: true,
		},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	tlsStates := make(chan tls.ConnectionState, 1)
	mux.HandleFunc("/webtty", func(w http.ResponseWriter, r *http.Request) {
		tlsStates <- *r.TLS
		session, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		go handler.ServeWebTransportSession(session.Context(), session)
	})
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(packetConn) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = handler.Shutdown(shutdown)
		_ = server.Close()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("WebTransport server error = %v", err)
			}
		case <-shutdown.Done():
			t.Error("WebTransport server did not stop within the shutdown deadline")
		}
	}()
	serverPublic := serverIdentity.Public()
	clientConfig := &SessionConfig{
		URL:                    "https://" + packetConn.LocalAddr().String() + "/webtty",
		Transport:              WebTTYTransportWebTransport,
		TLSConfig:              clientTLS,
		CmdArgs:                testShellCommand("read line; printf \"%s\" \"$line\"", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line)"),
		OpenDeadline:           durationPtr(5 * time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientPrincipalID:      "fips-user",
	}
	if !clientE2E {
		clientConfig.PayloadCrypto = nil
		clientConfig.EndpointIdentity = nil
		clientConfig.ExpectedServerIdentity = nil
	}
	session, err := OpenClientSession(t.Context(), clientConfig)
	if serverE2E && !clientE2E {
		if err == nil {
			_ = session.Close()
			t.Fatal("server accepted a client without its required E2E and identity proof")
		}
		if !strings.Contains(err.Error(), "requires authenticated E2E") {
			t.Fatalf("server policy refusal = %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	state := <-tlsStates
	if state.Version != tls.VersionTLS13 || (state.CipherSuite != tls.TLS_AES_128_GCM_SHA256 && state.CipherSuite != tls.TLS_AES_256_GCM_SHA384) {
		t.Fatalf("WebTransport negotiated version=%x cipher=%x", state.Version, state.CipherSuite)
	}
	if err := session.SendText("fips-webtransport\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "fips-webtransport" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestFIPSProfileWebTTYTransportOnlyRejectsUnsafeTLSBeforeDial(t *testing.T) {
	for _, test := range []struct {
		name string
		tls  tls.Config
		want string
	}{
		{name: "unverified", tls: tls.Config{InsecureSkipVerify: true}, want: "certificate verification"},
		{name: "ech", tls: tls.Config{EncryptedClientHelloConfigList: []byte{1}}, want: "ECH"},
		{name: "minimum", tls: tls.Config{MinVersion: tls.VersionTLS12}, want: "TLS 1.3"},
		{name: "maximum", tls: tls.Config{MaxVersion: tls.VersionTLS12}, want: "TLS 1.3"},
		{name: "curve", tls: tls.Config{CurvePreferences: []tls.CurveID{tls.X25519}}, want: "not approved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, err := OpenClientSession(t.Context(), &SessionConfig{
				URL: "rstrm://fips-fixture", Transport: WebTTYTransportWebTransport, TLSConfig: &test.tls,
				DialPacketContext: func(context.Context, string) (net.PacketConn, net.Addr, error) {
					calls++
					return nil, nil, net.ErrClosed
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) || calls != 0 {
				t.Fatalf("unsafe TLS: error=%v network calls=%d", err, calls)
			}
		})
	}
}

func TestFIPSProfileWebTTYRejectsEitherLegacyEncryptionSuiteBeforeDial(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public := identity.Public()
	for _, test := range []struct {
		name     string
		payload  PayloadCipherSuite
		envelope KeyEnvelopeSuite
	}{
		{name: "payload", payload: PayloadCipherSuiteAES256GCM, envelope: KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce},
		{name: "envelope", payload: PayloadCipherSuiteAES256GCMRandomNonce, envelope: KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, err := OpenClientSession(t.Context(), &SessionConfig{
				URL: "rstrm://fips-fixture", Transport: WebTTYTransportWebTransport,
				EndpointIdentity: identity, ExpectedServerIdentity: &public,
				PayloadCrypto: &PayloadCrypto{SessionKeyGrant: &SessionKeyGrant{PayloadSuite: test.payload, KeyEnvelopeSuite: test.envelope}},
				DialPacketContext: func(context.Context, string) (net.PacketConn, net.Addr, error) {
					calls++
					return nil, nil, net.ErrClosed
				},
			})
			if err == nil || !strings.Contains(err.Error(), "FIPS encryption suites") || calls != 0 {
				t.Fatalf("legacy suite: error=%v network calls=%d", err, calls)
			}
		})
	}
}

func fipsCompatibleWebTransportTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC()
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certificate, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: privateKey}},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		NextProtos:   []string{http3.NextProtoH3},
	}, &tls.Config{
		RootCAs:    roots,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
	}
}

func TestFIPSProfileRejectsFilesystemBackends(t *testing.T) {
	for _, backend := range []string{"webdav", "webrtc"} {
		t.Run(backend, func(t *testing.T) {
			handler, err := NewFileSystemHandler(&FileSystemConfig{Root: t.TempDir(), Backend: backend})
			if handler != nil {
				defer handler.(interface{ Close() error }).Close()
			}
			if err == nil || !strings.Contains(err.Error(), "FIPS profile") {
				t.Fatalf("NewFileSystemHandler() error = %v", err)
			}
		})
	}
}

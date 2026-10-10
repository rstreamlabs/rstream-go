// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type localTURNFixture struct {
	Now             int64
	TTL             int
	Realm           string
	Domain          string
	ProjectEndpoint string
	APP             struct {
		ClientID           string
		ClientSecret       string
		ServerPublicKeyHex string
		Username           string
		Credential         string
	}
	PAT struct {
		Token      string
		Username   string
		Credential string
	}
}

func readLocalTURNFixture(t *testing.T) localTURNFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/local-turn-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture localTURNFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestLocalTURNGoJavaScriptVectors(t *testing.T) {
	fixture := readLocalTURNFixture(t)
	opts := CreateTURNCredentialsOptions{ClientID: fixture.APP.ClientID, ClientSecret: fixture.APP.ClientSecret, ProjectEndpoint: fixture.ProjectEndpoint, TURNDomain: fixture.Domain, TURNRealm: fixture.Realm, TURNServerPublicKeyHex: fixture.APP.ServerPublicKeyHex, TTL: time.Duration(fixture.TTL) * time.Second}
	key, err := turnAppPrivateKey(opts)
	if err != nil {
		t.Fatal(err)
	}
	app, err := deriveAPPTURNCredentials(opts, key, opts.TURNServerPublicKeyHex, opts.TURNDomain, opts.TURNRealm, time.Unix(fixture.Now, 0))
	if err != nil {
		t.Fatal(err)
	}
	if app.Username != fixture.APP.Username || app.Credential != fixture.APP.Credential || app.TTL != fixture.TTL {
		t.Fatal("Go APP derivation differs from the shared vector")
	}
	claims, err := parseTURNTokenClaims(fixture.PAT.Token)
	if err != nil {
		t.Fatal(err)
	}
	pat, err := createPATTURNCredentialsAt(opts, fixture.PAT.Token, claims, time.Unix(fixture.Now, 0))
	if err != nil {
		t.Fatal(err)
	}
	if pat.Username != fixture.PAT.Username || pat.Credential != fixture.PAT.Credential || pat.TTL != fixture.TTL {
		t.Fatal("Go PAT derivation differs from the shared vector")
	}
	if app.URLs[0] != "turn:regional.test:3478?transport=udp" || pat.URLs[0] != app.URLs[0] {
		t.Fatal("relay domain was confused with the signing realm")
	}
}

func TestTURNAppAPITokenIsES512AndNarrowlyScoped(t *testing.T) {
	fixture := readLocalTURNFixture(t)
	opts := CreateTURNCredentialsOptions{ClientID: fixture.APP.ClientID, ClientSecret: fixture.APP.ClientSecret}
	token, err := createTURNAppToken(opts)
	if err != nil {
		t.Fatal(err)
	}
	key, err := turnAppPrivateKey(opts)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 132 {
		t.Fatal("invalid ES512 signature encoding")
	}
	digest := sha512.Sum512([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&key.PublicKey, digest[:], new(big.Int).SetBytes(signature[:66]), new(big.Int).SetBytes(signature[66:])) {
		t.Fatal("invalid ES512 signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Type        string
		ClientID    string
		IAT         int64
		EXP         int64
		Permissions []string
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Type != "app" || claims.ClientID != fixture.APP.ClientID || claims.EXP-claims.IAT != 60 || len(claims.Permissions) != 1 || claims.Permissions[0] != "turn.credentials.create" {
		t.Fatal("APP bearer proof is not bounded to issuance")
	}
}

func TestTURNAppLocalAndManagedSelection(t *testing.T) {
	fixture := readLocalTURNFixture(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("APP API request omitted authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"managed","credential":"secret","ttl":60,"urls":["turn:turn.test:3478"]}`))
	}))
	defer server.Close()
	opts := CreateTURNCredentialsOptions{ClientID: fixture.APP.ClientID, ClientSecret: fixture.APP.ClientSecret, ProjectEndpoint: fixture.ProjectEndpoint, TURNServerPublicKeyHex: fixture.APP.ServerPublicKeyHex, APIURL: server.URL}
	managed, err := CreateTURNCredentials(context.Background(), opts)
	if err != nil || managed.Username != "managed" || calls != 1 {
		t.Fatal("APP did not use managed issuance without local metadata", err)
	}
	opts.TURNDomain, opts.TURNRealm = fixture.Domain, fixture.Realm
	local, err := CreateTURNCredentials(context.Background(), opts)
	if err != nil || !strings.Contains(local.Username, ":app:") || calls != 1 {
		t.Fatal("APP did not derive locally with explicit metadata", err)
	}
	opts.Token = "other-proof"
	if _, err := CreateTURNCredentials(context.Background(), opts); err == nil {
		t.Fatal("mixed identity accepted")
	}
	opts.Token = ""
	opts.TURNServerPublicKeyHex += "zz"
	if _, err := CreateTURNCredentials(context.Background(), opts); err == nil {
		t.Fatal("malformed public key accepted")
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	opts.TURNServerPublicKeyHex = hex.EncodeToString(der)
	if _, err := CreateTURNCredentials(context.Background(), opts); err == nil {
		t.Fatal("mismatched curve accepted")
	}
}

func TestTURNAppManagedFallbackKeepsTheSameShortLivedProof(t *testing.T) {
	fixture := readLocalTURNFixture(t)
	var proof string
	engineCalls := 0
	engine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		engineCalls++
		if r.Header.Get("Authorization") != proof || !strings.HasPrefix(proof, "Bearer ") {
			t.Error("APP fallback changed authentication")
		}
		_, _ = w.Write([]byte(`{"username":"managed","credential":"secret","ttl":60,"urls":["turn:turn.test:3478"]}`))
	}))
	defer engine.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proof = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer control.Close()
	roots := x509.NewCertPool()
	roots.AddCert(engine.Certificate())
	address := strings.TrimPrefix(engine.URL, "https://")
	client := &Client{EngineURL: &address, TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer client.Close()
	result, err := client.CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{ClientID: fixture.APP.ClientID, ClientSecret: fixture.APP.ClientSecret, APIURL: control.URL, ProjectEndpoint: "127"})
	if err != nil || result == nil || result.TTL != 60 || engineCalls != 1 {
		t.Fatal("APP Engine fallback failed", err)
	}
}

func TestTURNAutoTreatsExplicitNullPermissionsAsUnrestricted(t *testing.T) {
	token := turnTestToken(t, map[string]any{"type": "pat", "token_endpoint": "token-1", "exp": time.Now().Add(time.Hour).Unix(), "permissions": nil})
	result, err := CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{Token: token, ProjectEndpoint: "project-1", ClusterDomain: "turn.test", Mode: modePtr(TURNCredentialModeAuto)})
	if err != nil || !strings.Contains(result.Username, ":pat:") {
		t.Fatal("explicit unrestricted permissions were not recognized", err)
	}
}

func TestTURNAppPublicKeyFetchIsBoundedAnonymousAndVerified(t *testing.T) {
	fixture := readLocalTURNFixture(t)
	for _, scenario := range []string{"valid", "oversized", "redirect", "untrusted"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Control-Plane") != "" || len(r.TLS.PeerCertificates) > 0 {
					t.Error("public key retrieval disclosed credentials")
				}
				if r.URL.Path != "/keyrings/turn/global.test.spki.der.hex" {
					t.Error("public key URL did not use the realm")
				}
				switch scenario {
				case "redirect":
					w.Header().Set("Location", "https://invalid.test/key")
					w.WriteHeader(302)
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("a", 8193)))
				default:
					_, _ = w.Write([]byte(fixture.APP.ServerPublicKeyHex))
				}
			}))
			server.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			configured := client.Transport.(*http.Transport)
			configured.TLSClientConfig.Certificates = server.TLS.Certificates
			configured.TLSClientConfig.InsecureSkipVerify = true
			if scenario == "untrusted" {
				configured.TLSClientConfig.RootCAs = nil
			}
			opts := CreateTURNCredentialsOptions{ClientID: fixture.APP.ClientID, ClientSecret: fixture.APP.ClientSecret, ProjectEndpoint: fixture.ProjectEndpoint, TURNDomain: fixture.Domain, TURNRealm: fixture.Realm, TURNKeyringBaseURL: server.URL, HTTPClient: client, ControlPlaneHeaders: map[string]string{"X-Control-Plane": "private"}}
			result, err := CreateTURNCredentials(t.Context(), opts)
			if scenario == "valid" {
				if err != nil || result == nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe keyring response accepted")
			}
		})
	}
}

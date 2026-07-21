// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
)

func modePtr(mode TURNCredentialMode) *TURNCredentialMode {
	return &mode
}

func TestCreateTURNCredentialsAutoUsesPATDerivation(t *testing.T) {
	token := turnTestToken(t, map[string]string{"type": "pat", "token_endpoint": "b95faf7f"})
	res, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		Token:           token,
		ProjectEndpoint: "abc12345",
		ClusterDomain:   "aws-eu-west-3-1.c.rstream.io",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if !strings.HasPrefix(res.Username, "v1:") || !strings.Contains(res.Username, ":pat:abc12345:b95faf7f") {
		t.Fatalf("unexpected username: %s", res.Username)
	}
	if len(res.URLs) != 4 || res.TTL != int(defaultTURNCredentialTTL.Seconds()) {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestCreateTURNCredentialsExplicitPATRejectsAppToken(t *testing.T) {
	token := turnTestToken(t, map[string]string{"type": "app", "clientId": "abc"})
	_, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		Token:           token,
		ProjectEndpoint: "abc12345",
		ClusterDomain:   "aws-eu-west-3-1.c.rstream.io",
		Mode:            modePtr(TURNCredentialModePAT),
	})
	if err == nil || err.Error() != "TURN PAT mode requires a PAT token" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateTURNCredentialsFallsBackToAPIForLegacyPAT(t *testing.T) {
	token := turnTestToken(t, map[string]string{"type": "pat"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/api/projects/tunnels/resolve/abc12345/turn-server/credentials" {
			t.Fatalf("unexpected path: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"u","credential":"c","urls":["turn:example.com:3478?transport=udp"],"ttl":86400}`))
	}))
	defer server.Close()
	res, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		APIURL:          server.URL,
		Token:           token,
		ProjectEndpoint: "abc12345",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestCreateTURNCredentialsExplicitAPIAcceptsOpaqueToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer opaque-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Deployment-Bypass"); got != "secret" {
			t.Fatalf("control plane header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"u","credential":"c","urls":["turn:example.com:3478?transport=udp"],"ttl":86400}`))
	}))
	defer server.Close()
	res, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		APIURL:          server.URL,
		Token:           "opaque-token",
		ProjectEndpoint: "abc12345",
		Mode:            modePtr(TURNCredentialModeAPI),
		ControlPlaneHeaders: map[string]string{
			"X-Deployment-Bypass": "secret",
		},
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestCreateTURNCredentialsAPIModeForwardsExplicitTTL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload controlplane.CreateTURNCredentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.TTLSeconds == nil || *payload.TTLSeconds != 120 {
			t.Fatalf("ttlSeconds = %#v, want 120", payload.TTLSeconds)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"u","credential":"c","urls":["turn:example.com:3478?transport=udp"],"ttl":120}`))
	}))
	defer server.Close()
	res, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		APIURL:          server.URL,
		Token:           "opaque-token",
		ProjectEndpoint: "abc12345",
		TTL:             2 * time.Minute,
		Mode:            modePtr(TURNCredentialModeAPI),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.TTL != 120 {
		t.Fatalf("TTL = %d, want 120", res.TTL)
	}
}

func TestPATTURNCredentialTTLIsBoundedByTokenExpiration(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	expiresAt := now.Add(15 * time.Minute).Unix()
	ttl, err := normalizePATTURNCredentialTTL(24*time.Hour, turnTokenClaims{ExpiresAt: &expiresAt}, now)
	if err != nil {
		t.Fatalf("normalizePATTURNCredentialTTL() error = %v", err)
	}
	if ttl != 15*time.Minute {
		t.Fatalf("ttl = %v, want token remaining lifetime", ttl)
	}
	expiredAt := now.Add(-time.Second).Unix()
	if _, err := normalizePATTURNCredentialTTL(time.Hour, turnTokenClaims{ExpiresAt: &expiredAt}, now); err == nil || err.Error() != "PAT token is expired" {
		t.Fatalf("expired PAT should be rejected, got %v", err)
	}
}

func TestPATTURNCredentialTTLIsCappedByServerMaximum(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	ttl, err := normalizePATTURNCredentialTTL(24*time.Hour, turnTokenClaims{}, now)
	if err != nil {
		t.Fatalf("normalizePATTURNCredentialTTL() error = %v", err)
	}
	if ttl != maxTURNCredentialTTL {
		t.Fatalf("ttl = %v, want server maximum", ttl)
	}
	expiresAt := now.Add(2 * time.Hour).Unix()
	ttl, err = normalizePATTURNCredentialTTL(24*time.Hour, turnTokenClaims{ExpiresAt: &expiresAt}, now)
	if err != nil {
		t.Fatalf("normalizePATTURNCredentialTTL() with PAT exp error = %v", err)
	}
	if ttl != maxTURNCredentialTTL {
		t.Fatalf("ttl = %v, want server maximum before PAT expiration", ttl)
	}
}

func TestCreateTURNCredentialsPATRejectsExpiredToken(t *testing.T) {
	token := turnTestToken(t, map[string]any{
		"type":           "pat",
		"token_endpoint": "b95faf7f",
		"exp":            time.Now().Add(-time.Minute).Unix(),
	})
	_, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		Token:           token,
		ProjectEndpoint: "abc12345",
		ClusterDomain:   "aws-eu-west-3-1.c.rstream.io",
		Mode:            modePtr(TURNCredentialModePAT),
	})
	if err == nil || err.Error() != "PAT token is expired" {
		t.Fatalf("expired PAT should be rejected, got %v", err)
	}
}

func turnTestToken(t *testing.T, claims any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "HS256"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

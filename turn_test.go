// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func modePtr(mode TURNCredentialMode) *TURNCredentialMode {
	return &mode
}

func TestCreateTURNCredentialsAutoUsesPATDerivation(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiJ9.eyJ0eXBlIjoicGF0IiwidG9rZW5fZW5kcG9pbnQiOiJiOTVmYWY3ZiJ9.sig"
	res, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		Token:           token,
		ProjectEndpoint: "bbc44f81",
		ClusterDomain:   "aws-eu-west-3-1.c.rstream.io",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if !strings.HasPrefix(res.Username, "v1:") || !strings.Contains(res.Username, ":pat:bbc44f81:b95faf7f") {
		t.Fatalf("unexpected username: %s", res.Username)
	}
	if len(res.URLs) != 4 || res.TTL != int(defaultTURNCredentialTTL.Seconds()) {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestCreateTURNCredentialsExplicitPATRejectsAppToken(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiJ9.eyJ0eXBlIjoiYXBwIiwiY2xpZW50SWQiOiJhYmMifQ.sig"
	_, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		Token:           token,
		ProjectEndpoint: "bbc44f81",
		ClusterDomain:   "aws-eu-west-3-1.c.rstream.io",
		Mode:            modePtr(TURNCredentialModePAT),
	})
	if err == nil || err.Error() != "TURN PAT mode requires a PAT token" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateTURNCredentialsFallsBackToAPIForLegacyPAT(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiJ9.eyJ0eXBlIjoicGF0In0.sig"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/api/projects/tunnels/resolve/bbc44f81/turn-server/credentials" {
			t.Fatalf("unexpected path: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"u","credential":"c","urls":["turn:example.com:3478?transport=udp"],"ttl":86400}`))
	}))
	defer server.Close()
	res, err := CreateTURNCredentials(context.Background(), CreateTURNCredentialsOptions{
		APIURL:          server.URL,
		Token:           token,
		ProjectEndpoint: "bbc44f81",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" {
		t.Fatalf("unexpected response: %+v", res)
	}
}

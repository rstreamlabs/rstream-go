// See LICENSE file in the project root for license information.

package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestCallerIdentityContract(t *testing.T) {
	// The same versioned vectors live in @rstreamlabs/rstream's SDK tests.
	data, err := os.ReadFile("testdata/caller-identity-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Version int `json:"contractVersion"`
		Cases   []struct {
			Name     string          `json:"name"`
			Valid    bool            `json:"valid"`
			Owner    CredentialOwner `json:"owner"`
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &contract); err != nil || contract.Version != 1 {
		t.Fatalf("identity contract: version=%d error=%v", contract.Version, err)
	}
	for _, vector := range contract.Cases {
		t.Run(vector.Name, func(t *testing.T) {
			var identity CallerIdentity
			err := json.Unmarshal(vector.Response, &identity)
			if (err == nil) != vector.Valid {
				t.Fatalf("identity parse error=%v, want valid=%v", err, vector.Valid)
			}
			if vector.Valid && identity.Owner != vector.Owner {
				t.Fatalf("owner=%+v, want %+v", identity.Owner, vector.Owner)
			}
			if vector.Valid {
				encoded, err := json.Marshal(identity)
				if err != nil {
					t.Fatal(err)
				}
				var decoded CallerIdentity
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatalf("identity round trip failed: %s: %v", encoded, err)
				}
			}
		})
	}
}

func TestIdentityUsesOwnerAwareFormatAndPreservesAuthority(t *testing.T) {
	const payload = `{"owner":{"kind":"workspace","workspaceId":"workspace-1"},"credentialId":"credential-1","permissions":[],"resources":{"tunnels":{"projects":["project-1"]}},"authExpiresAt":"2030-01-01T00:00:00.000Z"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whoami" || r.URL.RawQuery != "format=identity" || r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Deployment-Bypass") != "fixture" {
			t.Errorf("identity request mismatch: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()
	identity, err := NewClient(server.URL, "token", WithHeaders(map[string]string{"X-Deployment-Bypass": "fixture"})).Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if identity.Owner.Kind != "workspace" || identity.Owner.WorkspaceID != "workspace-1" || identity.Owner.UserID != "" || identity.CredentialID != "credential-1" || identity.Role != nil || identity.Email != "" {
		t.Fatalf("unexpected machine identity: %+v", identity)
	}
	if identity.Permissions == nil || len(*identity.Permissions) != 0 || string(identity.Resources) != `{"tunnels":{"projects":["project-1"]}}` || identity.AuthExpiresAt != "2030-01-01T00:00:00.000Z" {
		t.Fatalf("identity lost effective authority: %+v", identity)
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields["permissions"], []any{}) {
		t.Fatalf("empty workspace permissions were omitted: %s", encoded)
	}
}

func TestIdentityAcceptsOlderPersonalServerWithoutChangingWhoami(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"user-1","role":"user","name":"Alice","permissions":[]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	legacy, err := client.Whoami(t.Context())
	if err != nil || legacy.ID != "user-1" || legacy.Role != "user" {
		t.Fatalf("legacy whoami=%+v error=%v", legacy, err)
	}
	identity, err := client.Identity(t.Context())
	if err != nil || identity.Owner.Kind != "user" || identity.Owner.UserID != legacy.ID || identity.Role == nil || *identity.Role != legacy.Role || identity.Name != legacy.Name || identity.Permissions == nil || len(*identity.Permissions) != 0 {
		t.Fatalf("normalized identity=%+v error=%v", identity, err)
	}
}

func TestIdentityPreservesAuthenticationErrorsAndRejectsMalformedMachineResponse(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"owner":{"kind":"workspace","workspaceId":"workspace-1"},"id":"user-1","role":"admin"}`))
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "token").Identity(t.Context())
			if err == nil {
				t.Fatal("expected rejected identity")
			}
			if status == http.StatusUnauthorized && !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("expected unauthorized sentinel, got %v", err)
			}
			if status == http.StatusForbidden && !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected forbidden sentinel, got %v", err)
			}
		})
	}
}

// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestWorkspaceCredentialDeviceProvisioningKeepsTheHumanTransportAndTargetOwnerSeparate(t *testing.T) {
	workspaceID, credentialID := "workspace/with space", "credential/with space"
	prefix := "/api/workspaces/" + url.PathEscape(workspaceID) + "/credentials/" + url.PathEscape(credentialID) + "/devices"
	request := CreateWorkspaceDeviceKeyRequest{Kind: "service", Label: "Camera", PublicEncryptionKey: "encryption", PublicSigningKey: "signing", Fingerprint: "fingerprint", ProofSignature: "signature"}
	lookup := LookupWorkspaceDeviceKeysRequest{Proofs: []WorkspaceDeviceAccessProof{{DeviceFingerprint: "fingerprint", Challenge: "challenge", SignedAt: "2030-01-01T00:00:00Z", Signature: "signature"}}}
	var createdRequest, lookupRequest atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer human-manager" {
			http.Error(w, "expected the existing human-authenticated transport", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case prefix:
			var got CreateWorkspaceDeviceKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, request) {
				t.Errorf("device request = %#v, %v", got, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			createdRequest.Store(true)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(CreateWorkspaceDeviceKeyResponse{DeviceKeyID: "device-id", Status: "pending"})
		case prefix + "/lookup":
			var got LookupWorkspaceDeviceKeysRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, lookup) {
				t.Errorf("lookup request = %#v, %v", got, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			lookupRequest.Store(true)
			_ = json.NewEncoder(w).Encode(LookupWorkspaceDeviceKeysResponse{Devices: []WorkspaceDeviceKey{{ID: "device-id", Kind: "service", CredentialID: credentialID, Status: "active"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "human-manager")
	created, err := client.CreateWorkspaceCredentialDeviceKey(context.Background(), workspaceID, credentialID, request)
	if err != nil || created.DeviceKeyID != "device-id" || created.Status != "pending" {
		t.Fatalf("create = %#v, %v", created, err)
	}
	result, err := client.LookupWorkspaceCredentialDeviceKeys(context.Background(), workspaceID, credentialID, lookup)
	if err != nil || len(result.Devices) != 1 || result.Devices[0].CredentialID != credentialID {
		t.Fatalf("lookup = %#v, %v", result, err)
	}
	if !createdRequest.Load() || !lookupRequest.Load() {
		t.Fatal("both owner-bound operations must use the human-authenticated transport")
	}
}

func TestWorkspaceCredentialDeviceProvisioningPreservesFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"provisioning refused"}`))
			}))
			defer server.Close()
			client := NewClient(server.URL, "human-manager")
			_, createErr := client.CreateWorkspaceCredentialDeviceKey(context.Background(), "workspace", "credential", CreateWorkspaceDeviceKeyRequest{})
			_, lookupErr := client.LookupWorkspaceCredentialDeviceKeys(context.Background(), "workspace", "credential", LookupWorkspaceDeviceKeysRequest{})
			for _, err := range []error{createErr, lookupErr} {
				var responseErr *APIError
				if !errors.As(err, &responseErr) || responseErr.StatusCode != status {
					t.Errorf("error = %v, want HTTP %d", err, status)
				}
			}
		})
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rstreamlabs/rstream-go/controlplane"
)

func TestWorkspaceServiceDeviceEnrollmentSourceIsolation(t *testing.T) {
	clearRstreamTestEnv(t)
	setWorkspaceDeviceTestHome(t, t.TempDir())
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token-a")
	type fixtureDevice struct {
		owner   string
		request controlplane.CreateWorkspaceDeviceKeyRequest
		status  string
	}
	var mu sync.Mutex
	devices := map[string]*fixtureDevice{}
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		owner := map[string]string{"Bearer token-a": "credential-a", "Bearer rotated-a": "credential-a", "Bearer token-b": "credential-b", "Bearer personal": ""}[r.Header.Get("Authorization")]
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/whoami" {
			if r.URL.RawQuery != "format=identity" {
				t.Error("caller identity format was not requested")
			}
			if owner == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "user-1", "role": "user"})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"owner": map[string]string{"kind": "workspace", "workspaceId": "workspace-1"}, "credentialId": owner, "permissions": []string{}})
			}
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/workspaces/workspace-1/enterprise/devices" {
			var request controlplane.CreateWorkspaceDeviceKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			payload := workspaceDeviceProofPayload("workspace-1", request.Kind, request.Label, request.PublicEncryptionKey, request.PublicSigningKey, request.WebTTYPublicKey, request.WebTTYKeyID, request.WebTTYKeyAlgorithm, request.Fingerprint, owner)
			if !verifyWorkspaceDeviceSignature(t, parseWorkspaceDevicePublicKey(t, request.PublicSigningKey), payload, request.ProofSignature) || (owner != "" && request.Kind != "service") {
				t.Error("registration signature did not bind the authenticated owner")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			creates++
			id := fmt.Sprintf("device-%d", creates)
			devices[id] = &fixtureDevice{owner: owner, request: request, status: workspaceDeviceStatusPending}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{DeviceKeyID: id, Status: workspaceDeviceStatusPending})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/workspaces/workspace-1/enterprise/devices/lookup" {
			var request controlplane.LookupWorkspaceDeviceKeysRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			response := controlplane.LookupWorkspaceDeviceKeysResponse{Devices: []controlplane.WorkspaceDeviceKey{}, DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{}}
			for id, device := range devices {
				if device.owner != owner {
					continue
				}
				for _, proof := range request.Proofs {
					if proof.DeviceFingerprint != device.request.Fingerprint {
						continue
					}
					if !verifyWorkspaceDeviceSignature(t, parseWorkspaceDevicePublicKey(t, device.request.PublicSigningKey), workspaceDeviceLookupPayload("workspace-1", proof.DeviceFingerprint, proof.Challenge, proof.SignedAt), proof.Signature) {
						t.Error("lookup proof did not verify")
						w.WriteHeader(http.StatusForbidden)
						return
					}
					response.Devices = append(response.Devices, controlplane.WorkspaceDeviceKey{ID: id, CredentialID: owner, Kind: device.request.Kind, Status: device.status, PublicEncryptionKey: device.request.PublicEncryptionKey, PublicSigningKey: &device.request.PublicSigningKey, Fingerprint: device.request.Fingerprint})
				}
			}
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revoke") {
			targetID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/workspaces/workspace-1/enterprise/devices/"), "/revoke")
			target := devices[targetID]
			var request controlplane.RevokeWorkspaceDeviceKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			actor := devices[request.ActorDeviceKeyID]
			if target == nil || actor == nil || target.owner != owner || actor.owner != owner || actor.status != workspaceDeviceStatusActive || !verifyWorkspaceDeviceSignature(t, parseWorkspaceDevicePublicKey(t, actor.request.PublicSigningKey), workspaceDeviceRevokePayload("workspace-1", request.ActorDeviceKeyID, targetID, workspaceDeviceStatusRevoked, request.Reason), request.Signature) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			target.status = workspaceDeviceStatusRevoked
			_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	enroll := func() map[string]any {
		t.Helper()
		var output bytes.Buffer
		cmd := newTestWorkspaceDeviceCommand(&output)
		cmd.SetContext(t.Context())
		mustSetFlag(t, cmd, "output", "json")
		raw, err := captureStdout(t, func() error { return runWorkspaceDeviceEnroll(cmd) })
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, flags := range [][]string{{"workspace", "foreign-workspace"}, {"kind", "cli"}, {"kind", "agent"}} {
		var output bytes.Buffer
		cmd := newTestWorkspaceDeviceCommand(&output)
		cmd.SetContext(t.Context())
		mustSetFlag(t, cmd, flags[0], flags[1])
		if err := runWorkspaceDeviceEnroll(cmd); err == nil {
			t.Fatalf("workspace token accepted %v", flags)
		}
	}
	first := enroll()
	if first["kind"] != "service" || first["credential_id"] != "credential-a" || first["workspace_source"] != "token" || first["has_key_envelope"] != false {
		t.Fatalf("service enrollment: %+v", first)
	}
	firstPath := first["device_file"].(string)
	firstDevice, err := loadWorkspaceDeviceFile(firstPath)
	if err != nil || firstDevice.Version != workspaceServiceDeviceFileVersion || firstDevice.CredentialID != "credential-a" {
		t.Fatalf("persisted service owner: %+v, %v", firstDevice, err)
	}
	mu.Lock()
	devices[firstDevice.DeviceKeyID].status = workspaceDeviceStatusActive
	mu.Unlock()
	var output bytes.Buffer
	status := newTestWorkspaceDeviceCommand(&output)
	status.SetContext(t.Context())
	if err := runWorkspaceDeviceStatus(status); err != nil {
		t.Fatal(err)
	}
	firstDevice, err = loadWorkspaceDeviceFile(firstPath)
	if err != nil || !workspaceDeviceIsActive(firstDevice) || firstDevice.DeviceEnvelope != nil {
		t.Fatalf("approved certificate without private keys: %+v, %v", firstDevice, err)
	}
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "rotated-a")
	if again := enroll(); again["device_id"] != first["device_id"] || again["already_enrolled"] != true {
		t.Fatalf("secret rotation changed device ownership: %+v", again)
	}
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token-b")
	second := enroll()
	if second["device_id"] == first["device_id"] || second["credential_id"] != "credential-b" {
		t.Fatalf("different token reused another token's device: %+v", second)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			var output bytes.Buffer
			cmd := newTestWorkspaceDeviceCommand(&output)
			cmd.SetContext(t.Context())
			errorsCh <- runWorkspaceDeviceEnroll(cmd)
		})
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if creates != 2 {
		t.Errorf("concurrent enrollment created %d devices, want 2 total", creates)
	}
	devices[second["device_id"].(string)].status = workspaceDeviceStatusActive
	mu.Unlock()
	output.Reset()
	rotate := newTestWorkspaceDeviceCommand(&output)
	rotate.SetContext(t.Context())
	mustSetFlag(t, rotate, "output", "json")
	raw, err := captureStdout(t, func() error { return runWorkspaceDeviceRotate(rotate) })
	if err != nil {
		t.Fatal(err)
	}
	var replacement map[string]any
	if err := json.Unmarshal([]byte(raw), &replacement); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	devices[replacement["device_id"].(string)].status = workspaceDeviceStatusActive
	mu.Unlock()
	if err := runWorkspaceDeviceStatus(status); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if devices[second["device_id"].(string)].status != workspaceDeviceStatusRevoked || devices[firstDevice.DeviceKeyID].status != workspaceDeviceStatusActive {
		t.Error("rotation did not isolate the revoked predecessor to its owning token")
	}
	mu.Unlock()
}

func TestWorkspaceServiceDeviceLookupFailsClosed(t *testing.T) {
	for _, scenario := range []string{"missing", "different owner", "private envelope", "wrong kind", "different key"} {
		t.Run(scenario, func(t *testing.T) {
			material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindService, "Camera")
			if err != nil {
				t.Fatal(err)
			}
			device := material.file
			device.Version = workspaceServiceDeviceFileVersion
			device.CredentialID = "credential-a"
			device.DeviceKeyID = "device-1"
			device.Status = workspaceDeviceStatusActive
			path := filepath.Join(t.TempDir(), "device.json")
			if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
				t.Fatal(err)
			}
			remote := controlplane.WorkspaceDeviceKey{ID: device.DeviceKeyID, CredentialID: device.CredentialID, Kind: device.Kind, Status: device.Status, PublicEncryptionKey: device.PublicEncryptionKey, PublicSigningKey: &device.PublicSigningKey, Fingerprint: device.Fingerprint}
			response := controlplane.LookupWorkspaceDeviceKeysResponse{Devices: []controlplane.WorkspaceDeviceKey{remote}}
			switch scenario {
			case "missing":
				response.Devices = nil
			case "different owner":
				response.Devices[0].CredentialID = "credential-b"
			case "private envelope":
				response.DeviceEnvelopes = []controlplane.WorkspaceKeyEnvelope{{RecipientID: device.DeviceKeyID, RecipientKind: "device"}}
			case "wrong kind":
				response.Devices[0].Kind = "cli"
			case "different key":
				response.Devices[0].PublicEncryptionKey = "other-key"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			_, _, err = refreshWorkspaceDeviceStatusContext(t.Context(), controlplane.NewClient(server.URL, "token"), path, device)
			if err == nil || (scenario == "missing" && !errors.Is(err, errWorkspaceDeviceUnavailable)) {
				t.Fatalf("unsafe lookup accepted: %v", err)
			}
			persisted, err := os.ReadFile(path)
			if err != nil || bytes.Contains(persisted, []byte("other-key")) || bytes.Contains(persisted, []byte("credential-b")) || bytes.Contains(persisted, []byte("device_envelope")) {
				t.Fatalf("rejected lookup modified local key material: %s, %v", persisted, err)
			}
		})
	}
}

func TestWorkspaceServiceDeviceCannotDecryptWorkspaceKeys(t *testing.T) {
	_, err := decryptWorkspaceKeyEnvelope(workspaceDeviceFile{CredentialID: "credential-a", Status: workspaceDeviceStatusActive}, controlplane.WorkspaceKeyEnvelope{})
	if err == nil || !strings.Contains(err.Error(), "cannot decrypt") {
		t.Fatalf("service attempted private-key decryption: %v", err)
	}
}

func TestWorkspaceServiceDeviceProofsUseOnlyTheCurrentOwner(t *testing.T) {
	clearRstreamTestEnv(t)
	setWorkspaceDeviceTestHome(t, t.TempDir())
	for _, credentialID := range []string{"credential-a", "credential-b", ""} {
		material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindService, "Camera")
		if err != nil {
			t.Fatal(err)
		}
		device := material.file
		device.DeviceKeyID = "device-" + credentialID
		device.Status = workspaceDeviceStatusActive
		if credentialID == "" {
			_, _, _, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
			device.DeviceEnvelope = &envelope
		} else {
			device.Version = workspaceServiceDeviceFileVersion
			device.CredentialID = credentialID
		}
		if _, err := writeWorkspaceDeviceFile(device); err != nil {
			t.Fatal(err)
		}
	}
	for _, credentialID := range []string{"credential-a", "credential-b", ""} {
		identity := controlplane.CallerIdentity{Owner: controlplane.CredentialOwner{Kind: "workspace", WorkspaceID: "workspace-1"}, CredentialID: credentialID}
		if credentialID == "" {
			identity.Owner = controlplane.CredentialOwner{Kind: "user", UserID: "user-1"}
		}
		proofs, err := workspaceDeviceAccessProofsWithDevices("workspace-1", 8, identity)
		if err != nil || len(proofs) != 1 || proofs[0].device.CredentialID != credentialID {
			t.Fatalf("proofs for %q: %+v, %v", credentialID, proofs, err)
		}
		proof := proofs[0].proof
		if !verifyWorkspaceDeviceSignature(t, parseWorkspaceDevicePublicKey(t, proofs[0].device.PublicSigningKey), workspaceDeviceLookupPayload("workspace-1", proof.DeviceFingerprint, proof.Challenge, proof.SignedAt), proof.Signature) {
			t.Fatal("source-filtered proof is invalid")
		}
	}
}

func TestWorkspaceServiceDeviceFilesRejectAmbiguousOwnership(t *testing.T) {
	for _, scenario := range []string{"old version with credential", "new version without credential", "human kind with credential", "private envelope"} {
		t.Run(scenario, func(t *testing.T) {
			device := workspaceDeviceFile{Version: workspaceServiceDeviceFileVersion, WorkspaceID: "workspace-1", CredentialID: "credential-a", Kind: workspaceDeviceKindService}
			switch scenario {
			case "old version with credential":
				device.Version = workspaceDeviceFileVersion
			case "new version without credential":
				device.CredentialID = ""
			case "human kind with credential":
				device.Kind = workspaceDeviceKindCLI
			case "private envelope":
				device.DeviceEnvelope = &controlplane.WorkspaceKeyEnvelope{}
			}
			path := filepath.Join(t.TempDir(), "device.json")
			if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
				t.Fatal(err)
			}
			if _, err := loadWorkspaceDeviceFile(path); err == nil {
				t.Fatal("ambiguous service ownership accepted")
			}
		})
	}
}

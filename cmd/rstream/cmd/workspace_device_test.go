// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func newTestWorkspaceDeviceCommand(output *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "device"}
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().String("label", "", "")
	cmd.Flags().String("kind", workspaceDeviceKindCLI, "")
	cmd.Flags().StringP("output", "o", "text", "")
	cmd.SetOut(output)
	return cmd
}

func setWorkspaceDeviceTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestWorkspaceDeviceMaterialSignsKind(t *testing.T) {
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindService, "Audit exporter")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	if material.file.Kind != workspaceDeviceKindService {
		t.Fatalf("device kind = %q, want service", material.file.Kind)
	}
	encryptionPublicKeyBytes := parseWorkspaceDeviceEncryptionPublicKey(t, material.file.PublicEncryptionKey)
	encryptionPrivateKey, err := parseWorkspaceDeviceEncryptionKey(material.file)
	if err != nil {
		t.Fatalf("parseWorkspaceDeviceEncryptionKey() error = %v", err)
	}
	if !bytes.Equal(encryptionPublicKeyBytes, encryptionPrivateKey.PublicKey().Bytes()) {
		t.Fatalf("workspace device encryption public key does not match private key")
	}
	signingKey := parseWorkspaceDevicePublicKey(t, material.file.PublicSigningKey)
	payload := workspaceDeviceProofPayload(
		material.file.WorkspaceID,
		material.file.Kind,
		material.file.Label,
		material.file.PublicEncryptionKey,
		material.file.PublicSigningKey,
		material.file.WebTTYPublicKey,
		material.file.WebTTYKeyID,
		material.file.WebTTYKeyAlgorithm,
		material.file.Fingerprint,
	)
	if !verifyWorkspaceDeviceSignature(t, signingKey, payload, material.proofSignature) {
		t.Fatalf("proof signature did not verify")
	}
	wrongKindPayload := workspaceDeviceProofPayload(
		material.file.WorkspaceID,
		"browser",
		material.file.Label,
		material.file.PublicEncryptionKey,
		material.file.PublicSigningKey,
		material.file.WebTTYPublicKey,
		material.file.WebTTYKeyID,
		material.file.WebTTYKeyAlgorithm,
		material.file.Fingerprint,
	)
	if verifyWorkspaceDeviceSignature(t, signingKey, wrongKindPayload, material.proofSignature) {
		t.Fatalf("proof signature must bind the device kind")
	}
}

func TestWorkspaceDeviceAccessProofsUseOnlyActiveTrustedDevices(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	workspaceID := "workspace-1"
	now := time.Now().UTC().Truncate(time.Second)
	activeMaterial, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Active CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial(active) error = %v", err)
	}
	active := activeMaterial.file
	active.DeviceKeyID = "device-active"
	active.Status = workspaceDeviceStatusActive
	active.CreatedAt = now
	active.UpdatedAt = now
	_, _, _, activeEnvelope := testWorkspaceKeyEnvelopeForDevice(t, active, "keyset-1")
	active.DeviceEnvelope = &activeEnvelope
	if _, err := writeWorkspaceDeviceFile(active); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile(active) error = %v", err)
	}
	revokedMaterial, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Revoked CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial(revoked) error = %v", err)
	}
	revoked := revokedMaterial.file
	revoked.DeviceKeyID = "device-revoked"
	revoked.Status = workspaceDeviceStatusRevoked
	revoked.CreatedAt = now.Add(time.Second)
	revoked.UpdatedAt = revoked.CreatedAt
	_, _, _, revokedEnvelope := testWorkspaceKeyEnvelopeForDevice(t, revoked, "keyset-1")
	revoked.DeviceEnvelope = &revokedEnvelope
	if _, err := writeWorkspaceDeviceFile(revoked); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile(revoked) error = %v", err)
	}
	incompleteMaterial, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Incomplete CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial(incomplete) error = %v", err)
	}
	incomplete := incompleteMaterial.file
	incomplete.DeviceKeyID = "device-incomplete"
	incomplete.Status = workspaceDeviceStatusActive
	incomplete.CreatedAt = now.Add(2 * time.Second)
	incomplete.UpdatedAt = incomplete.CreatedAt
	if _, err := writeWorkspaceDeviceFile(incomplete); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile(incomplete) error = %v", err)
	}
	proofs, err := workspaceDeviceAccessProofsWithDevices(workspaceID, 8)
	if err != nil {
		t.Fatalf("workspaceDeviceAccessProofsWithDevices() error = %v", err)
	}
	if len(proofs) != 1 {
		t.Fatalf("proof count = %d, want 1", len(proofs))
	}
	if proofs[0].device.DeviceKeyID != active.DeviceKeyID || proofs[0].proof.DeviceFingerprint != active.Fingerprint {
		t.Fatalf("unexpected proof source: %#v", proofs[0])
	}
}

func TestWorkspaceSignedAtNowUsesJavaScriptISOStringShape(t *testing.T) {
	value := workspaceSignedAtNow()
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`).MatchString(value) {
		t.Fatalf("workspaceSignedAtNow() = %q, want JavaScript Date.toISOString shape", value)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", value); err != nil {
		t.Fatalf("workspaceSignedAtNow() returned unparsable timestamp %q: %v", value, err)
	}
}

func TestWorkspaceDeviceFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not available on Windows")
	}
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = "pending"
	path := filepath.Join(t.TempDir(), "device.json")
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat device file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("device file mode = %o, want 0600", info.Mode().Perm())
	}
	if _, err := loadWorkspaceDeviceFile(path); err != nil {
		t.Fatalf("loadWorkspaceDeviceFile() error = %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod device file: %v", err)
	}
	if _, err := loadWorkspaceDeviceFile(path); err == nil || !strings.Contains(err.Error(), "must not be readable") {
		t.Fatalf("expected permissive file mode error, got %v", err)
	}
}

func TestRunWorkspaceDeviceEnrollWritesLocalDevice(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	var seen controlplane.CreateWorkspaceDeviceKeyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --workspace is set", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if seen.Kind != workspaceDeviceKindCLI || seen.BrowserID != "" || seen.Label != "Local CLI" {
			http.Error(w, "unexpected device metadata", http.StatusBadRequest)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, seen.PublicSigningKey)
		payload := workspaceDeviceProofPayload(
			"workspace-1",
			seen.Kind,
			seen.Label,
			seen.PublicEncryptionKey,
			seen.PublicSigningKey,
			seen.WebTTYPublicKey,
			seen.WebTTYKeyID,
			seen.WebTTYKeyAlgorithm,
			seen.Fingerprint,
		)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.ProofSignature) {
			http.Error(w, "invalid device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
			DeviceKeyID: "device-1",
			Status:      "pending",
		})
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := cmd.Flags().Set("label", "Local CLI"); err != nil {
		t.Fatalf("set label: %v", err)
	}
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	device, err := loadWorkspaceDeviceFile(path)
	if err != nil {
		t.Fatalf("loadWorkspaceDeviceFile() error = %v", err)
	}
	if device.Kind != workspaceDeviceKindCLI || device.Status != "pending" || device.DeviceKeyID != "device-1" {
		t.Fatalf("unexpected device file: %#v", device)
	}
	identityPath := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "webtty", "identities", "device-1.identity.json")
	if device.WebTTYIdentityPath != identityPath {
		t.Fatalf("WebTTY identity path = %q, want %q", device.WebTTYIdentityPath, identityPath)
	}
	if _, err := loadWorkspaceDeviceWebTTYIdentity(device); err != nil {
		t.Fatalf("loadWorkspaceDeviceWebTTYIdentity() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(identityPath)
		if err != nil {
			t.Fatalf("stat WebTTY identity file: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("WebTTY identity file mode = %o, want 0600", info.Mode().Perm())
		}
	}
	code, err := workspaceDeviceVerificationCode(device)
	if err != nil {
		t.Fatalf("workspaceDeviceVerificationCode() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Workspace device enrollment requested") || !strings.Contains(got, code) || !strings.Contains(got, path) {
		t.Fatalf("unexpected enroll output: %q", got)
	}
}

func TestWorkspaceDeviceVerificationCodeMatchesBrowserFixture(t *testing.T) {
	device := workspaceDeviceFile{
		Kind:                workspaceDeviceKindService,
		WorkspaceID:         "workspace-fixture",
		DeviceKeyID:         "device-fixture",
		PublicEncryptionKey: "enc-fixture",
		PublicSigningKey:    "sig-fixture",
		WebTTYPublicKey:     "webtty-pub-fixture",
		WebTTYKeyID:         "webtty-key-fixture",
		WebTTYKeyAlgorithm:  workspaceDeviceWebTTYCryptoSuite,
		Fingerprint:         "sha256:fingerprint-fixture",
	}
	code, err := workspaceDeviceVerificationCode(device)
	if err != nil {
		t.Fatalf("workspaceDeviceVerificationCode() error = %v", err)
	}
	if code != "3G37-ZQKL-VY46" {
		t.Fatalf("workspaceDeviceVerificationCode() = %q, want %q", code, "3G37-ZQKL-VY46")
	}
}

func TestRunWorkspaceDeviceEnrollReusesPendingLocalDevice(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusPending
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	createCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			var seen controlplane.LookupWorkspaceDeviceKeysRequest
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if len(seen.Proofs) != 1 || seen.Proofs[0].DeviceFingerprint != device.Fingerprint {
				http.Error(w, "unexpected lookup proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  "device-1",
					Kind:                workspaceDeviceKindCLI,
					Status:              workspaceDeviceStatusPending,
					PublicEncryptionKey: device.PublicEncryptionKey,
					PublicSigningKey:    &device.PublicSigningKey,
					Fingerprint:         device.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices":
			createCalled = true
			http.Error(w, "create must not be called", http.StatusBadRequest)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	if createCalled {
		t.Fatalf("enroll created a second local device")
	}
	if got := out.String(); !strings.Contains(got, "already pending") || !strings.Contains(got, "device-1") {
		t.Fatalf("unexpected idempotent enroll output: %q", got)
	}
}

func TestRunWorkspaceDeviceEnrollReusesActiveLocalDevice(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	_, _, _, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	createCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			var seen controlplane.LookupWorkspaceDeviceKeysRequest
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if len(seen.Proofs) != 1 || seen.Proofs[0].DeviceFingerprint != device.Fingerprint {
				http.Error(w, "unexpected lookup proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  "device-1",
					Kind:                workspaceDeviceKindCLI,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: device.PublicEncryptionKey,
					PublicSigningKey:    &device.PublicSigningKey,
					Fingerprint:         device.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}},
				DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{envelope},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices":
			createCalled = true
			http.Error(w, "create must not be called", http.StatusBadRequest)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	if createCalled {
		t.Fatalf("enroll created a second local device")
	}
	if got := out.String(); !strings.Contains(got, "already enrolled") || !strings.Contains(got, "Status: active") || !strings.Contains(got, "device-1") {
		t.Fatalf("unexpected idempotent enroll output: %q", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read device dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("local device file count = %d, want 1", len(entries))
	}
}

func TestRunWorkspaceDeviceRotateKeepsActiveDeviceUntilReplacementIsApproved(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			seen["lookup"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  "device-1",
					Kind:                workspaceDeviceKindCLI,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: device.PublicEncryptionKey,
					PublicSigningKey:    &device.PublicSigningKey,
					Fingerprint:         device.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices":
			seen["create"] = true
			var req controlplane.CreateWorkspaceDeviceKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if req.Fingerprint == device.Fingerprint {
				http.Error(w, "rotated device reused old key material", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
				DeviceKeyID: "device-2",
				Status:      workspaceDeviceStatusPending,
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceRotate(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceRotate() error = %v", err)
	}
	for _, key := range []string{"lookup", "create"} {
		if !seen[key] {
			t.Fatalf("missing %s call", key)
		}
	}
	if seen["revoke"] {
		t.Fatalf("active device was revoked before replacement approval")
	}
	oldDevice, err := loadWorkspaceDeviceFile(path)
	if err != nil {
		t.Fatalf("load old device: %v", err)
	}
	if oldDevice.Status != workspaceDeviceStatusActive {
		t.Fatalf("old device status = %q, want active", oldDevice.Status)
	}
	newPath := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-2.json")
	newDevice, err := loadWorkspaceDeviceFile(newPath)
	if err != nil {
		t.Fatalf("load new device: %v", err)
	}
	if newDevice.Status != workspaceDeviceStatusPending {
		t.Fatalf("new device status = %q, want pending", newDevice.Status)
	}
	if got := out.String(); !strings.Contains(got, "Workspace device rotation requested") || !strings.Contains(got, "device-2") || !strings.Contains(got, "device-1 remains active") {
		t.Fatalf("unexpected rotate output: %q", got)
	}
}

func TestRunWorkspaceDeviceStatusCompletesApprovedRotation(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	oldMaterial, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Old CLI")
	if err != nil {
		t.Fatalf("generate old device: %v", err)
	}
	oldDevice := oldMaterial.file
	oldDevice.DeviceKeyID = "device-1"
	oldDevice.Status = workspaceDeviceStatusActive
	oldDevice.CreatedAt = time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	oldDevice.UpdatedAt = oldDevice.CreatedAt
	oldPath := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if err := writeWorkspaceDeviceFileAt(oldPath, oldDevice); err != nil {
		t.Fatalf("write old device: %v", err)
	}
	newMaterial, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "New CLI")
	if err != nil {
		t.Fatalf("generate replacement device: %v", err)
	}
	newDevice := newMaterial.file
	newDevice.DeviceKeyID = "device-2"
	newDevice.Status = workspaceDeviceStatusActive
	newDevice.RotatesDeviceKeyID = oldDevice.DeviceKeyID
	newDevice.CreatedAt = time.Now().UTC().Truncate(time.Second)
	newDevice.UpdatedAt = newDevice.CreatedAt
	newPath := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-2.json")
	if err := writeWorkspaceDeviceFileAt(newPath, newDevice); err != nil {
		t.Fatalf("write replacement device: %v", err)
	}
	revokeCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			var seen controlplane.LookupWorkspaceDeviceKeysRequest
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if len(seen.Proofs) != 1 {
				http.Error(w, "unexpected proof count", http.StatusBadRequest)
				return
			}
			var device controlplane.WorkspaceDeviceKey
			switch seen.Proofs[0].DeviceFingerprint {
			case oldDevice.Fingerprint:
				device = controlplane.WorkspaceDeviceKey{
					ID:                  oldDevice.DeviceKeyID,
					Kind:                oldDevice.Kind,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: oldDevice.PublicEncryptionKey,
					PublicSigningKey:    &oldDevice.PublicSigningKey,
					Fingerprint:         oldDevice.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}
			case newDevice.Fingerprint:
				device = controlplane.WorkspaceDeviceKey{
					ID:                  newDevice.DeviceKeyID,
					Kind:                newDevice.Kind,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: newDevice.PublicEncryptionKey,
					PublicSigningKey:    &newDevice.PublicSigningKey,
					Fingerprint:         newDevice.Fingerprint,
					CreatedAt:           "2026-06-08T11:00:00.000Z",
				}
			default:
				http.Error(w, "unexpected fingerprint", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{device},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/device-1/revoke":
			revokeCalled = true
			var seen controlplane.RevokeWorkspaceDeviceKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if seen.ActorDeviceKeyID != newDevice.DeviceKeyID {
				http.Error(w, "unexpected actor", http.StatusBadRequest)
				return
			}
			publicKey := parseWorkspaceDevicePublicKey(t, newDevice.PublicSigningKey)
			payload := workspaceDeviceRevokePayload("workspace-1", newDevice.DeviceKeyID, oldDevice.DeviceKeyID, workspaceDeviceStatusRevoked, seen.Reason)
			if !verifyWorkspaceDeviceSignature(t, publicKey, payload, seen.Signature) {
				http.Error(w, "invalid revoke proof", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceStatus(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceStatus() error = %v", err)
	}
	if !revokeCalled {
		t.Fatalf("status did not revoke the replaced device")
	}
	refreshedOld, err := loadWorkspaceDeviceFile(oldPath)
	if err != nil {
		t.Fatalf("load old device: %v", err)
	}
	if refreshedOld.Status != workspaceDeviceStatusRevoked {
		t.Fatalf("old device status = %q, want revoked", refreshedOld.Status)
	}
	refreshedNew, err := loadWorkspaceDeviceFile(newPath)
	if err != nil {
		t.Fatalf("load replacement device: %v", err)
	}
	if refreshedNew.RotationCompletedAt == nil {
		t.Fatalf("replacement rotation was not marked complete")
	}
	if got := out.String(); strings.Contains(got, "device-1") || !strings.Contains(got, "device-2") {
		t.Fatalf("status output should show only the replacement by default, got %q", got)
	}
}

func TestRunWorkspaceDeviceEnrollSupportsServiceKind(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	var seen controlplane.CreateWorkspaceDeviceKeyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if seen.Kind != workspaceDeviceKindService || seen.Label != "Audit exporter" {
			http.Error(w, "unexpected device metadata", http.StatusBadRequest)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, seen.PublicSigningKey)
		payload := workspaceDeviceProofPayload(
			"workspace-1",
			seen.Kind,
			seen.Label,
			seen.PublicEncryptionKey,
			seen.PublicSigningKey,
			seen.WebTTYPublicKey,
			seen.WebTTYKeyID,
			seen.WebTTYKeyAlgorithm,
			seen.Fingerprint,
		)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.ProofSignature) {
			http.Error(w, "invalid device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
			DeviceKeyID: "device-service-1",
			Status:      "pending",
		})
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := cmd.Flags().Set("kind", workspaceDeviceKindService); err != nil {
		t.Fatalf("set kind: %v", err)
	}
	if err := cmd.Flags().Set("label", "Audit exporter"); err != nil {
		t.Fatalf("set label: %v", err)
	}
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-service-1.json")
	device, err := loadWorkspaceDeviceFile(path)
	if err != nil {
		t.Fatalf("loadWorkspaceDeviceFile() error = %v", err)
	}
	if device.Kind != workspaceDeviceKindService || device.Status != "pending" {
		t.Fatalf("unexpected service device file: %#v", device)
	}
	if got := out.String(); !strings.Contains(got, "Kind: service") {
		t.Fatalf("enroll output should expose the selected kind, got %q", got)
	}
}

func TestRunWorkspaceDeviceEnrollRejectsUnsupportedKind(t *testing.T) {
	clearRstreamTestEnv(t)
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := cmd.Flags().Set("kind", "browser"); err != nil {
		t.Fatalf("set kind: %v", err)
	}
	err := runWorkspaceDeviceEnroll(cmd)
	if err == nil || !strings.Contains(err.Error(), "--kind must be one of: cli, agent, service") {
		t.Fatalf("expected unsupported kind error, got %v", err)
	}
}

func TestRunWorkspaceDeviceEnrollUsesDefaultContext(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	var seen controlplane.CreateWorkspaceDeviceKeyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer default-token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, seen.PublicSigningKey)
		payload := workspaceDeviceProofPayload(
			"workspace-1",
			seen.Kind,
			seen.Label,
			seen.PublicEncryptionKey,
			seen.PublicSigningKey,
			seen.WebTTYPublicKey,
			seen.WebTTYKeyID,
			seen.WebTTYKeyAlgorithm,
			seen.Fingerprint,
		)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.ProofSignature) {
			http.Error(w, "invalid device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
			DeviceKeyID: "device-1",
			Status:      "pending",
		})
	}))
	defer server.Close()
	writeWorkspaceDeviceTestConfig(t, server.URL, "global-project")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	if seen.Kind != workspaceDeviceKindCLI {
		t.Fatalf("unexpected device request: %#v", seen)
	}
}

func TestRunWorkspaceDeviceStatusWithWorkspaceUsesDefaultContextWithoutResolvingProject(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusPending
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	if err := writeWorkspaceDeviceFileAt(filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json"), device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --workspace is set", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices/lookup" {
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer default-token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		seen = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
			Devices: []controlplane.WorkspaceDeviceKey{{
				ID:                  "device-1",
				Kind:                workspaceDeviceKindCLI,
				Status:              workspaceDeviceStatusActive,
				PublicEncryptionKey: device.PublicEncryptionKey,
				PublicSigningKey:    &device.PublicSigningKey,
				Fingerprint:         device.Fingerprint,
				CreatedAt:           "2026-06-08T10:00:00.000Z",
			}},
		})
	}))
	defer server.Close()
	writeWorkspaceDeviceTestConfig(t, server.URL, "global-project")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceStatus(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceStatus() error = %v", err)
	}
	if !seen {
		t.Fatalf("workspace device status was not refreshed")
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1") || !strings.Contains(got, "device-1 cli active") || strings.Contains(got, "global-project") {
		t.Fatalf("unexpected status output: %q", got)
	}
}

func TestRunWorkspaceDeviceRotateWithWorkspaceUsesDefaultContextWithoutResolvingProject(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	if err := writeWorkspaceDeviceFileAt(filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json"), device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --workspace is set", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer default-token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			seen["lookup"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  "device-1",
					Kind:                workspaceDeviceKindCLI,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: device.PublicEncryptionKey,
					PublicSigningKey:    &device.PublicSigningKey,
					Fingerprint:         device.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices":
			seen["create"] = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
				DeviceKeyID: "device-2",
				Status:      workspaceDeviceStatusPending,
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	writeWorkspaceDeviceTestConfig(t, server.URL, "global-project")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceRotate(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceRotate() error = %v", err)
	}
	for _, key := range []string{"lookup", "create"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1") || !strings.Contains(got, "device-2") || strings.Contains(got, "global-project") {
		t.Fatalf("unexpected rotate output: %q", got)
	}
}

func TestRunWorkspaceDeviceEnrollWithWorkspaceIgnoresGlobalProjectContext(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	configPath := writeTestGlobalProjectConfig(t)
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --workspace is set", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices" {
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
			return
		}
		seen = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
			DeviceKeyID: "device-1",
			Status:      workspaceDeviceStatusPending,
		})
	}))
	defer server.Close()
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "workspace", "workspace-1")
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	if !seen {
		t.Fatalf("workspace device was not created")
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1") || strings.Contains(got, "global-project") {
		t.Fatalf("unexpected enroll output: %q", got)
	}
}

func TestRunWorkspaceDeviceStatusWithWorkspaceIgnoresGlobalProjectContext(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusPending
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	if err := writeWorkspaceDeviceFileAt(filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json"), device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	configPath := writeTestGlobalProjectConfig(t)
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --workspace is set", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices/lookup" {
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
			return
		}
		seen = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
			Devices: []controlplane.WorkspaceDeviceKey{{
				ID:                  "device-1",
				Kind:                workspaceDeviceKindCLI,
				Status:              workspaceDeviceStatusActive,
				PublicEncryptionKey: device.PublicEncryptionKey,
				PublicSigningKey:    &device.PublicSigningKey,
				Fingerprint:         device.Fingerprint,
				CreatedAt:           "2026-06-08T10:00:00.000Z",
			}},
		})
	}))
	defer server.Close()
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "workspace", "workspace-1")
	if err := runWorkspaceDeviceStatus(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceStatus() error = %v", err)
	}
	if !seen {
		t.Fatalf("workspace device status was not refreshed")
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1") || !strings.Contains(got, "device-1 cli active") || strings.Contains(got, "global-project") {
		t.Fatalf("unexpected status output: %q", got)
	}
}

func TestRunWorkspaceDeviceRotateWithWorkspaceIgnoresGlobalProjectContext(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	if err := writeWorkspaceDeviceFileAt(filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json"), device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	configPath := writeTestGlobalProjectConfig(t)
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --workspace is set", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			seen["lookup"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  "device-1",
					Kind:                workspaceDeviceKindCLI,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: device.PublicEncryptionKey,
					PublicSigningKey:    &device.PublicSigningKey,
					Fingerprint:         device.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices":
			seen["create"] = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
				DeviceKeyID: "device-2",
				Status:      workspaceDeviceStatusPending,
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "workspace", "workspace-1")
	if err := runWorkspaceDeviceRotate(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceRotate() error = %v", err)
	}
	for _, key := range []string{"lookup", "create"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1") || !strings.Contains(got, "device-2") || strings.Contains(got, "global-project") {
		t.Fatalf("unexpected rotate output: %q", got)
	}
}

func TestRunWorkspaceDeviceEnrollInfersWorkspaceFromActiveProject(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	var seen controlplane.CreateWorkspaceDeviceKeyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/resolve/demo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.Project{ID: "project-1", WorkspaceID: "workspace-1", Endpoint: "demo"})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices":
			if got := r.Header.Get("Authorization"); got != "Bearer default-token" {
				http.Error(w, "missing authorization", http.StatusUnauthorized)
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			signingKey := parseWorkspaceDevicePublicKey(t, seen.PublicSigningKey)
			payload := workspaceDeviceProofPayload(
				"workspace-1",
				seen.Kind,
				seen.Label,
				seen.PublicEncryptionKey,
				seen.PublicSigningKey,
				seen.WebTTYPublicKey,
				seen.WebTTYKeyID,
				seen.WebTTYKeyAlgorithm,
				seen.Fingerprint,
			)
			if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.ProofSignature) {
				http.Error(w, "invalid device proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlplane.CreateWorkspaceDeviceKeyResponse{
				DeviceKeyID: "device-1",
				Status:      "pending",
			})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	writeWorkspaceDeviceTestConfig(t, server.URL, "demo")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := runWorkspaceDeviceEnroll(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceEnroll() error = %v", err)
	}
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if _, err := loadWorkspaceDeviceFile(path); err != nil {
		t.Fatalf("loadWorkspaceDeviceFile() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1 (from active project demo)") {
		t.Fatalf("enroll output should describe inferred workspace, got %q", got)
	}
}

func TestRunWorkspaceDeviceEnrollRequiresWorkspaceWithoutActiveProject(t *testing.T) {
	clearRstreamTestEnv(t)
	t.Setenv("RSTREAM_API_URL", "https://api.example.com")
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	err := runWorkspaceDeviceEnroll(cmd)
	if err == nil || !strings.Contains(err.Error(), "rstream workspace list") || !strings.Contains(err.Error(), "rstream project use") {
		t.Fatalf("expected actionable workspace discovery error, got %v", err)
	}
}

func TestWorkspaceDeviceEnrollmentHintPrefersProjectContext(t *testing.T) {
	got := workspaceDeviceEnrollmentHint("workspace-1")
	for _, want := range []string{
		"rstream workspace device enroll from an active project context",
		"rstream workspace device enroll --workspace workspace-1",
		"trusted browser",
		"rstream workspace device status",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("enrollment hint missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "project list") {
		t.Fatalf("enrollment hint should not send users through project JSON output: %s", got)
	}
}

func TestRunWorkspaceDeviceStatusStoresEnvelope(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = "pending"
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	var seen controlplane.LookupWorkspaceDeviceKeysRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/workspaces/workspace-1/enterprise/devices/lookup" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(seen.Proofs) != 1 || seen.Proofs[0].DeviceFingerprint != device.Fingerprint {
			http.Error(w, "unexpected proof", http.StatusBadRequest)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
		payload := workspaceDeviceLookupPayload(
			device.WorkspaceID,
			device.Fingerprint,
			seen.Proofs[0].Challenge,
			seen.Proofs[0].SignedAt,
		)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.Proofs[0].Signature) {
			http.Error(w, "invalid lookup proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
			Devices: []controlplane.WorkspaceDeviceKey{{
				ID:                  "device-1",
				Kind:                workspaceDeviceKindCLI,
				Status:              "active",
				PublicEncryptionKey: device.PublicEncryptionKey,
				PublicSigningKey:    &device.PublicSigningKey,
				Fingerprint:         device.Fingerprint,
				CreatedAt:           "2026-06-08T10:00:00.000Z",
			}},
			DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{{
				ID:            "envelope-1",
				KeysetID:      "keyset-1",
				RecipientKind: "device",
				RecipientID:   "device-1",
				Ciphertext:    "ciphertext",
				Crypto: controlplane.WorkspaceKeyEnvelopeCrypto{
					Suite: workspaceDeviceCryptoSuite,
				},
				CreatedAt: "2026-06-08T10:00:00.000Z",
			}},
		})
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("workspace", "workspace-1"); err != nil {
		t.Fatalf("set workspace: %v", err)
	}
	if err := runWorkspaceDeviceStatus(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceStatus() error = %v", err)
	}
	updated, err := loadWorkspaceDeviceFile(path)
	if err != nil {
		t.Fatalf("loadWorkspaceDeviceFile() error = %v", err)
	}
	if !workspaceDeviceIsActive(updated) {
		t.Fatalf("device should be active and have an envelope: %#v", updated)
	}
	if got := out.String(); !strings.Contains(got, "device-1 cli active") {
		t.Fatalf("unexpected status output: %q", got)
	}
}

func TestRunWorkspaceDeviceStatusInfersWorkspaceFromActiveProject(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = "pending"
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	path := filepath.Join(home, ".rstream", "workspaces", "workspace-1", "devices", "device-1.json")
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFileAt() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/resolve/demo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.Project{ID: "project-1", WorkspaceID: "workspace-1", Endpoint: "demo"})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			var seen controlplane.LookupWorkspaceDeviceKeysRequest
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if len(seen.Proofs) != 1 || seen.Proofs[0].DeviceFingerprint != device.Fingerprint {
				http.Error(w, "unexpected proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  "device-1",
					Kind:                workspaceDeviceKindCLI,
					Status:              "active",
					PublicEncryptionKey: device.PublicEncryptionKey,
					PublicSigningKey:    &device.PublicSigningKey,
					Fingerprint:         device.Fingerprint,
					CreatedAt:           "2026-06-08T10:00:00.000Z",
				}},
				DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{{
					ID:            "envelope-1",
					KeysetID:      "keyset-1",
					RecipientKind: "device",
					RecipientID:   "device-1",
					Ciphertext:    "ciphertext",
					Crypto: controlplane.WorkspaceKeyEnvelopeCrypto{
						Suite: workspaceDeviceCryptoSuite,
					},
					CreatedAt: "2026-06-08T10:00:00.000Z",
				}},
			})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	writeWorkspaceDeviceTestConfig(t, server.URL, "demo")
	var out bytes.Buffer
	cmd := newTestWorkspaceDeviceCommand(&out)
	cmd.SetContext(t.Context())
	if err := runWorkspaceDeviceStatus(cmd); err != nil {
		t.Fatalf("runWorkspaceDeviceStatus() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Workspace: workspace-1 (from active project demo)") || !strings.Contains(got, "device-1 cli active") {
		t.Fatalf("unexpected status output: %q", got)
	}
}

func writeWorkspaceDeviceTestConfig(t *testing.T, apiURL string, projectEndpoint string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := "version: 1\ndefaults:\n  context:\n    name: runtime\ncontexts:\n  - name: runtime\n    apiUrl: " + apiURL + "\n    projectEndpoint: " + projectEndpoint + "\n    auth:\n      token:\n        storage:\n          kind: inline\n          value: default-token\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
}

func parseWorkspaceDevicePublicKey(t *testing.T, encoded string) *ecdsa.PublicKey {
	t.Helper()
	der, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	publicKey, ok := key.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		t.Fatalf("unexpected public key: %T", key)
	}
	return publicKey
}

func parseWorkspaceDeviceEncryptionPublicKey(t *testing.T, encoded string) []byte {
	t.Helper()
	der, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode encryption public key: %v", err)
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatalf("parse encryption public key: %v", err)
	}
	publicKey, ok := key.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		t.Fatalf("unexpected encryption public key: %T", key)
	}
	raw, err := publicKey.Bytes()
	if err != nil {
		t.Fatalf("encode encryption public key: %v", err)
	}
	return raw
}

func verifyWorkspaceDeviceSignature(t *testing.T, key *ecdsa.PublicKey, payload any, signature string) bool {
	t.Helper()
	canonical, err := workspaceCanonicalJSON(payload)
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	digest := sha256.Sum256([]byte(canonical))
	rawSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	return ecdsa.VerifyASN1(key, digest[:], rawSignature)
}

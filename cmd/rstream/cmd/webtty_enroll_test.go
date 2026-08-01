// See LICENSE file in the project root for license information.

package cmd

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

func newTestWebTTYServerEnrollCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "enroll"}
	cmd.Flags().String("project-id", "", "")
	cmd.Flags().String("identity-file", "", "")
	cmd.Flags().String("server-enrollment", "", "")
	cmd.Flags().StringP("output", "o", "text", "")
	return cmd
}

func TestRunWebTTYServerEnrollWritesLocalEnrollment(t *testing.T) {
	projectID := "project-1"
	serverID := "server-1"
	var seen controlplane.EnrollWebTTYServerRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --project-id is set", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      "workspace-1",
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "pending_enrollment",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: "explicit_key",
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
			return
		}
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/enroll" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		publicKey, err := webtty.DecodeE2EKeyMaterial(seen.ServerPublicKey, webtty.E2EX25519PublicKeySize, "server public key")
		signingPublicKey, signingErr := webtty.DecodeE2EKeyMaterial(seen.ServerSigningPublicKey, 0, "server signing public key")
		signingKeyID, keyErr := webtty.DecodeE2EKeyMaterial(seen.ServerSigningKeyID, webtty.WebTTYSigningKeyIDSize, "server signing key id")
		if err != nil || signingErr != nil || keyErr != nil || !strings.EqualFold(seen.ServerFingerprint, webTTYServerPublicKeyFingerprint(publicKey)) || !strings.EqualFold(webtty.EncodeE2EKeyMaterial(webtty.WebTTYSigningKeyID(signingPublicKey)), webtty.EncodeE2EKeyMaterial(signingKeyID)) || seen.ServerKeyAlgorithm != webTTYServerKeyAlgorithmX25519 {
			http.Error(w, "unexpected enrollment payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
			ID:               serverID,
			WorkspaceID:      "workspace-1",
			ProjectID:        projectID,
			Name:             "Shell",
			Status:           "active",
			RecordingPolicy:  "recorded",
			EncryptionPolicy: "explicit_key",
			AccessPolicy:     "project_members",
			CreatedAt:        "2026-06-06T12:00:00.000Z",
			UpdatedAt:        "2026-06-06T12:00:00.000Z",
		})
	}))
	defer server.Close()
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "webtty", "identities", "server.identity.json")
	enrollmentPath := filepath.Join(dir, "webtty", "enrollments", "server.yaml")
	configPath := writeTestGlobalProjectConfig(t)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", configPath)
	cmd := newTestWebTTYServerEnrollCommand()
	cmd.SetContext(t.Context())
	cmd.SetOut(&strings.Builder{})
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	if err := cmd.Flags().Set("project-id", projectID); err != nil {
		t.Fatalf("failed to set project-id: %v", err)
	}
	if err := cmd.Flags().Set("identity-file", identityPath); err != nil {
		t.Fatalf("failed to set identity-file: %v", err)
	}
	if err := cmd.Flags().Set("server-enrollment", enrollmentPath); err != nil {
		t.Fatalf("failed to set server-enrollment: %v", err)
	}
	if err := runWebTTYServerEnroll(cmd, serverID); err != nil {
		t.Fatalf("runWebTTYServerEnroll() error = %v", err)
	}
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		t.Fatalf("loadWebTTYServerEnrollmentFile() error = %v", err)
	}
	if enrollment.ServerID != serverID || enrollment.ProjectID != projectID || enrollment.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected enrollment: %#v", enrollment)
	}
	if enrollment.EncryptionPolicy != webTTYServerEncryptionPolicyExplicitKey {
		t.Fatalf("enrollment encryptionPolicy = %q, want %q", enrollment.EncryptionPolicy, webTTYServerEncryptionPolicyExplicitKey)
	}
	if _, err := webtty.LoadWebTTYEndpointIdentityFile(identityPath); err != nil {
		t.Fatalf("LoadWebTTYEndpointIdentityFile() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(identityPath)
		if err != nil {
			t.Fatalf("stat identity file: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("identity file mode = %o, want 0600", info.Mode().Perm())
		}
	}
	if seen.Capabilities == nil || len(seen.Capabilities.Transports) != 3 || len(seen.Capabilities.ExecutionModes) != 2 {
		t.Fatalf("unexpected capabilities: %#v", seen.Capabilities)
	}
}

func TestRunWebTTYServerEnrollUsesAPIURLOverrideWithProjectID(t *testing.T) {
	clearRstreamTestEnv(t)
	projectID := "project-1"
	serverID := "server-1"
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "wrong control-plane project context was used", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1":
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      "workspace-1",
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "pending_enrollment",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: "explicit_key",
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1/enroll":
			seen["enroll"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      "workspace-1",
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "active",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: "explicit_key",
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := `version: 1
defaults:
  context:
    name: review
contexts:
  - name: review
    apiUrl: https://wrong.example.com
    projectEndpoint: demo
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	dir := t.TempDir()
	enrollmentPath := filepath.Join(dir, "server.yaml")
	cmd := newTestWebTTYServerEnrollCommand()
	cmd.SetContext(t.Context())
	cmd.SetOut(&strings.Builder{})
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "project-id", projectID)
	mustSetFlag(t, cmd, "server-enrollment", enrollmentPath)
	if err := runWebTTYServerEnroll(cmd, serverID); err != nil {
		t.Fatalf("runWebTTYServerEnroll() error = %v", err)
	}
	for _, key := range []string{"get", "enroll"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		t.Fatalf("loadWebTTYServerEnrollmentFile() error = %v", err)
	}
	if enrollment.APIURL != server.URL || enrollment.ProjectID != projectID {
		t.Fatalf("unexpected enrollment runtime metadata: %#v", enrollment)
	}
}

func TestRunWebTTYServerEnrollAutoPinsWorkspaceManagedTrust(t *testing.T) {
	projectID := "project-1"
	serverID := "server-1"
	workspaceID := "workspace-1"
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "review")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	material.file.DeviceKeyID = "device-1"
	material.file.Status = workspaceDeviceStatusActive
	material.file.CreatedAt = now
	material.file.UpdatedAt = now
	if _, err := writeWorkspaceDeviceFile(material.file); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile() error = %v", err)
	}
	keysetPrivate, keysetPublic, bundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, material.file, "keyset-1")
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1":
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      workspaceID,
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "pending_enrollment",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1/enroll":
			seen["enroll"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      workspaceID,
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "active",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			seen["lookup"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  material.file.DeviceKeyID,
					Kind:                material.file.Kind,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: material.file.PublicEncryptionKey,
					PublicSigningKey:    &material.file.PublicSigningKey,
					Fingerprint:         material.file.Fingerprint,
					CreatedAt:           "2026-06-06T12:00:00.000Z",
				}},
				DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{envelope},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1/workspace-trust":
			seen["trust"] = true
			var req controlplane.ApproveWebTTYServerWorkspaceTrustRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			enrollmentPath, _ := defaultWebTTYServerEnrollmentPath(serverID)
			enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload := workspaceWebTTYServerTrustApprovalPayload(enrollment, envelope.KeysetID, material.file.DeviceKeyID, req.SignedAt)
			if req.ActorDeviceKeyID != material.file.DeviceKeyID ||
				req.KeysetID != envelope.KeysetID ||
				verifyWorkspaceP256Signature(material.file.PublicSigningKey, payload, req.ActorSignature, "actor") != nil ||
				verifyWorkspaceP256Signature(keysetPublic, payload, req.KeysetSignature, "keyset") != nil {
				http.Error(w, "invalid trust payload", http.StatusBadRequest)
				return
			}
			status := "workspace_trusted"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      workspaceID,
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "active",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
				AccessPolicy:     "project_members",
				ServerKeyStatus:  &status,
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	cmd := newTestWebTTYServerEnrollCommand()
	cmd.SetContext(t.Context())
	cmd.SetOut(&strings.Builder{})
	mustSetFlag(t, cmd, "project-id", projectID)
	if err := runWebTTYServerEnroll(cmd, serverID); err != nil {
		t.Fatalf("runWebTTYServerEnroll() error = %v", err)
	}
	_ = keysetPrivate
	for _, key := range []string{"get", "lookup", "enroll", "trust"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	enrollmentPath, _ := defaultWebTTYServerEnrollmentPath(serverID)
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		t.Fatalf("loadWebTTYServerEnrollmentFile() error = %v", err)
	}
	if enrollment.WorkspaceTrustKeysetID != envelope.KeysetID ||
		enrollment.WorkspaceTrustKeysetFingerprint != bundle.Fingerprint ||
		enrollment.WorkspaceTrustPublicSigningKey != bundle.PublicSigningKey {
		t.Fatalf("workspace trust pins were not written: %#v", enrollment)
	}
}

func TestRunWebTTYServerEnrollWorkspaceManagedRequiresTrustedDeviceBeforeEnroll(t *testing.T) {
	clearRstreamTestEnv(t)
	projectID := "project-1"
	serverID := "server-1"
	workspaceID := "workspace-1"
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1":
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               serverID,
				WorkspaceID:      workspaceID,
				ProjectID:        projectID,
				Name:             "Shell",
				Status:           "pending_enrollment",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1/enroll":
			seen["enroll"] = true
			http.Error(w, "enroll must not be called without a trusted workspace device", http.StatusBadRequest)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	configPath := writeTestGlobalProjectConfig(t)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", configPath)
	cmd := newTestWebTTYServerEnrollCommand()
	cmd.SetContext(t.Context())
	cmd.SetOut(&strings.Builder{})
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "project-id", projectID)
	err := runWebTTYServerEnroll(cmd, serverID)
	if err == nil {
		t.Fatalf("expected trusted workspace device error")
	}
	for _, want := range []string{
		"workspace-managed WebTTY servers require this machine to be a trusted workspace device before enrollment",
		"rstream workspace device enroll --workspace workspace-1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if !seen["get"] {
		t.Fatalf("server metadata must be loaded before requiring workspace trust")
	}
	if seen["enroll"] {
		t.Fatalf("enroll endpoint was called before local workspace trust was available")
	}
}

func TestLoadWebTTYServerEnrollmentFileRejectsUnsupportedEncryptionPolicy(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	publicKey := webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey)
	enrollmentPath := filepath.Join(t.TempDir(), "server.yaml")
	enrollment := webTTYServerEnrollmentFile{
		Version:                webTTYServerEnrollmentVersion,
		ServerID:               "server-1",
		ProjectID:              "project-1",
		IdentityFile:           filepath.Join(t.TempDir(), "identity.json"),
		ServerPublicKey:        publicKey,
		ServerSigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		ServerSigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ServerFingerprint:      webTTYServerPublicKeyFingerprint(identity.Encryption.PublicKey),
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:       "surprising",
		EnrollmentStatus:       webTTYServerEnrollmentStatusOK,
		EnrolledAt:             time.Now().UTC(),
	}
	if err := writeWebTTYServerEnrollmentFile(enrollmentPath, enrollment); err != nil {
		t.Fatalf("writeWebTTYServerEnrollmentFile() error = %v", err)
	}
	if _, err := loadWebTTYServerEnrollmentFile(enrollmentPath); err == nil || !strings.Contains(err.Error(), "unsupported WebTTY server encryptionPolicy") {
		t.Fatalf("expected unsupported encryptionPolicy error, got %v", err)
	}
}

func TestLoadWebTTYServerEnrollmentFileRejectsWeakPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode validation is not enforced on Windows")
	}
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	publicKey := webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey)
	enrollmentPath := filepath.Join(t.TempDir(), "server.yaml")
	enrollment := webTTYServerEnrollmentFile{
		Version:                webTTYServerEnrollmentVersion,
		ServerID:               "server-1",
		ProjectID:              "project-1",
		IdentityFile:           filepath.Join(t.TempDir(), "identity.json"),
		ServerPublicKey:        publicKey,
		ServerSigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		ServerSigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ServerFingerprint:      webTTYServerPublicKeyFingerprint(identity.Encryption.PublicKey),
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:       webTTYServerEncryptionPolicyExplicitKey,
		EnrollmentStatus:       webTTYServerEnrollmentStatusOK,
		EnrolledAt:             time.Now().UTC(),
	}
	if err := writeWebTTYServerEnrollmentFile(enrollmentPath, enrollment); err != nil {
		t.Fatalf("writeWebTTYServerEnrollmentFile() error = %v", err)
	}
	if err := os.Chmod(enrollmentPath, 0o644); err != nil {
		t.Fatalf("chmod enrollment file: %v", err)
	}
	if _, err := loadWebTTYServerEnrollmentFile(enrollmentPath); err == nil || !strings.Contains(err.Error(), "must not be readable by group or others") {
		t.Fatalf("expected weak permission error, got %v", err)
	}
}

func testWorkspaceKeyEnvelopeForDevice(t *testing.T, device workspaceDeviceFile, keysetID string) (*ecdsa.PrivateKey, string, *workspacePrivateBundle, controlplane.WorkspaceKeyEnvelope) {
	t.Helper()
	keysetEncryptionPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keyset encryption key: %v", err)
	}
	keysetSigningPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate keyset signing key: %v", err)
	}
	keysetSigningPublic, err := keysetSigningPrivate.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("encode keyset signing public key: %v", err)
	}
	keysetSigningScalar, err := keysetSigningPrivate.Bytes()
	if err != nil {
		t.Fatalf("encode keyset signing private key: %v", err)
	}
	keysetPublicEncryption, err := x509.MarshalPKIXPublicKey(keysetEncryptionPrivate.PublicKey())
	if err != nil {
		t.Fatalf("marshal keyset encryption public key: %v", err)
	}
	keysetPublicSigning, err := x509.MarshalPKIXPublicKey(&keysetSigningPrivate.PublicKey)
	if err != nil {
		t.Fatalf("marshal keyset signing public key: %v", err)
	}
	ext := true
	bundle := &workspacePrivateBundle{
		Version:             1,
		CryptoSuite:         workspaceDeviceCryptoSuite,
		PublicEncryptionKey: workspaceBase64URL(keysetPublicEncryption),
		PublicSigningKey:    workspaceBase64URL(keysetPublicSigning),
		SigningPrivateKeyJWK: workspaceECKeyJWK{
			Kty: "EC",
			Crv: "P-256",
			X:   workspaceBase64URL(keysetSigningPublic[1:33]),
			Y:   workspaceBase64URL(keysetSigningPublic[33:]),
			D:   workspaceBase64URL(keysetSigningScalar),
			Ext: &ext,
		},
	}
	bundle.Fingerprint, err = workspacePublicKeyFingerprint(bundle.PublicEncryptionKey, bundle.PublicSigningKey)
	if err != nil {
		t.Fatalf("workspacePublicKeyFingerprint() error = %v", err)
	}
	bundle.EncryptionPrivateKeyJWK = workspaceECKeyJWK{
		Kty: "EC",
		Crv: "P-256",
		D:   workspaceBase64URL(keysetEncryptionPrivate.Bytes()),
		Ext: &ext,
	}
	plaintext, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	envelope := testEncryptWorkspaceKeyEnvelope(t, device, keysetID, plaintext)
	return keysetSigningPrivate, bundle.PublicSigningKey, bundle, envelope
}

func testWebTTYCurrentDeviceResolution(t *testing.T, device workspaceDeviceFile, keysetPrivate *ecdsa.PrivateKey, bundle *workspacePrivateBundle) *controlplane.WebTTYCurrentDeviceResolution {
	t.Helper()
	payload := map[string]any{
		"v":                         1,
		"type":                      "workspace.keyset.setup",
		"workspace_id":              device.WorkspaceID,
		"keyset_fingerprint":        bundle.Fingerprint,
		"keyset_public_signing_key": bundle.PublicSigningKey,
		"device_fingerprint":        device.Fingerprint,
		"device_envelope_hash":      "test-device-envelope-hash",
		"recovery_envelope_hash":    "test-recovery-envelope-hash",
	}
	hash, err := workspaceSHA256Base64URL(payload)
	if err != nil {
		t.Fatalf("workspaceSHA256Base64URL(device trust) error = %v", err)
	}
	signature, err := signWorkspacePayload(keysetPrivate, payload)
	if err != nil {
		t.Fatalf("signWorkspacePayload(device trust) error = %v", err)
	}
	return &controlplane.WebTTYCurrentDeviceResolution{
		DeviceKeyID:          device.DeviceKeyID,
		Kind:                 device.Kind,
		PublicEncryptionKey:  device.PublicEncryptionKey,
		PublicSigningKey:     device.PublicSigningKey,
		Fingerprint:          device.Fingerprint,
		WebTTYPublicKey:      device.WebTTYPublicKey,
		WebTTYKeyID:          device.WebTTYKeyID,
		WebTTYKeyAlgorithm:   device.WebTTYKeyAlgorithm,
		TrustKeysetID:        "keyset-1",
		TrustSource:          "keyset_setup",
		TrustPayload:         mustMarshalWorkspaceTrustPayload(t, payload),
		TrustPayloadHash:     hash,
		TrustKeysetSignature: signature,
		TrustSignedAt:        workspaceSignedAtNow(),
	}
}

func testEncryptWorkspaceKeyEnvelope(t *testing.T, device workspaceDeviceFile, keysetID string, plaintext []byte) controlplane.WorkspaceKeyEnvelope {
	t.Helper()
	recipientPublic, err := workspaceP256ECDHPublicKeyFromSPKI(device.PublicEncryptionKey)
	if err != nil {
		t.Fatalf("parse recipient public key: %v", err)
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ephemeral key: %v", err)
	}
	sharedSecret, err := ephemeral.ECDH(recipientPublic)
	if err != nil {
		t.Fatalf("derive shared secret: %v", err)
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("generate salt: %v", err)
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	key, err := hkdf.Key(sha256.New, sharedSecret, salt, workspaceDeviceEnvelopeInfo, 32)
	if err != nil {
		t.Fatalf("derive envelope key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("create gcm: %v", err)
	}
	ephemeralPublicDER, err := x509.MarshalPKIXPublicKey(ephemeral.PublicKey())
	if err != nil {
		t.Fatalf("marshal ephemeral public key: %v", err)
	}
	return controlplane.WorkspaceKeyEnvelope{
		ID:            "envelope-1",
		KeysetID:      keysetID,
		RecipientKind: "device",
		RecipientID:   device.DeviceKeyID,
		Ciphertext:    base64.RawURLEncoding.EncodeToString(aead.Seal(nil, nonce, plaintext, nil)),
		Crypto: controlplane.WorkspaceKeyEnvelopeCrypto{
			Suite:           workspaceDeviceCryptoSuite,
			EncapsulatedKey: base64.RawURLEncoding.EncodeToString(ephemeralPublicDER),
			Context: map[string]any{
				"salt":  base64.RawURLEncoding.EncodeToString(salt),
				"nonce": base64.RawURLEncoding.EncodeToString(nonce),
				"info":  workspaceDeviceEnvelopeInfo,
			},
		},
		CreatedAt: "2026-06-06T12:00:00.000Z",
	}
}

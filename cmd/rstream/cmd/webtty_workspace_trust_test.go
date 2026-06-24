// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestWebTTYWorkspaceClientCredentialVerifierAcceptsSignedDeviceCredential(t *testing.T) {
	enrollment, endpointIdentity, credential := testWebTTYWorkspaceClientCredentialFixture(t)
	publicKey, err := verifyWebTTYWorkspaceClientCredential(enrollment, webtty.ClientProofVerification{
		Proof: &pb.ClientProof{
			SigningKeyId:     endpointIdentity.Signing.KeyID,
			SigningPublicKey: endpointIdentity.Signing.PublicKey,
		},
		Credential: credential,
	})
	if err != nil {
		t.Fatalf("verifyWebTTYWorkspaceClientCredential() error = %v", err)
	}
	if string(publicKey) != string(endpointIdentity.Signing.PublicKey) {
		t.Fatalf("verified public key mismatch")
	}
}

func TestWebTTYWorkspaceClientCredentialVerifierRejectsDevicePublicKeyMismatch(t *testing.T) {
	enrollment, endpointIdentity, credential := testWebTTYWorkspaceClientCredentialFixture(t)
	tampered := testResignWebTTYWorkspaceClientCredential(t, credential, func(payload map[string]any) {
		payload["device_public_encryption_key"] = "different-public-encryption-key"
	})
	_, err := verifyWebTTYWorkspaceClientCredential(enrollment, webtty.ClientProofVerification{
		Proof: &pb.ClientProof{
			SigningKeyId:     endpointIdentity.Signing.KeyID,
			SigningPublicKey: endpointIdentity.Signing.PublicKey,
		},
		Credential: tampered,
	})
	if err == nil {
		t.Fatalf("expected device public key mismatch to be rejected")
	}
}

func TestWebTTYWorkspaceClientCredentialRequiresResolvedTrustedDevice(t *testing.T) {
	device, resolved := testWebTTYWorkspaceClientCredentialResolutionFixture(t)
	resolved.CurrentDevice = nil
	_, err := webTTYWorkspaceClientCredential(device, resolved)
	if err == nil || !strings.Contains(err.Error(), "requires a resolved trusted device") {
		t.Fatalf("expected missing resolved device error, got %v", err)
	}
}

func TestWebTTYWorkspaceClientCredentialRequiresActiveLocalDevice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*workspaceDeviceFile)
	}{
		{
			name: "revoked",
			mutate: func(device *workspaceDeviceFile) {
				device.Status = workspaceDeviceStatusRevoked
			},
		},
		{
			name: "pending",
			mutate: func(device *workspaceDeviceFile) {
				device.Status = workspaceDeviceStatusPending
			},
		},
		{
			name: "missing envelope",
			mutate: func(device *workspaceDeviceFile) {
				device.DeviceEnvelope = nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device, resolved := testWebTTYWorkspaceClientCredentialResolutionFixture(t)
			tc.mutate(&device)
			_, err := webTTYWorkspaceClientCredential(device, resolved)
			if err == nil || !strings.Contains(err.Error(), "requires an active trusted workspace device") {
				t.Fatalf("expected active local device error, got %v", err)
			}
		})
	}
}

func TestWebTTYWorkspaceClientCredentialRejectsResolvedDeviceMismatch(t *testing.T) {
	device, resolved := testWebTTYWorkspaceClientCredentialResolutionFixture(t)
	resolved.CurrentDevice.DeviceKeyID = "other-device"
	_, err := webTTYWorkspaceClientCredential(device, resolved)
	if err == nil || !strings.Contains(err.Error(), "does not match local trusted device") {
		t.Fatalf("expected resolved device mismatch error, got %v", err)
	}
}

func TestWebTTYWorkspaceClientCredentialRejectsResolvedKeyMismatch(t *testing.T) {
	device, resolved := testWebTTYWorkspaceClientCredentialResolutionFixture(t)
	resolved.CurrentDevice.PublicSigningKey = "different-public-signing-key"
	_, err := webTTYWorkspaceClientCredential(device, resolved)
	if err == nil || !strings.Contains(err.Error(), "client keys do not match trusted device") {
		t.Fatalf("expected resolved key mismatch error, got %v", err)
	}
}

func TestWebTTYWorkspaceClientCredentialRejectsResolvedEndpointIdentityMismatch(t *testing.T) {
	device, resolved := testWebTTYWorkspaceClientCredentialResolutionFixture(t)
	resolved.CurrentDevice.WebTTYKeyID = "different-webtty-key-id"
	_, err := webTTYWorkspaceClientCredential(device, resolved)
	if err == nil || !strings.Contains(err.Error(), "client endpoint identity does not match trusted device") {
		t.Fatalf("expected resolved endpoint identity mismatch error, got %v", err)
	}
}

func TestWebTTYWorkspaceClientCredentialVerifierRejectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification)
		want   string
	}{
		{
			name: "server binding mismatch",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				tampered := testResignWebTTYWorkspaceClientCredential(t, credential, func(payload map[string]any) {
					payload["server_id"] = "other-server"
				})
				return enrollment, testWebTTYClientProofVerification(endpointIdentity, tampered)
			},
			want: "does not match this server",
		},
		{
			name: "keyset pin mismatch",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				copy := *enrollment
				copy.WorkspaceTrustKeysetID = "other-keyset"
				return &copy, testWebTTYClientProofVerification(endpointIdentity, credential)
			},
			want: "keyset does not match server enrollment",
		},
		{
			name: "proof public key mismatch",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				other, err := webtty.GenerateWebTTYEndpointIdentity()
				if err != nil {
					t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
				}
				verification := testWebTTYClientProofVerification(endpointIdentity, credential)
				verification.Proof.SigningPublicKey = other.Signing.PublicKey
				return enrollment, verification
			},
			want: "signing key does not match proof",
		},
		{
			name: "trust payload hash mismatch",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				tampered := testResignWebTTYWorkspaceClientCredential(t, credential, func(payload map[string]any) {
					payload["trust_payload_hash"] = "invalid-hash"
				})
				return enrollment, testWebTTYClientProofVerification(endpointIdentity, tampered)
			},
			want: "trust payload hash does not match payload",
		},
		{
			name: "trust payload signature mismatch",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				tampered := testResignWebTTYWorkspaceClientCredential(t, credential, func(payload map[string]any) {
					payload["trust_keyset_signature"] = "invalid-signature"
				})
				return enrollment, testWebTTYClientProofVerification(endpointIdentity, tampered)
			},
			want: "workspace-managed WebTTY device trust signature",
		},
		{
			name: "credential signature mismatch",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				tampered := testMutateWebTTYWorkspaceClientCredentialWithoutResigning(t, credential, func(payload map[string]any) {
					payload["device_kind"] = "automation"
				})
				return enrollment, testWebTTYClientProofVerification(endpointIdentity, tampered)
			},
			want: "workspace-managed WebTTY client credential signature is invalid",
		},
		{
			name: "unsupported credential type",
			mutate: func(t *testing.T, enrollment *webTTYServerEnrollmentFile, endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) (*webTTYServerEnrollmentFile, webtty.ClientProofVerification) {
				t.Helper()
				tampered := testResignWebTTYWorkspaceClientCredential(t, credential, func(payload map[string]any) {
					payload["type"] = "workspace.other.credential"
				})
				return enrollment, testWebTTYClientProofVerification(endpointIdentity, tampered)
			},
			want: "unsupported type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enrollment, endpointIdentity, credential := testWebTTYWorkspaceClientCredentialFixture(t)
			enrollment, verification := tt.mutate(t, enrollment, endpointIdentity, credential)
			_, err := verifyWebTTYWorkspaceClientCredential(enrollment, verification)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func testWebTTYWorkspaceClientCredentialFixture(t *testing.T) (*webTTYServerEnrollmentFile, *webtty.WebTTYEndpointIdentity, []byte) {
	t.Helper()
	testSetWebTTYWorkspaceTrustHome(t)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", "cli", "test device")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-cli"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC()
	device.UpdatedAt = device.CreatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	enrollment := &webTTYServerEnrollmentFile{
		Version:                         webTTYServerEnrollmentVersion,
		ServerID:                        "server-1",
		WorkspaceID:                     device.WorkspaceID,
		ProjectID:                       "project-1",
		IdentityFile:                    "/tmp/server-1.identity.json",
		ServerPublicKey:                 webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey),
		ServerSigningKeyID:              webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.KeyID),
		ServerSigningPublicKey:          webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.PublicKey),
		ServerFingerprint:               webTTYServerPublicKeyFingerprint(serverIdentity.Encryption.PublicKey),
		ServerKeyAlgorithm:              webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:                webTTYServerEncryptionPolicyWorkspaceManaged,
		EnrollmentStatus:                webTTYServerEnrollmentStatusOK,
		WorkspaceTrustKeysetID:          "keyset-1",
		WorkspaceTrustKeysetFingerprint: keysetBundle.Fingerprint,
		WorkspaceTrustPublicSigningKey:  keysetBundle.PublicSigningKey,
	}
	currentDevice := testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle)
	resolved := controlplane.ResolveWebTTYServerClientResponse{
		ServerID:         enrollment.ServerID,
		WorkspaceID:      enrollment.WorkspaceID,
		ProjectID:        enrollment.ProjectID,
		EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
		E2ERequired:      true,
		CurrentDevice:    currentDevice,
	}
	credential, err := webTTYWorkspaceClientCredential(device, resolved)
	if err != nil {
		t.Fatalf("webTTYWorkspaceClientCredential() error = %v", err)
	}
	endpointIdentity, err := workspaceDeviceWebTTYEndpointIdentity(device)
	if err != nil {
		t.Fatalf("workspaceDeviceWebTTYEndpointIdentity() error = %v", err)
	}
	return enrollment, endpointIdentity, credential
}

func testWebTTYWorkspaceClientCredentialResolutionFixture(t *testing.T) (workspaceDeviceFile, controlplane.ResolveWebTTYServerClientResponse) {
	t.Helper()
	testSetWebTTYWorkspaceTrustHome(t)
	material, err := generateWorkspaceDeviceMaterial("workspace-1", "cli", "test device")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-cli"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC()
	device.UpdatedAt = device.CreatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	return device, controlplane.ResolveWebTTYServerClientResponse{
		ServerID:         "server-1",
		WorkspaceID:      device.WorkspaceID,
		ProjectID:        "project-1",
		EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
		E2ERequired:      true,
		CurrentDevice:    testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle),
	}
}

func testWebTTYWorkspaceTrustEnrollment(t *testing.T) (*webTTYServerEnrollmentFile, *webtty.WebTTYEndpointIdentity) {
	t.Helper()
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	return &webTTYServerEnrollmentFile{
		Version:                webTTYServerEnrollmentVersion,
		ServerID:               "prod-shell",
		WorkspaceID:            "workspace-1",
		ProjectID:              "project-1",
		IdentityFile:           "/tmp/prod-shell.identity.json",
		ServerPublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		ServerSigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		ServerSigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ServerFingerprint:      webTTYServerPublicKeyFingerprint(identity.Encryption.PublicKey),
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:       webTTYServerEncryptionPolicyWorkspaceManaged,
		EnrollmentStatus:       webTTYServerEnrollmentStatusOK,
	}, identity
}

func testWebTTYClientProofVerification(endpointIdentity *webtty.WebTTYEndpointIdentity, credential []byte) webtty.ClientProofVerification {
	return webtty.ClientProofVerification{
		Proof: &pb.ClientProof{
			SigningKeyId:     endpointIdentity.Signing.KeyID,
			SigningPublicKey: endpointIdentity.Signing.PublicKey,
		},
		Credential: credential,
	}
}

func testResignWebTTYWorkspaceClientCredential(t *testing.T, credential []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	envelope, err := decodeWebTTYWorkspaceClientCredential(credential)
	if err != nil {
		t.Fatalf("decodeWebTTYWorkspaceClientCredential() error = %v", err)
	}
	mutate(envelope.Payload)
	deviceMaterial := workspaceDeviceFile{
		PrivateSigningKey: testWorkspaceDevicePrivateSigningKeyFromCredential(t, credential),
	}
	signingKey, err := parseWorkspaceDeviceSigningKey(deviceMaterial)
	if err != nil {
		t.Fatalf("parseWorkspaceDeviceSigningKey() error = %v", err)
	}
	signature, err := signWorkspacePayload(signingKey, envelope.Payload)
	if err != nil {
		t.Fatalf("signWorkspacePayload() error = %v", err)
	}
	raw, err := workspaceCanonicalJSON(map[string]any{
		"v":         1,
		"payload":   envelope.Payload,
		"signature": signature,
	})
	if err != nil {
		t.Fatalf("workspaceCanonicalJSON() error = %v", err)
	}
	return []byte(raw)
}

func testMutateWebTTYWorkspaceClientCredentialWithoutResigning(t *testing.T, credential []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	envelope, err := decodeWebTTYWorkspaceClientCredential(credential)
	if err != nil {
		t.Fatalf("decodeWebTTYWorkspaceClientCredential() error = %v", err)
	}
	mutate(envelope.Payload)
	raw, err := workspaceCanonicalJSON(map[string]any{
		"v":         envelope.Version,
		"payload":   envelope.Payload,
		"signature": envelope.Signature,
	})
	if err != nil {
		t.Fatalf("workspaceCanonicalJSON() error = %v", err)
	}
	return []byte(raw)
}

func testWorkspaceDevicePrivateSigningKeyFromCredential(t *testing.T, credential []byte) string {
	t.Helper()
	envelope, err := decodeWebTTYWorkspaceClientCredential(credential)
	if err != nil {
		t.Fatalf("decodeWebTTYWorkspaceClientCredential() error = %v", err)
	}
	deviceKeyID, _ := envelope.Payload["device_key_id"].(string)
	if strings.TrimSpace(deviceKeyID) == "" {
		t.Fatalf("credential payload has no device_key_id")
	}
	devices, err := loadWorkspaceDeviceFiles("workspace-1")
	if err != nil {
		t.Fatalf("loadWorkspaceDeviceFiles() error = %v", err)
	}
	for _, item := range devices {
		if item.device.DeviceKeyID == deviceKeyID {
			return item.device.PrivateSigningKey
		}
	}
	t.Fatalf("workspace device %s was not found", deviceKeyID)
	return ""
}

func testSetWebTTYWorkspaceTrustHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func mustMarshalWorkspaceTrustPayload(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

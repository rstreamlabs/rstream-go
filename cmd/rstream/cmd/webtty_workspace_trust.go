// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func webTTYWorkspaceClientCredential(device workspaceDeviceFile, resolved controlplane.ResolveWebTTYServerClientResponse) ([]byte, error) {
	if !workspaceDeviceIsActive(device) {
		return nil, fmt.Errorf("workspace-managed WebTTY client credential requires an active trusted workspace device")
	}
	current := resolved.CurrentDevice
	if current == nil {
		return nil, fmt.Errorf("workspace-managed WebTTY client credential requires a resolved trusted device")
	}
	if device.DeviceKeyID != current.DeviceKeyID {
		return nil, fmt.Errorf("workspace-managed WebTTY resolved device does not match local trusted device")
	}
	endpointIdentity, err := workspaceDeviceWebTTYEndpointIdentity(device)
	if err != nil {
		return nil, err
	}
	clientSigningKeyID := webtty.EncodeE2EKeyMaterial(endpointIdentity.Signing.KeyID)
	clientSigningPublicKey := webtty.EncodeE2EKeyMaterial(endpointIdentity.Signing.PublicKey)
	if device.PublicEncryptionKey != current.PublicEncryptionKey ||
		clientSigningPublicKey != current.PublicSigningKey {
		return nil, fmt.Errorf("workspace-managed WebTTY client keys do not match trusted device")
	}
	if device.WebTTYPublicKey != current.WebTTYPublicKey ||
		device.WebTTYKeyID != current.WebTTYKeyID ||
		device.WebTTYKeyAlgorithm != current.WebTTYKeyAlgorithm {
		return nil, fmt.Errorf("workspace-managed WebTTY client endpoint identity does not match trusted device")
	}
	trustPayload, err := workspaceTrustPayload(current.TrustPayload)
	if err != nil {
		return nil, err
	}
	signedAt := workspaceSignedAtNow()
	payload := map[string]any{
		"v":                            1,
		"type":                         "workspace.webtty.client.credential",
		"workspace_id":                 resolved.WorkspaceID,
		"project_id":                   resolved.ProjectID,
		"server_id":                    resolved.ServerID,
		"device_key_id":                current.DeviceKeyID,
		"device_kind":                  current.Kind,
		"device_fingerprint":           current.Fingerprint,
		"device_public_encryption_key": current.PublicEncryptionKey,
		"device_public_signing_key":    device.PublicSigningKey,
		"client_signing_key_id":        clientSigningKeyID,
		"client_signing_public_key":    clientSigningPublicKey,
		"webtty_public_key":            device.WebTTYPublicKey,
		"webtty_key_id":                device.WebTTYKeyID,
		"webtty_key_algorithm":         device.WebTTYKeyAlgorithm,
		"trust_keyset_id":              current.TrustKeysetID,
		"trust_source":                 current.TrustSource,
		"trust_payload":                trustPayload,
		"trust_payload_hash":           current.TrustPayloadHash,
		"trust_keyset_signature":       current.TrustKeysetSignature,
		"trust_signed_at":              current.TrustSignedAt,
		"signed_at":                    signedAt,
	}
	if current.TrustActorSignature != nil && strings.TrimSpace(*current.TrustActorSignature) != "" {
		payload["trust_actor_signature"] = strings.TrimSpace(*current.TrustActorSignature)
	}
	signingKey, err := parseWorkspaceDeviceSigningKey(device)
	if err != nil {
		return nil, err
	}
	signature, err := signWorkspacePayload(signingKey, payload)
	if err != nil {
		return nil, err
	}
	envelope, err := workspaceCanonicalJSON(map[string]any{
		"v":         1,
		"payload":   payload,
		"signature": signature,
	})
	if err != nil {
		return nil, err
	}
	return []byte(envelope), nil
}

func webTTYWorkspaceClientProofVerifier(enrollment *webTTYServerEnrollmentFile) webtty.ClientProofVerifier {
	if enrollment == nil || enrollment.EncryptionPolicy != webTTYServerEncryptionPolicyWorkspaceManaged {
		return nil
	}
	return func(_ context.Context, verification webtty.ClientProofVerification) ([]byte, error) {
		return verifyWebTTYWorkspaceClientCredential(enrollment, verification)
	}
}

type webTTYWorkspaceClientCredentialEnvelope struct {
	Version   int            `json:"v"`
	Payload   map[string]any `json:"payload"`
	Signature string         `json:"signature"`
}

func verifyWebTTYWorkspaceClientCredential(enrollment *webTTYServerEnrollmentFile, verification webtty.ClientProofVerification) ([]byte, error) {
	if enrollment == nil {
		return nil, fmt.Errorf("workspace-managed WebTTY client credential requires a registered server enrollment")
	}
	if len(verification.Credential) == 0 {
		return nil, fmt.Errorf("workspace-managed WebTTY client credential is required")
	}
	envelope, err := decodeWebTTYWorkspaceClientCredential(verification.Credential)
	if err != nil {
		return nil, err
	}
	payload := envelope.Payload
	if workspaceTrustString(payload, "type") != "workspace.webtty.client.credential" {
		return nil, fmt.Errorf("workspace-managed WebTTY client credential has an unsupported type")
	}
	if err := validateWebTTYWorkspaceClientCredentialPayload(enrollment, verification, payload); err != nil {
		return nil, err
	}
	publicSigningKey := workspaceTrustString(payload, "client_signing_public_key")
	if err := verifyWorkspaceP256Signature(publicSigningKey, payload, envelope.Signature, "workspace-managed WebTTY client credential"); err != nil {
		return nil, err
	}
	return append([]byte(nil), verification.Proof.SigningPublicKey...), nil
}

func decodeWebTTYWorkspaceClientCredential(raw []byte) (webTTYWorkspaceClientCredentialEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var envelope webTTYWorkspaceClientCredentialEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("decode workspace-managed WebTTY client credential: %w", err)
	}
	if err := ensureSingleWorkspaceTrustJSONValue(decoder); err != nil {
		return webTTYWorkspaceClientCredentialEnvelope{}, err
	}
	if envelope.Version != 1 {
		return webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("workspace-managed WebTTY client credential has unsupported version %d", envelope.Version)
	}
	if len(envelope.Payload) == 0 {
		return webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("workspace-managed WebTTY client credential payload is required")
	}
	if strings.TrimSpace(envelope.Signature) == "" {
		return webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("workspace-managed WebTTY client credential signature is required")
	}
	return envelope, nil
}

func validateWebTTYWorkspaceClientCredentialPayload(enrollment *webTTYServerEnrollmentFile, verification webtty.ClientProofVerification, payload map[string]any) error {
	if workspaceTrustString(payload, "workspace_id") != enrollment.WorkspaceID ||
		workspaceTrustString(payload, "project_id") != enrollment.ProjectID ||
		workspaceTrustString(payload, "server_id") != enrollment.ServerID {
		return fmt.Errorf("workspace-managed WebTTY client credential does not match this server")
	}
	if workspaceTrustString(payload, "trust_keyset_id") != enrollment.WorkspaceTrustKeysetID {
		return fmt.Errorf("workspace-managed WebTTY client credential keyset does not match server enrollment")
	}
	if workspaceTrustString(payload, "client_signing_key_id") != webtty.EncodeE2EKeyMaterial(verification.Proof.SigningKeyId) {
		return fmt.Errorf("workspace-managed WebTTY client credential signing key id does not match proof")
	}
	publicSigningKey := workspaceTrustString(payload, "client_signing_public_key")
	devicePublicSigningKey := workspaceTrustString(payload, "device_public_signing_key")
	if publicSigningKey == "" || publicSigningKey != devicePublicSigningKey {
		return fmt.Errorf("workspace-managed WebTTY client credential signing key does not match trusted device")
	}
	publicSigningKeyBytes, err := webtty.DecodeE2EKeyMaterial(publicSigningKey, 0, "workspace-managed WebTTY client signing public key")
	if err != nil {
		return err
	}
	if !bytes.Equal(publicSigningKeyBytes, verification.Proof.SigningPublicKey) {
		return fmt.Errorf("workspace-managed WebTTY client credential signing key does not match proof")
	}
	fingerprint, err := workspacePublicKeyFingerprint(
		workspaceTrustString(payload, "device_public_encryption_key"),
		devicePublicSigningKey,
	)
	if err != nil {
		return err
	}
	if fingerprint != workspaceTrustString(payload, "device_fingerprint") {
		return fmt.Errorf("workspace-managed WebTTY client credential device fingerprint does not match public keys")
	}
	trustPayload, ok := payload["trust_payload"].(map[string]any)
	if !ok {
		return fmt.Errorf("workspace-managed WebTTY client credential trust payload is invalid")
	}
	hash, err := workspaceSHA256Base64URL(trustPayload)
	if err != nil {
		return err
	}
	if hash != workspaceTrustString(payload, "trust_payload_hash") {
		return fmt.Errorf("workspace-managed WebTTY client credential trust payload hash does not match payload")
	}
	if !workspaceClientCredentialTrustPayloadMatches(enrollment, payload, trustPayload) {
		return fmt.Errorf("workspace-managed WebTTY client credential trust payload does not match device")
	}
	if err := verifyWorkspaceP256Signature(enrollment.WorkspaceTrustPublicSigningKey, trustPayload, workspaceTrustString(payload, "trust_keyset_signature"), "workspace-managed WebTTY device trust"); err != nil {
		return err
	}
	return nil
}

func workspaceClientCredentialTrustPayloadMatches(enrollment *webTTYServerEnrollmentFile, credential map[string]any, trustPayload map[string]any) bool {
	if workspaceTrustString(trustPayload, "workspace_id") != enrollment.WorkspaceID {
		return false
	}
	deviceFingerprint := workspaceTrustString(credential, "device_fingerprint")
	deviceKeyID := workspaceTrustString(credential, "device_key_id")
	switch workspaceTrustString(trustPayload, "type") {
	case "workspace.keyset.setup":
		return workspaceTrustString(trustPayload, "keyset_fingerprint") == enrollment.WorkspaceTrustKeysetFingerprint &&
			workspaceTrustString(trustPayload, "keyset_public_signing_key") == enrollment.WorkspaceTrustPublicSigningKey &&
			workspaceTrustString(trustPayload, "device_fingerprint") == deviceFingerprint
	case "workspace.device.approve":
		return workspaceTrustString(trustPayload, "keyset_id") == enrollment.WorkspaceTrustKeysetID &&
			workspaceTrustString(trustPayload, "target_device_key_id") == deviceKeyID &&
			workspaceTrustString(trustPayload, "target_fingerprint") == deviceFingerprint
	case "workspace.recovery_kit.use":
		return workspaceTrustString(trustPayload, "keyset_id") == enrollment.WorkspaceTrustKeysetID &&
			workspaceTrustString(trustPayload, "device_fingerprint") == deviceFingerprint
	default:
		return false
	}
}

func webTTYServerEnrollmentWorkspaceID(enrollment *webTTYServerEnrollmentFile) string {
	if enrollment == nil {
		return ""
	}
	return strings.TrimSpace(enrollment.WorkspaceID)
}

func webTTYServerEnrollmentProjectID(enrollment *webTTYServerEnrollmentFile) string {
	if enrollment == nil {
		return ""
	}
	return strings.TrimSpace(enrollment.ProjectID)
}

func webTTYServerEnrollmentServerID(enrollment *webTTYServerEnrollmentFile) string {
	if enrollment == nil {
		return ""
	}
	return strings.TrimSpace(enrollment.ServerID)
}

func workspaceTrustPayload(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode workspace-managed WebTTY trust payload: %w", err)
	}
	if err := ensureSingleWorkspaceTrustJSONValue(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func ensureSingleWorkspaceTrustJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("decode workspace-managed WebTTY trust payload: multiple JSON values")
	} else if err != io.EOF {
		return fmt.Errorf("decode workspace-managed WebTTY trust payload: %w", err)
	}
	return nil
}

func workspaceTrustString(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return value
}

func verifyWorkspaceP256Signature(publicSigningKey string, payload any, signature string, label string) error {
	publicKey, err := workspaceP256PublicSigningKey(publicSigningKey)
	if err != nil {
		return fmt.Errorf("%s public signing key is invalid: %w", label, err)
	}
	canonical, err := workspaceCanonicalJSON(payload)
	if err != nil {
		return fmt.Errorf("%s payload is invalid: %w", label, err)
	}
	rawSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%s signature is not base64url: %w", label, err)
	}
	digest := sha256.Sum256([]byte(canonical))
	if ecdsa.VerifyASN1(publicKey, digest[:], rawSignature) {
		return nil
	}
	if len(rawSignature) == 64 {
		r := new(big.Int).SetBytes(rawSignature[:32])
		s := new(big.Int).SetBytes(rawSignature[32:])
		if ecdsa.Verify(publicKey, digest[:], r, s) {
			return nil
		}
	}
	return fmt.Errorf("%s signature is invalid", label)
}

func workspaceP256PublicSigningKey(publicSigningKey string) (*ecdsa.PublicKey, error) {
	der, err := base64.RawURLEncoding.DecodeString(publicSigningKey)
	if err != nil {
		return nil, err
	}
	return webtty.ParseWebTTYSigningPublicKey(der)
}

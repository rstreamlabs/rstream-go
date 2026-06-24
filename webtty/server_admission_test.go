// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewWebTTYServerAdmissionProofLabelSignsVerifiableProof(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	labels := map[string]string{
		WebTTYServerIDLabelKey:         "server-1",
		WebTTYEncryptionPolicyLabelKey: "disabled",
		WebTTYE2ELabelKey:              WebTTYE2EDisabled,
		WebTTYClientProofLabelKey:      WebTTYClientProofNone,
	}
	now := time.Now().UTC().Truncate(time.Second)
	label, err := NewWebTTYServerAdmissionProofLabel(identity, WebTTYServerAdmissionProofParams{
		WorkspaceID:    "workspace-1",
		ProjectID:      "project-1",
		ServerID:       "server-1",
		TunnelProtocol: "webtty",
		TunnelType:     "bytestream",
		Labels:         labels,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("NewWebTTYServerAdmissionProofLabel() error = %v", err)
	}
	proof := decodeWebTTYServerAdmissionProofLabelForTest(t, label)
	if proof.WorkspaceID != "workspace-1" || proof.ProjectID != "project-1" || proof.ServerID != "server-1" {
		t.Fatalf("unexpected proof target: %#v", proof)
	}
	if proof.SigningKeyID != EncodeE2EKeyMaterial(identity.Signing.KeyID) {
		t.Fatalf("proof signing key = %q, want %q", proof.SigningKeyID, EncodeE2EKeyMaterial(identity.Signing.KeyID))
	}
	if proof.LabelsSHA256 != EncodeE2EKeyMaterial(WebTTYServerAdmissionLabelsHash(labels)) {
		t.Fatalf("proof labels hash = %q, want current labels hash", proof.LabelsSHA256)
	}
	signature, err := DecodeE2EKeyMaterial(proof.Signature, 0, "server admission signature")
	if err != nil {
		t.Fatalf("DecodeE2EKeyMaterial(signature) error = %v", err)
	}
	publicKey, err := ParseWebTTYSigningPublicKey(identity.Signing.PublicKey)
	if err != nil {
		t.Fatalf("ParseWebTTYSigningPublicKey() error = %v", err)
	}
	hash := sha256.Sum256(WebTTYServerAdmissionTranscript(proof))
	if !ecdsa.VerifyASN1(publicKey, hash[:], signature) {
		t.Fatal("server admission signature does not verify")
	}
}

func TestWebTTYServerAdmissionLabelsHashIgnoresProofLabel(t *testing.T) {
	labels := map[string]string{
		WebTTYServerIDLabelKey:         "server-1",
		WebTTYEncryptionPolicyLabelKey: "disabled",
		WebTTYServerAdmissionLabelKey:  "previous-proof",
	}
	withProof := WebTTYServerAdmissionLabelsHash(labels)
	delete(labels, WebTTYServerAdmissionLabelKey)
	withoutProof := WebTTYServerAdmissionLabelsHash(labels)
	if string(withProof) != string(withoutProof) {
		t.Fatal("server admission labels hash must ignore the proof label")
	}
}

func TestNewWebTTYServerAdmissionProofLabelRequiresTarget(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	_, err = NewWebTTYServerAdmissionProofLabel(identity, WebTTYServerAdmissionProofParams{
		WorkspaceID:    "workspace-1",
		ProjectID:      "project-1",
		TunnelProtocol: "webtty",
		TunnelType:     "bytestream",
	})
	if err == nil || !strings.Contains(err.Error(), "server ID is required") {
		t.Fatalf("expected missing server ID error, got %v", err)
	}
}

func decodeWebTTYServerAdmissionProofLabelForTest(t *testing.T, label string) *WebTTYServerAdmissionProof {
	t.Helper()
	data, err := DecodeE2EKeyMaterial(label, 0, "server admission label")
	if err != nil {
		t.Fatalf("DecodeE2EKeyMaterial(label) error = %v", err)
	}
	var proof WebTTYServerAdmissionProof
	if err := json.Unmarshal(data, &proof); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return &proof
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	WebTTYServerAdmissionVersion      = 1
	WebTTYServerAdmissionSuite        = "webtty-server-admission-ecdsa-p256-sha256-v1"
	WebTTYServerAdmissionDomain       = "rstream-webtty-server-admission-v1"
	webTTYServerAdmissionLabelsDomain = "rstream-webtty-server-admission-labels-v1"
	webTTYServerAdmissionTTL          = 90 * time.Second
)

type WebTTYServerAdmissionProofParams struct {
	WorkspaceID    string
	ProjectID      string
	ServerID       string
	TunnelProtocol string
	TunnelType     string
	Labels         map[string]string
	Now            time.Time
	Random         io.Reader
}

type WebTTYServerAdmissionProof struct {
	Version        int    `json:"version"`
	Suite          string `json:"suite"`
	WorkspaceID    string `json:"workspace_id"`
	ProjectID      string `json:"project_id"`
	ServerID       string `json:"server_id"`
	TunnelProtocol string `json:"tunnel_protocol"`
	TunnelType     string `json:"tunnel_type"`
	LabelsSHA256   string `json:"labels_sha256"`
	SigningKeyID   string `json:"signing_key_id"`
	IssuedAt       string `json:"issued_at"`
	ExpiresAt      string `json:"expires_at"`
	Nonce          string `json:"nonce"`
	Signature      string `json:"signature"`
}

func NewWebTTYServerAdmissionProofLabel(identity *WebTTYEndpointIdentity, params WebTTYServerAdmissionProofParams) (string, error) {
	if identity == nil {
		return "", fmt.Errorf("WebTTY server admission identity is required")
	}
	privateKey, err := ParseWebTTYSigningPrivateKey(identity.Signing.PrivateKey)
	if err != nil {
		return "", err
	}
	now := params.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	random := params.Random
	if random == nil {
		random = rand.Reader
	}
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return "", fmt.Errorf("generate WebTTY server admission nonce: %w", err)
	}
	proof := &WebTTYServerAdmissionProof{
		Version:        WebTTYServerAdmissionVersion,
		Suite:          WebTTYServerAdmissionSuite,
		WorkspaceID:    strings.TrimSpace(params.WorkspaceID),
		ProjectID:      strings.TrimSpace(params.ProjectID),
		ServerID:       strings.TrimSpace(params.ServerID),
		TunnelProtocol: strings.TrimSpace(params.TunnelProtocol),
		TunnelType:     strings.TrimSpace(params.TunnelType),
		LabelsSHA256:   EncodeE2EKeyMaterial(WebTTYServerAdmissionLabelsHash(params.Labels)),
		SigningKeyID:   EncodeE2EKeyMaterial(identity.Signing.KeyID),
		IssuedAt:       now.Format(time.RFC3339Nano),
		ExpiresAt:      now.Add(webTTYServerAdmissionTTL).Format(time.RFC3339Nano),
		Nonce:          EncodeE2EKeyMaterial(nonce),
	}
	if err := validateWebTTYServerAdmissionProofForSigning(proof); err != nil {
		return "", err
	}
	signature, err := signWebTTYServerAdmissionProof(privateKey, proof, random)
	if err != nil {
		return "", err
	}
	proof.Signature = EncodeE2EKeyMaterial(signature)
	data, err := json.Marshal(proof)
	if err != nil {
		return "", fmt.Errorf("encode WebTTY server admission proof: %w", err)
	}
	return EncodeE2EKeyMaterial(data), nil
}

func WebTTYServerAdmissionLabelsHash(labels map[string]string) []byte {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key == WebTTYServerAdmissionLabelKey {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []byte(webTTYServerAdmissionLabelsDomain)
	out = appendUint32(out, uint32(len(keys)))
	for _, key := range keys {
		out = appendWebTTYServerAdmissionString(out, key)
		out = appendWebTTYServerAdmissionString(out, labels[key])
	}
	digest := sha256.Sum256(out)
	return cloneBytes(digest[:])
}

func WebTTYServerAdmissionTranscript(proof *WebTTYServerAdmissionProof) []byte {
	if proof == nil {
		return nil
	}
	out := []byte(WebTTYServerAdmissionDomain)
	out = appendUint32(out, uint32(proof.Version))
	out = appendWebTTYServerAdmissionString(out, proof.Suite)
	out = appendWebTTYServerAdmissionString(out, proof.WorkspaceID)
	out = appendWebTTYServerAdmissionString(out, proof.ProjectID)
	out = appendWebTTYServerAdmissionString(out, proof.ServerID)
	out = appendWebTTYServerAdmissionString(out, proof.TunnelProtocol)
	out = appendWebTTYServerAdmissionString(out, proof.TunnelType)
	out = appendWebTTYServerAdmissionString(out, proof.LabelsSHA256)
	out = appendWebTTYServerAdmissionString(out, proof.SigningKeyID)
	out = appendWebTTYServerAdmissionString(out, proof.IssuedAt)
	out = appendWebTTYServerAdmissionString(out, proof.ExpiresAt)
	out = appendWebTTYServerAdmissionString(out, proof.Nonce)
	return out
}

func appendWebTTYServerAdmissionString(dst []byte, value string) []byte {
	return appendLengthPrefixed(dst, []byte(value))
}

func signWebTTYServerAdmissionProof(privateKey *ecdsa.PrivateKey, proof *WebTTYServerAdmissionProof, random io.Reader) ([]byte, error) {
	hash := sha256.Sum256(WebTTYServerAdmissionTranscript(proof))
	signature, err := ecdsa.SignASN1(random, privateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("sign WebTTY server admission proof: %w", err)
	}
	return signature, nil
}

func validateWebTTYServerAdmissionProofForSigning(proof *WebTTYServerAdmissionProof) error {
	if proof == nil {
		return fmt.Errorf("WebTTY server admission proof is required")
	}
	if proof.WorkspaceID == "" {
		return fmt.Errorf("WebTTY server admission workspace ID is required")
	}
	if proof.ProjectID == "" {
		return fmt.Errorf("WebTTY server admission project ID is required")
	}
	if proof.ServerID == "" {
		return fmt.Errorf("WebTTY server admission server ID is required")
	}
	if proof.TunnelProtocol == "" {
		return fmt.Errorf("WebTTY server admission tunnel protocol is required")
	}
	if proof.TunnelType == "" {
		return fmt.Errorf("WebTTY server admission tunnel type is required")
	}
	return nil
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestWebTTYClientProofTranscriptHashChangesWithBoundFields(t *testing.T) {
	transcript := testClientProofTranscript(t)
	hash := transcript.Hash()
	if len(hash) != 32 {
		t.Fatalf("hash length = %d, want 32", len(hash))
	}
	if got := EncodeE2EKeyMaterial(hash); got != "KENVbyGFj-d6U1ePniz0ZngXjadyEZJ0IWK2IOODG8Y" {
		t.Fatalf("client transcript hash = %s", got)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*ClientProofTranscript)
	}{
		{name: "transport", mutate: func(value *ClientProofTranscript) { value.Transport = "plain" }},
		{name: "workspace", mutate: func(value *ClientProofTranscript) { value.WorkspaceID = "workspace-2" }},
		{name: "project", mutate: func(value *ClientProofTranscript) { value.ProjectID = "project-2" }},
		{name: "server", mutate: func(value *ClientProofTranscript) { value.ServerID = "server-2" }},
		{name: "session", mutate: func(value *ClientProofTranscript) { value.SessionID = "session-2" }},
		{name: "server signing key", mutate: func(value *ClientProofTranscript) { value.ServerSigningKeyID = []byte("different") }},
		{name: "server encryption key", mutate: func(value *ClientProofTranscript) { value.ServerEncryptionKeyID = []byte("different") }},
		{name: "server nonce", mutate: func(value *ClientProofTranscript) { value.ServerNonce = []byte("different") }},
		{name: "auth requirement", mutate: func(value *ClientProofTranscript) { value.AuthRequirement = AuthRequirementNone }},
		{name: "payload suite", mutate: func(value *ClientProofTranscript) { value.PayloadSuite = PayloadCipherSuiteChaCha20Poly1305 }},
		{name: "key envelope suite", mutate: func(value *ClientProofTranscript) {
			value.KeyEnvelopeSuite = KeyEnvelopeSuiteHPKEX25519HKDFSHA256ChaCha20Poly1305
		}},
		{name: "session key grant", mutate: func(value *ClientProofTranscript) { value.SessionKeyGrantHash = []byte("different") }},
		{name: "command config", mutate: func(value *ClientProofTranscript) { value.CommandConfigHash = []byte("different") }},
		{name: "client principal", mutate: func(value *ClientProofTranscript) { value.ClientPrincipalID = "user-2" }},
		{name: "client signing key", mutate: func(value *ClientProofTranscript) { value.ClientSigningKeyID = []byte("different") }},
		{name: "client credential", mutate: func(value *ClientProofTranscript) { value.ClientCredentialHash = []byte("different") }},
		{name: "issued at", mutate: func(value *ClientProofTranscript) { value.IssuedAt = "2026-06-12T10:00:01Z" }},
		{name: "expires at", mutate: func(value *ClientProofTranscript) { value.ExpiresAt = "2026-06-12T10:00:59Z" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := transcript
			tt.mutate(&changed)
			if bytes.Equal(hash, changed.Hash()) {
				t.Fatalf("client transcript hash did not change after %s changed", tt.name)
			}
		})
	}
}

func TestWebTTYServerProofTranscriptHashChangesWithBoundFields(t *testing.T) {
	transcript := ServerProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             "websocket",
		WorkspaceID:           "workspace-1",
		ProjectID:             "project-1",
		ServerID:              "server-1",
		SessionID:             "session-1",
		ServerSigningKeyID:    []byte("server-signing-key-id"),
		ServerEncryptionKeyID: []byte("server-encryption-key-id"),
		ServerNonce:           []byte("nonce-1"),
		AuthRequirement:       AuthRequirementClientProof,
		PayloadSuites:         []PayloadCipherSuite{PayloadCipherSuiteAES256GCM},
		KeyEnvelopeSuites:     []KeyEnvelopeSuite{KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM},
		SignatureSuites:       []SignatureSuite{SignatureSuiteECDSAP256SHA256},
	}
	hash := transcript.Hash()
	if len(hash) != 32 {
		t.Fatalf("hash length = %d, want 32", len(hash))
	}
	if got := EncodeE2EKeyMaterial(hash); got != "uDy-1Y7s7aOa1dCrk9dQ0c3bA6mZWyO7_qZycuOPgT0" {
		t.Fatalf("server transcript hash = %s", got)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*ServerProofTranscript)
	}{
		{name: "transport", mutate: func(value *ServerProofTranscript) { value.Transport = "plain" }},
		{name: "workspace", mutate: func(value *ServerProofTranscript) { value.WorkspaceID = "workspace-2" }},
		{name: "project", mutate: func(value *ServerProofTranscript) { value.ProjectID = "project-2" }},
		{name: "server", mutate: func(value *ServerProofTranscript) { value.ServerID = "server-2" }},
		{name: "session", mutate: func(value *ServerProofTranscript) { value.SessionID = "session-2" }},
		{name: "server signing key", mutate: func(value *ServerProofTranscript) { value.ServerSigningKeyID = []byte("different") }},
		{name: "server encryption key", mutate: func(value *ServerProofTranscript) { value.ServerEncryptionKeyID = []byte("different") }},
		{name: "server nonce", mutate: func(value *ServerProofTranscript) { value.ServerNonce = []byte("different") }},
		{name: "auth requirement", mutate: func(value *ServerProofTranscript) { value.AuthRequirement = AuthRequirementNone }},
		{name: "payload suites", mutate: func(value *ServerProofTranscript) {
			value.PayloadSuites = []PayloadCipherSuite{PayloadCipherSuiteChaCha20Poly1305}
		}},
		{name: "key envelope suites", mutate: func(value *ServerProofTranscript) {
			value.KeyEnvelopeSuites = []KeyEnvelopeSuite{KeyEnvelopeSuiteHPKEX25519HKDFSHA256ChaCha20Poly1305}
		}},
		{name: "signature suites", mutate: func(value *ServerProofTranscript) {
			value.SignatureSuites = nil
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := transcript
			tt.mutate(&changed)
			if bytes.Equal(hash, changed.Hash()) {
				t.Fatalf("server transcript hash did not change after %s changed", tt.name)
			}
		})
	}
}

func TestWebTTYProofSigningAndVerification(t *testing.T) {
	privateKey, err := GenerateWebTTYSigningKey()
	if err != nil {
		t.Fatalf("GenerateWebTTYSigningKey() error = %v", err)
	}
	publicDER, err := MarshalWebTTYSigningPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("MarshalWebTTYSigningPublicKey() error = %v", err)
	}
	keyID := WebTTYSigningKeyID(publicDER)
	if len(keyID) != WebTTYSigningKeyIDSize {
		t.Fatalf("key id length = %d, want %d", len(keyID), WebTTYSigningKeyIDSize)
	}
	transcript := testClientProofTranscript(t)
	hash, signature, err := SignWebTTYClientProofTranscript(nil, privateKey, transcript)
	if err != nil {
		t.Fatalf("SignWebTTYClientProofTranscript() error = %v", err)
	}
	if !bytes.Equal(hash, transcript.Hash()) {
		t.Fatal("returned transcript hash does not match transcript")
	}
	if err := VerifyWebTTYClientProofTranscript(publicDER, transcript, signature); err != nil {
		t.Fatalf("VerifyWebTTYClientProofTranscript() error = %v", err)
	}
	changed := transcript
	changed.ClientPrincipalID = "other"
	if err := VerifyWebTTYClientProofTranscript(publicDER, changed, signature); err == nil {
		t.Fatal("VerifyWebTTYClientProofTranscript() succeeded with changed transcript")
	}
	otherPrivateKey, err := GenerateWebTTYSigningKey()
	if err != nil {
		t.Fatalf("GenerateWebTTYSigningKey() other error = %v", err)
	}
	otherPublicDER, err := MarshalWebTTYSigningPublicKey(&otherPrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("MarshalWebTTYSigningPublicKey() other error = %v", err)
	}
	if err := VerifyWebTTYClientProofTranscript(otherPublicDER, transcript, signature); err == nil {
		t.Fatal("VerifyWebTTYClientProofTranscript() succeeded with wrong key")
	}
}

func TestValidateClientProofTimeWindowRejectsReplayWindows(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		issuedAt  string
		expiresAt string
		want      string
	}{
		{
			name:      "valid",
			issuedAt:  now.Add(-time.Second).Format(time.RFC3339),
			expiresAt: now.Add(10 * time.Second).Format(time.RFC3339),
		},
		{
			name:      "bad issued at",
			issuedAt:  "not-a-time",
			expiresAt: now.Add(time.Minute).Format(time.RFC3339),
			want:      "issued_at is invalid",
		},
		{
			name:      "bad expires at",
			issuedAt:  now.Format(time.RFC3339),
			expiresAt: "not-a-time",
			want:      "expires_at is invalid",
		},
		{
			name:      "expired",
			issuedAt:  now.Add(-2 * time.Minute).Format(time.RFC3339),
			expiresAt: now.Add(-time.Second).Format(time.RFC3339),
			want:      "has expired",
		},
		{
			name:      "not yet valid",
			issuedAt:  now.Add(6 * time.Second).Format(time.RFC3339),
			expiresAt: now.Add(time.Minute).Format(time.RFC3339),
			want:      "not valid yet",
		},
		{
			name:      "lifetime too long",
			issuedAt:  now.Format(time.RFC3339),
			expiresAt: now.Add(webTTYProofTTL + time.Second).Format(time.RFC3339),
			want:      "lifetime exceeds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClientProofTimeWindow(&pb.ClientProof{
				IssuedAt:  tt.issuedAt,
				ExpiresAt: tt.expiresAt,
			}, now)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateClientProofTimeWindow() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateClientProofTimeWindow() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestHashWebTTYConfigIsDeterministic(t *testing.T) {
	config := &pb.Config{
		Options: &pb.Options{Interactive: true, AllocateTty: true, SendHeartbeat: true},
		CmdArgs: []string{"sh", "-lc", "whoami"},
		EnvVars: []*pb.Environment{{Key: "LANG", Value: "C"}},
		Workdir: &pb.Workdir{Value: "/tmp"},
	}
	first, err := HashWebTTYConfig(config)
	if err != nil {
		t.Fatalf("HashWebTTYConfig() first error = %v", err)
	}
	second, err := HashWebTTYConfig(config)
	if err != nil {
		t.Fatalf("HashWebTTYConfig() second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("HashWebTTYConfig() is not deterministic")
	}
	changed := protoCloneConfig(config)
	changed.CmdArgs[2] = "id"
	third, err := HashWebTTYConfig(changed)
	if err != nil {
		t.Fatalf("HashWebTTYConfig() changed error = %v", err)
	}
	if bytes.Equal(first, third) {
		t.Fatal("HashWebTTYConfig() did not change after command changed")
	}
}

func testClientProofTranscript(t *testing.T) ClientProofTranscript {
	t.Helper()
	configHash, err := HashWebTTYConfig(&pb.Config{
		Options: &pb.Options{Interactive: false, AllocateTty: false, SendHeartbeat: true},
		CmdArgs: []string{"sh", "-lc", "whoami"},
	})
	if err != nil {
		t.Fatalf("HashWebTTYConfig() error = %v", err)
	}
	grantHash, err := HashWebTTYSessionKeyGrant(&pb.SessionKeyGrant{
		PayloadSuite:     pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_AES_256_GCM,
		PayloadKeyId:     []byte("payload-key-id"),
		KeyEnvelopeSuite: pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_AES_256_GCM,
	})
	if err != nil {
		t.Fatalf("HashWebTTYSessionKeyGrant() error = %v", err)
	}
	return ClientProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             "websocket",
		WorkspaceID:           "workspace-1",
		ProjectID:             "project-1",
		ServerID:              "server-1",
		SessionID:             "session-1",
		ServerSigningKeyID:    []byte("server-signing-key-id"),
		ServerEncryptionKeyID: []byte("server-encryption-key-id"),
		ServerNonce:           []byte("nonce-1"),
		AuthRequirement:       AuthRequirementClientProof,
		PayloadSuite:          PayloadCipherSuiteAES256GCM,
		KeyEnvelopeSuite:      KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
		SessionKeyGrantHash:   grantHash,
		CommandConfigHash:     configHash,
		ClientPrincipalID:     "user-1",
		ClientSigningKeyID:    []byte("client-signing-key-id"),
		IssuedAt:              "2026-06-12T10:00:00Z",
		ExpiresAt:             "2026-06-12T10:01:00Z",
	}
}

func protoCloneConfig(src *pb.Config) *pb.Config {
	if src == nil {
		return nil
	}
	dst := &pb.Config{
		Options:  src.Options,
		CmdArgs:  append([]string(nil), src.CmdArgs...),
		Workdir:  src.Workdir,
		Username: src.Username,
	}
	if len(src.EnvVars) > 0 {
		dst.EnvVars = make([]*pb.Environment, 0, len(src.EnvVars))
		for _, item := range src.EnvVars {
			if item == nil {
				dst.EnvVars = append(dst.EnvVars, nil)
				continue
			}
			dst.EnvVars = append(dst.EnvVars, &pb.Environment{Key: item.Key, Value: item.Value})
		}
	}
	return dst
}

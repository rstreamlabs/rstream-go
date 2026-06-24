// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const webTTYProofTTL = 30 * time.Second

var (
	errWebTTYClientProofRequired     = errors.New("WebTTY client proof is required")
	errWebTTYClientProofInvalid      = errors.New("WebTTY client proof is invalid")
	errWebTTYClientProofUnauthorized = errors.New("WebTTY client signing key is not authorized")
)

func webTTYStringValue(value string) *wrapperspb.StringValue {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return wrapperspb.String(value)
}

func webTTYStringValueText(value *wrapperspb.StringValue) string {
	if value == nil {
		return ""
	}
	return value.GetValue()
}

func webTTYBytesValue(value []byte) *wrapperspb.BytesValue {
	if len(value) == 0 {
		return nil
	}
	return wrapperspb.Bytes(append([]byte(nil), value...))
}

func webTTYBytesValueBytes(value *wrapperspb.BytesValue) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value.GetValue()...)
}

func serverEndpointIdentityToProto(identity *WebTTYEndpointIdentity) *pb.EndpointIdentity {
	if identity == nil {
		return nil
	}
	return endpointIdentityPublicToProto(identity.Public())
}

func endpointIdentityPublicToProto(identity WebTTYEndpointIdentityPublic) *pb.EndpointIdentity {
	return &pb.EndpointIdentity{
		SigningKeyId:        cloneBytes(identity.SigningKeyID),
		SigningPublicKey:    cloneBytes(identity.SigningPublicKey),
		SignatureSuite:      pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256,
		EncryptionKeyId:     cloneBytes(identity.EncryptionKeyID),
		EncryptionPublicKey: cloneBytes(identity.EncryptionPublicKey),
		KeyEnvelopeSuite:    pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_AES_256_GCM,
	}
}

func webTTYAuthRequirement(cfg *ServerConfig) AuthRequirement {
	if cfg != nil && cfg.RequireClientProof != nil && *cfg.RequireClientProof {
		return AuthRequirementClientProof
	}
	return AuthRequirementNone
}

func webTTYAuthRequirementFromProto(value pb.AuthRequirement) AuthRequirement {
	switch value {
	case pb.AuthRequirement_AUTH_REQUIREMENT_CLIENT_PROOF:
		return AuthRequirementClientProof
	case pb.AuthRequirement_AUTH_REQUIREMENT_NONE:
		return AuthRequirementNone
	default:
		return AuthRequirementNone
	}
}

func (s *session) sendServerHelloIfConfigured() error {
	if s.cfg == nil || s.cfg.EndpointIdentity == nil {
		return nil
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate WebTTY server nonce: %w", err)
	}
	s.serverNonce = nonce
	identity := s.cfg.EndpointIdentity.Public()
	s.serverKeyID = EncodeE2EKeyMaterial(identity.SigningKeyID)
	workspaceID := strings.TrimSpace(s.cfg.WorkspaceID)
	projectID := strings.TrimSpace(s.cfg.ProjectID)
	serverID := strings.TrimSpace(s.cfg.ServerID)
	transcript := ServerProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             string(s.transport),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		ServerID:              serverID,
		SessionID:             s.sessionID,
		ServerSigningKeyID:    identity.SigningKeyID,
		ServerEncryptionKeyID: identity.EncryptionKeyID,
		ServerNonce:           nonce,
		AuthRequirement:       webTTYAuthRequirement(s.cfg),
		PayloadSuites:         []PayloadCipherSuite{PayloadCipherSuiteAES256GCM},
		KeyEnvelopeSuites:     []KeyEnvelopeSuite{KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM},
		SignatureSuites:       []SignatureSuite{SignatureSuiteECDSAP256SHA256},
	}
	privateKey, err := ParseWebTTYSigningPrivateKey(s.cfg.EndpointIdentity.Signing.PrivateKey)
	if err != nil {
		return err
	}
	transcriptHash, signature, err := SignWebTTYServerProofTranscript(rand.Reader, privateKey, transcript)
	if err != nil {
		return err
	}
	return s.writeMessage(&pb.Message{Payload: &pb.Message_ServerHello{ServerHello: &pb.ServerHello{
		ProtocolVersion:   pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1,
		SessionNonce:      cloneBytes(nonce),
		ServerIdentity:    endpointIdentityPublicToProto(identity),
		PayloadSuites:     []pb.PayloadCipherSuite{pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_AES_256_GCM},
		KeyEnvelopeSuites: []pb.KeyEnvelopeSuite{pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_AES_256_GCM},
		SignatureSuites:   []pb.SignatureSuite{pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256},
		AuthRequirement:   pb.AuthRequirement(webTTYAuthRequirement(s.cfg)),
		WorkspaceId:       webTTYStringValue(workspaceID),
		ProjectId:         webTTYStringValue(projectID),
		ServerId:          webTTYStringValue(serverID),
		SessionId:         s.sessionID,
		ServerProof: &pb.ServerProof{
			SignatureSuite: pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256,
			SigningKeyId:   cloneBytes(identity.SigningKeyID),
			TranscriptHash: cloneBytes(transcriptHash),
			Signature:      cloneBytes(signature),
		},
	}}})
}

func (s *session) verifyClientProof(ctx context.Context, openCfg *pb.Open) error {
	if s.cfg == nil || s.cfg.RequireClientProof == nil || !*s.cfg.RequireClientProof {
		return nil
	}
	if openCfg == nil || openCfg.ClientProof == nil {
		return errWebTTYClientProofRequired
	}
	if s.cfg.EndpointIdentity == nil {
		return fmt.Errorf("WebTTY server endpoint identity is required when client proof is required")
	}
	proof := openCfg.ClientProof
	if len(proof.SigningKeyId) != WebTTYSigningKeyIDSize {
		return fmt.Errorf("%w: invalid signing key id", errWebTTYClientProofInvalid)
	}
	expectedSigningKeyID := WebTTYSigningKeyID(proof.SigningPublicKey)
	if !bytes.Equal(proof.SigningKeyId, expectedSigningKeyID) {
		return fmt.Errorf("%w: signing key id does not match signing public key", errWebTTYClientProofInvalid)
	}
	s.clientKeyID = EncodeE2EKeyMaterial(proof.SigningKeyId)
	s.clientPrincipal = strings.TrimSpace(webTTYStringValueText(proof.GetPrincipalId()))
	s.clientDeviceID = strings.TrimSpace(webTTYStringValueText(proof.GetDeviceId()))
	s.clientBrowserID = strings.TrimSpace(webTTYStringValueText(proof.GetBrowserId()))
	if s.logger != nil && s.logger.Enabled(ctx, slog.LevelDebug) {
		s.logger.Debug(
			"verifying WebTTY client proof",
			"client_signing_key_id", EncodeE2EKeyMaterial(proof.SigningKeyId),
			"authorized_client_signing_key_ids", authorizedWebTTYClientSigningKeyIDs(s.cfg.AuthorizedClientSigningKeys),
		)
	}
	now := time.Now().UTC()
	if err := validateClientProofTimeWindow(proof, now); err != nil {
		return fmt.Errorf("%w: %v", errWebTTYClientProofInvalid, err)
	}
	sessionKeyGrantHash, err := HashWebTTYSessionKeyGrant(openCfg.SessionKeyGrant)
	if err != nil {
		return fmt.Errorf("%w: %v", errWebTTYClientProofInvalid, err)
	}
	configHash, err := HashWebTTYConfig(openCfg.Config)
	if err != nil {
		return fmt.Errorf("%w: %v", errWebTTYClientProofInvalid, err)
	}
	credential := webTTYBytesValueBytes(proof.GetCredential())
	credentialHash := HashWebTTYClientCredential(credential)
	serverIdentity := s.cfg.EndpointIdentity.Public()
	transcript := ClientProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             string(s.transport),
		WorkspaceID:           s.cfg.WorkspaceID,
		ProjectID:             s.cfg.ProjectID,
		ServerID:              s.cfg.ServerID,
		SessionID:             s.sessionID,
		ServerSigningKeyID:    serverIdentity.SigningKeyID,
		ServerEncryptionKeyID: serverIdentity.EncryptionKeyID,
		ServerNonce:           s.serverNonce,
		AuthRequirement:       AuthRequirementClientProof,
		PayloadSuite:          PayloadCipherSuiteAES256GCM,
		KeyEnvelopeSuite:      KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
		SessionKeyGrantHash:   sessionKeyGrantHash,
		CommandConfigHash:     configHash,
		ClientPrincipalID:     webTTYStringValueText(proof.GetPrincipalId()),
		ClientSigningKeyID:    proof.SigningKeyId,
		ClientCredentialHash:  credentialHash,
		IssuedAt:              proof.IssuedAt,
		ExpiresAt:             proof.ExpiresAt,
	}
	expectedTranscriptHash := transcript.Hash()
	if !bytes.Equal(proof.TranscriptHash, expectedTranscriptHash) {
		if s.logger != nil && s.logger.Enabled(ctx, slog.LevelDebug) {
			s.logger.Debug(
				"WebTTY client proof transcript hash mismatch",
				"expected_transcript_hash", base64.RawURLEncoding.EncodeToString(expectedTranscriptHash),
				"provided_transcript_hash", base64.RawURLEncoding.EncodeToString(proof.TranscriptHash),
				"transport", transcript.Transport,
				"workspace_id", transcript.WorkspaceID,
				"project_id", transcript.ProjectID,
				"server_id", transcript.ServerID,
				"session_id", transcript.SessionID,
				"client_principal_id", transcript.ClientPrincipalID,
				"client_signing_key_id", EncodeE2EKeyMaterial(transcript.ClientSigningKeyID),
				"credential_hash", base64.RawURLEncoding.EncodeToString(transcript.ClientCredentialHash),
				"config_hash", base64.RawURLEncoding.EncodeToString(transcript.CommandConfigHash),
				"session_key_grant_hash", base64.RawURLEncoding.EncodeToString(transcript.SessionKeyGrantHash),
			)
		}
		return fmt.Errorf("%w: transcript hash does not match", errWebTTYClientProofInvalid)
	}
	if err := VerifyWebTTYClientProofTranscript(proof.SigningPublicKey, transcript, proof.Signature); err != nil {
		return fmt.Errorf("%w: signature is invalid: %v", errWebTTYClientProofInvalid, err)
	}
	var authorizedPublicKey []byte
	if s.cfg.ClientProofVerifier != nil {
		var err error
		authorizedPublicKey, err = s.cfg.ClientProofVerifier(ctx, ClientProofVerification{
			Proof:      proof,
			Credential: credential,
			Transcript: transcript,
		})
		if err != nil {
			return err
		}
	} else {
		authorizedPublicKey = s.cfg.AuthorizedClientSigningKeys[string(proof.SigningKeyId)]
	}
	if len(authorizedPublicKey) == 0 && s.cfg.ClientProofVerifier == nil && s.cfg.AuthorizedClientSigningKey != nil {
		var err error
		authorizedPublicKey, err = s.cfg.AuthorizedClientSigningKey(ctx, proof.SigningKeyId)
		if err != nil {
			return err
		}
	}
	if len(authorizedPublicKey) == 0 {
		return errWebTTYClientProofUnauthorized
	}
	if !bytes.Equal(authorizedPublicKey, proof.SigningPublicKey) {
		return fmt.Errorf("%w: signing key does not match the authorized key", errWebTTYClientProofInvalid)
	}
	return nil
}

func authorizedWebTTYClientSigningKeyIDs(keys map[string][]byte) []string {
	ids := make([]string, 0, len(keys))
	for key := range keys {
		ids = append(ids, EncodeE2EKeyMaterial([]byte(key)))
	}
	sort.Strings(ids)
	return ids
}

func validateClientProofTimeWindow(proof *pb.ClientProof, now time.Time) error {
	issuedAt, err := time.Parse(time.RFC3339, proof.IssuedAt)
	if err != nil {
		return fmt.Errorf("WebTTY client proof issued_at is invalid: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, proof.ExpiresAt)
	if err != nil {
		return fmt.Errorf("WebTTY client proof expires_at is invalid: %w", err)
	}
	if expiresAt.Before(now) {
		return fmt.Errorf("WebTTY client proof has expired")
	}
	if issuedAt.After(now.Add(5 * time.Second)) {
		return fmt.Errorf("WebTTY client proof is not valid yet")
	}
	if expiresAt.Sub(issuedAt) > webTTYProofTTL {
		return fmt.Errorf("WebTTY client proof lifetime exceeds %s", webTTYProofTTL)
	}
	return nil
}

func (c *clientRuntime) verifyServerHello(hello *pb.ServerHello, transport WebTTYTransport) error {
	if c.cfg.ExpectedServerIdentity == nil {
		return nil
	}
	if hello == nil {
		return fmt.Errorf("WebTTY server hello is required")
	}
	expected := c.cfg.ExpectedServerIdentity
	actual := hello.GetServerIdentity()
	if actual == nil {
		return fmt.Errorf("WebTTY server hello is missing server identity")
	}
	if !bytes.Equal(actual.SigningKeyId, expected.SigningKeyID) ||
		!bytes.Equal(actual.SigningPublicKey, expected.SigningPublicKey) ||
		!bytes.Equal(actual.EncryptionKeyId, expected.EncryptionKeyID) ||
		!bytes.Equal(actual.EncryptionPublicKey, expected.EncryptionPublicKey) {
		return fmt.Errorf("WebTTY server identity does not match the expected identity")
	}
	if hello.ServerProof == nil {
		return fmt.Errorf("WebTTY server proof is required")
	}
	transcript := ServerProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             string(transport),
		WorkspaceID:           webTTYStringValueText(hello.GetWorkspaceId()),
		ProjectID:             webTTYStringValueText(hello.GetProjectId()),
		ServerID:              webTTYStringValueText(hello.GetServerId()),
		SessionID:             hello.SessionId,
		ServerSigningKeyID:    actual.SigningKeyId,
		ServerEncryptionKeyID: actual.EncryptionKeyId,
		ServerNonce:           hello.SessionNonce,
		AuthRequirement:       webTTYAuthRequirementFromProto(hello.AuthRequirement),
		PayloadSuites:         payloadSuitesFromProto(hello.PayloadSuites),
		KeyEnvelopeSuites:     keyEnvelopeSuitesFromProto(hello.KeyEnvelopeSuites),
		SignatureSuites:       signatureSuitesFromProto(hello.SignatureSuites),
	}
	if !bytes.Equal(hello.ServerProof.TranscriptHash, transcript.Hash()) {
		return fmt.Errorf("WebTTY server proof transcript hash does not match")
	}
	if err := VerifyWebTTYServerProofTranscript(actual.SigningPublicKey, transcript, hello.ServerProof.Signature); err != nil {
		return fmt.Errorf("WebTTY server proof signature is invalid: %w", err)
	}
	return nil
}

func (c *clientRuntime) clientProofForOpen(openCfg *pb.Open, hello *pb.ServerHello, transport WebTTYTransport) (*pb.ClientProof, error) {
	if hello == nil || hello.AuthRequirement != pb.AuthRequirement_AUTH_REQUIREMENT_CLIENT_PROOF {
		return nil, nil
	}
	if c.cfg.EndpointIdentity == nil {
		return nil, fmt.Errorf("WebTTY server requires a client proof, but no client endpoint identity is configured")
	}
	if c.cfg.ExpectedServerIdentity == nil {
		return nil, fmt.Errorf("WebTTY server requires a client proof, but no expected server endpoint identity is configured")
	}
	serverIdentity := hello.GetServerIdentity()
	if serverIdentity == nil {
		return nil, fmt.Errorf("WebTTY server requires a client proof, but the server hello is missing server identity")
	}
	sessionKeyGrantHash, err := HashWebTTYSessionKeyGrant(openCfg.SessionKeyGrant)
	if err != nil {
		return nil, err
	}
	configHash, err := HashWebTTYConfig(openCfg.Config)
	if err != nil {
		return nil, err
	}
	issuedAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(webTTYProofTTL)
	clientPrincipalID := strings.TrimSpace(c.cfg.ClientPrincipalID)
	clientCredential := append([]byte(nil), c.cfg.ClientCredential...)
	transcript := ClientProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             string(transport),
		WorkspaceID:           webTTYStringValueText(hello.GetWorkspaceId()),
		ProjectID:             webTTYStringValueText(hello.GetProjectId()),
		ServerID:              webTTYStringValueText(hello.GetServerId()),
		SessionID:             hello.SessionId,
		ServerSigningKeyID:    serverIdentity.GetSigningKeyId(),
		ServerEncryptionKeyID: serverIdentity.GetEncryptionKeyId(),
		ServerNonce:           hello.SessionNonce,
		AuthRequirement:       AuthRequirementClientProof,
		PayloadSuite:          PayloadCipherSuiteAES256GCM,
		KeyEnvelopeSuite:      KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
		SessionKeyGrantHash:   sessionKeyGrantHash,
		CommandConfigHash:     configHash,
		ClientPrincipalID:     clientPrincipalID,
		ClientSigningKeyID:    c.cfg.EndpointIdentity.Signing.KeyID,
		ClientCredentialHash:  HashWebTTYClientCredential(clientCredential),
		IssuedAt:              issuedAt.Format(time.RFC3339),
		ExpiresAt:             expiresAt.Format(time.RFC3339),
	}
	privateKey, err := ParseWebTTYSigningPrivateKey(c.cfg.EndpointIdentity.Signing.PrivateKey)
	if err != nil {
		return nil, err
	}
	transcriptHash, signature, err := SignWebTTYClientProofTranscript(rand.Reader, privateKey, transcript)
	if err != nil {
		return nil, err
	}
	return &pb.ClientProof{
		PrincipalId:      webTTYStringValue(clientPrincipalID),
		SigningKeyId:     cloneBytes(c.cfg.EndpointIdentity.Signing.KeyID),
		SigningPublicKey: cloneBytes(c.cfg.EndpointIdentity.Signing.PublicKey),
		SignatureSuite:   pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256,
		TranscriptHash:   transcriptHash,
		Signature:        signature,
		IssuedAt:         transcript.IssuedAt,
		ExpiresAt:        transcript.ExpiresAt,
		DeviceId:         webTTYStringValue(c.cfg.ClientDeviceID),
		BrowserId:        webTTYStringValue(c.cfg.ClientBrowserID),
		Credential:       webTTYBytesValue(clientCredential),
	}, nil
}

func (c *clientRuntime) clientProofForAttach(attachCfg *pb.Attach, transport WebTTYTransport) (*pb.ClientProof, error) {
	if c == nil || c.cfg == nil {
		return nil, nil
	}
	credentialConfigured := len(c.cfg.ClientCredential) > 0
	identityConfigured := c.cfg.EndpointIdentity != nil
	if !credentialConfigured && !identityConfigured {
		return nil, nil
	}
	if c.cfg.EndpointIdentity == nil {
		return nil, fmt.Errorf("WebTTY attach client proof requires a client endpoint identity")
	}
	if c.cfg.Attach == nil {
		return nil, fmt.Errorf("WebTTY attach config is required")
	}
	if attachCfg == nil {
		return nil, fmt.Errorf("WebTTY attach message is required")
	}
	issuedAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(webTTYProofTTL)
	clientPrincipalID := strings.TrimSpace(c.cfg.ClientPrincipalID)
	clientCredential := append([]byte(nil), c.cfg.ClientCredential...)
	transcript := ClientProofTranscript{
		ProtocolVersion:      ProtocolVersionWebTTY1,
		Transport:            string(transport),
		WorkspaceID:          strings.TrimSpace(c.cfg.Attach.WorkspaceID),
		ProjectID:            strings.TrimSpace(c.cfg.Attach.ProjectID),
		ServerID:             strings.TrimSpace(c.cfg.Attach.ServerID),
		SessionID:            strings.TrimSpace(attachCfg.SessionId),
		AuthRequirement:      AuthRequirementClientProof,
		PayloadSuite:         PayloadCipherSuiteAES256GCM,
		KeyEnvelopeSuite:     KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
		AttachGrantHash:      HashWebTTYAttachGrant(attachCfg.AttachGrant),
		RequestedRole:        attachRoleTranscriptValue(attachCfg.RequestedRole),
		ClientPrincipalID:    clientPrincipalID,
		ClientSigningKeyID:   c.cfg.EndpointIdentity.Signing.KeyID,
		ClientCredentialHash: HashWebTTYClientCredential(clientCredential),
		IssuedAt:             issuedAt.Format(time.RFC3339),
		ExpiresAt:            expiresAt.Format(time.RFC3339),
	}
	privateKey, err := ParseWebTTYSigningPrivateKey(c.cfg.EndpointIdentity.Signing.PrivateKey)
	if err != nil {
		return nil, err
	}
	transcriptHash, signature, err := SignWebTTYClientProofTranscript(rand.Reader, privateKey, transcript)
	if err != nil {
		return nil, err
	}
	return &pb.ClientProof{
		PrincipalId:      webTTYStringValue(clientPrincipalID),
		SigningKeyId:     cloneBytes(c.cfg.EndpointIdentity.Signing.KeyID),
		SigningPublicKey: cloneBytes(c.cfg.EndpointIdentity.Signing.PublicKey),
		SignatureSuite:   pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256,
		TranscriptHash:   transcriptHash,
		Signature:        signature,
		IssuedAt:         transcript.IssuedAt,
		ExpiresAt:        transcript.ExpiresAt,
		DeviceId:         webTTYStringValue(c.cfg.ClientDeviceID),
		BrowserId:        webTTYStringValue(c.cfg.ClientBrowserID),
		Credential:       webTTYBytesValue(clientCredential),
	}, nil
}

func attachRoleTranscriptValue(role pb.AttachRole) string {
	switch role {
	case pb.AttachRole_ATTACH_ROLE_CONTROLLER:
		return string(AttachRoleController)
	case pb.AttachRole_ATTACH_ROLE_SPECTATOR, pb.AttachRole_ATTACH_ROLE_UNSPECIFIED:
		return string(AttachRoleSpectator)
	default:
		return strings.TrimSpace(role.String())
	}
}

func payloadSuitesFromProto(values []pb.PayloadCipherSuite) []PayloadCipherSuite {
	out := make([]PayloadCipherSuite, 0, len(values))
	for _, value := range values {
		out = append(out, PayloadCipherSuite(value))
	}
	return out
}

func keyEnvelopeSuitesFromProto(values []pb.KeyEnvelopeSuite) []KeyEnvelopeSuite {
	out := make([]KeyEnvelopeSuite, 0, len(values))
	for _, value := range values {
		out = append(out, KeyEnvelopeSuite(value))
	}
	return out
}

func signatureSuitesFromProto(values []pb.SignatureSuite) []SignatureSuite {
	out := make([]SignatureSuite, 0, len(values))
	for _, value := range values {
		out = append(out, SignatureSuite(value))
	}
	return out
}

func waitForClientEvent(ctx context.Context, timeoutDuration *time.Duration, eventCh <-chan clientEvent) (clientEvent, error) {
	var timer *time.Timer
	var timeout <-chan time.Time
	if timeoutDuration != nil && *timeoutDuration > 0 {
		timer = time.NewTimer(*timeoutDuration)
		timeout = timer.C
		defer func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}()
	}
	select {
	case event := <-eventCh:
		if event.err != nil {
			return clientEvent{}, event.err
		}
		return event, nil
	case <-timeout:
		return clientEvent{}, errClientOperationTimeout
	case <-ctx.Done():
		return clientEvent{}, ctx.Err()
	}
}

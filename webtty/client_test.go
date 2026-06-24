// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNormalizeWebTTYURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default scheme", input: "127.0.0.1:8080", want: "ws://127.0.0.1:8080/"},
		{name: "ws scheme", input: "ws://localhost:8080/path", want: "ws://localhost:8080/path"},
		{name: "wss scheme", input: "wss://example.com", want: "wss://example.com/"},
		{name: "rstrm scheme", input: "rstrm://shell", want: "ws://shell/"},
		{name: "invalid scheme", input: "http://localhost", wantErr: true},
		{name: "missing host", input: "ws:///path", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeWebTTYURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeWebTTYURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected normalized url: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestResolveWebTTYEndpoint(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		wantURL            string
		wantRequiresCustom bool
		wantErr            bool
	}{
		{name: "ws endpoint", input: "ws://localhost:8080/path", wantURL: "ws://localhost:8080/path", wantRequiresCustom: false},
		{name: "wss endpoint", input: "wss://example.com", wantURL: "wss://example.com/", wantRequiresCustom: false},
		{name: "rstrm endpoint", input: "rstrm://shell", wantURL: "ws://shell/", wantRequiresCustom: true},
		{name: "rstrm path", input: "rstrm://shell/session?mode=ro", wantURL: "ws://shell/session?mode=ro", wantRequiresCustom: true},
		{name: "invalid endpoint", input: "http://localhost", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWebTTYEndpoint(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWebTTYEndpoint returned error: %v", err)
			}
			if got.URL != tt.wantURL {
				t.Fatalf("unexpected normalized url: got %q want %q", got.URL, tt.wantURL)
			}
			if got.RequiresCustomDial != tt.wantRequiresCustom {
				t.Fatalf("unexpected custom dial requirement: got %t want %t", got.RequiresCustomDial, tt.wantRequiresCustom)
			}
		})
	}
}

func TestResolvePlainWebTTYEndpoint(t *testing.T) {
	tcp, err := resolveWebTTYEndpointWithTransport("tcp://127.0.0.1:2222", WebTTYTransportPlain)
	if err != nil {
		t.Fatalf("resolve tcp plain endpoint: %v", err)
	}
	if tcp.Transport != WebTTYTransportPlain || tcp.Address != "127.0.0.1:2222" || tcp.TLS || tcp.RequiresCustomDial {
		t.Fatalf("unexpected tcp plain endpoint: %#v", tcp)
	}
	bare, err := resolveWebTTYEndpointWithTransport("127.0.0.1:2222", WebTTYTransportPlain)
	if err != nil {
		t.Fatalf("resolve bare plain endpoint: %v", err)
	}
	if bare.Transport != WebTTYTransportPlain || bare.Address != "127.0.0.1:2222" || bare.TLS || bare.RequiresCustomDial {
		t.Fatalf("unexpected bare plain endpoint: %#v", bare)
	}
	tlsEndpoint, err := resolveWebTTYEndpointWithTransport("tls://terminal.example", WebTTYTransportPlain)
	if err != nil {
		t.Fatalf("resolve tls plain endpoint: %v", err)
	}
	if tlsEndpoint.Transport != WebTTYTransportPlain || tlsEndpoint.Address != "terminal.example:443" || !tlsEndpoint.TLS {
		t.Fatalf("unexpected tls plain endpoint: %#v", tlsEndpoint)
	}
	rstrm, err := resolveWebTTYEndpointWithTransport("rstrm://shell", WebTTYTransportPlain)
	if err != nil {
		t.Fatalf("resolve rstrm plain endpoint: %v", err)
	}
	if rstrm.Transport != WebTTYTransportPlain || rstrm.Address != "shell" || !rstrm.RequiresCustomDial {
		t.Fatalf("unexpected rstrm plain endpoint: %#v", rstrm)
	}
}

func TestResolveWebTransportWebTTYEndpoint(t *testing.T) {
	httpsEndpoint, err := resolveWebTTYEndpointWithTransport("https://terminal.example/session", WebTTYTransportWebTransport)
	if err != nil {
		t.Fatalf("resolve https webtransport endpoint: %v", err)
	}
	if httpsEndpoint.Transport != WebTTYTransportWebTransport || httpsEndpoint.URL != "https://terminal.example/session" || httpsEndpoint.RequiresCustomDial {
		t.Fatalf("unexpected https webtransport endpoint: %#v", httpsEndpoint)
	}
	defaultEndpoint, err := resolveWebTTYEndpointWithTransport("terminal.example", WebTTYTransportWebTransport)
	if err != nil {
		t.Fatalf("resolve default webtransport endpoint: %v", err)
	}
	if defaultEndpoint.URL != "https://terminal.example/" {
		t.Fatalf("unexpected default webtransport url: %#v", defaultEndpoint)
	}
	rstrmEndpoint, err := resolveWebTTYEndpointWithTransport("rstrm://shell", WebTTYTransportWebTransport)
	if err != nil {
		t.Fatalf("resolve rstrm webtransport endpoint: %v", err)
	}
	if rstrmEndpoint.URL != "https://shell/" || !rstrmEndpoint.RequiresCustomDial || rstrmEndpoint.Address != "shell" {
		t.Fatalf("unexpected rstrm webtransport endpoint: %#v", rstrmEndpoint)
	}
}

func TestParseClientEnvVars(t *testing.T) {
	t.Setenv("RSTREAM_TEST_ENV", "from-env")
	values, err := parseClientEnvVars([]string{"A=1", "RSTREAM_TEST_ENV", "EMPTY="})
	if err != nil {
		t.Fatalf("parseClientEnvVars returned error: %v", err)
	}
	if len(values) != 3 {
		t.Fatalf("unexpected env var count: got %d want 3", len(values))
	}
	if values[0].Key != "A" || values[0].Value != "1" {
		t.Fatalf("unexpected first env var: %#v", values[0])
	}
	if values[1].Key != "RSTREAM_TEST_ENV" || values[1].Value != "from-env" {
		t.Fatalf("unexpected second env var: %#v", values[1])
	}
	if values[2].Key != "EMPTY" || values[2].Value != "" {
		t.Fatalf("unexpected third env var: %#v", values[2])
	}
}

func TestParseClientUsername(t *testing.T) {
	idValue := "42"
	id, err := parseClientUsername(&idValue)
	if err != nil {
		t.Fatalf("parseClientUsername(id) returned error: %v", err)
	}
	if id.GetId() != 42 {
		t.Fatalf("unexpected numeric id: got %d want 42", id.GetId())
	}
	nameValue := "alice"
	name, err := parseClientUsername(&nameValue)
	if err != nil {
		t.Fatalf("parseClientUsername(name) returned error: %v", err)
	}
	if name.GetName() != "alice" {
		t.Fatalf("unexpected username: got %q want %q", name.GetName(), "alice")
	}
}

func TestResolveClientConfigDefaults(t *testing.T) {
	cfg, err := resolveClientConfig(nil)
	if err != nil {
		t.Fatalf("resolveClientConfig returned error: %v", err)
	}
	if got, want := cfg.URL, "ws://127.0.0.1:8080"; got != want {
		t.Fatalf("unexpected default url: got %q want %q", got, want)
	}
	if got, want := *cfg.OpenDeadline, 5*time.Second; got != want {
		t.Fatalf("unexpected open deadline: got %s want %s", got, want)
	}
	if got, want := *cfg.CloseDeadline, 5*time.Second; got != want {
		t.Fatalf("unexpected close deadline: got %s want %s", got, want)
	}
	if got, want := *cfg.HeartbeatInterval, 5*time.Second; got != want {
		t.Fatalf("unexpected heartbeat interval: got %s want %s", got, want)
	}
}

func TestHandleOpenMessage(t *testing.T) {
	runtime := &clientRuntime{}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}}); err != nil {
		t.Fatalf("handleOpenMessage(ack) returned error: %v", err)
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: "server error"}}}); err == nil {
		t.Fatalf("handleOpenMessage(error) returned nil")
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Error{}}); !errors.Is(err, errClientServer) {
		t.Fatalf("handleOpenMessage(empty error) = %v", err)
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_ServerHello{ServerHello: &pb.ServerHello{}}}); err == nil || !strings.Contains(err.Error(), "known server endpoint identity") {
		t.Fatalf("handleOpenMessage(server hello) = %v", err)
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_ProtocolError{ProtocolError: &pb.ProtocolError{Code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_UNAUTHORIZED}}}); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("handleOpenMessage(protocol error) = %v", err)
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}}); err == nil {
		t.Fatalf("handleOpenMessage(close) returned nil")
	}
}

func TestHandleSessionMessage(t *testing.T) {
	var stdout bytes.Buffer
	runtime := &clientRuntime{cfg: &ClientConfig{Stdout: &stdout, Stderr: &bytes.Buffer{}}}
	exitCode, done, err := runtime.handleSessionMessage(context.TODO(), &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("ok")}}}})
	if err != nil {
		t.Fatalf("handleSessionMessage(data) returned error: %v", err)
	}
	if done {
		t.Fatalf("handleSessionMessage(data) unexpectedly finished the session")
	}
	if exitCode != -1 {
		t.Fatalf("unexpected exit code for data: got %d want -1", exitCode)
	}
	if got := stdout.String(); got != "ok" {
		t.Fatalf("unexpected stdout payload: got %q want %q", got, "ok")
	}
	exitCode, done, err = runtime.handleSessionMessage(context.TODO(), &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 7}}})
	if err != nil {
		t.Fatalf("handleSessionMessage(close) returned error: %v", err)
	}
	if !done {
		t.Fatalf("handleSessionMessage(close) did not finish the session")
	}
	if exitCode != 7 {
		t.Fatalf("unexpected exit code for close: got %d want 7", exitCode)
	}
	if _, done, err := runtime.handleSessionMessage(context.TODO(), &pb.Message{Payload: &pb.Message_Close{}}); !errors.Is(err, errClientProtocol) || !done {
		t.Fatalf("handleSessionMessage(empty close) = done %v err %v", done, err)
	}
	if _, done, err := runtime.handleSessionMessage(context.TODO(), &pb.Message{Payload: &pb.Message_Error{}}); !errors.Is(err, errClientServer) || !done {
		t.Fatalf("handleSessionMessage(empty error) = done %v err %v", done, err)
	}
	if _, done, err := runtime.handleSessionMessage(context.TODO(), &pb.Message{Payload: &pb.Message_ProtocolError{ProtocolError: &pb.ProtocolError{Code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED}}}); err == nil || !strings.Contains(err.Error(), "client proof") || !done {
		t.Fatalf("handleSessionMessage(protocol error) = done %v err %v", done, err)
	}
	if _, done, err := runtime.handleSessionMessage(context.TODO(), &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}}); err == nil || !done {
		t.Fatalf("handleSessionMessage(ack) should fail and finish the session")
	}
}

func TestBuildOpenMessage(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	workdir := " /tmp/work "
	username := "1001"
	runtime := &clientRuntime{cfg: &ClientConfig{
		Interactive:   true,
		AllocateTTY:   true,
		SendHeartbeat: true,
		EnvVars:       []string{"A=1"},
		Workdir:       &workdir,
		Username:      &username,
		CmdArgs:       []string{"bash", "-lc", "echo ok"},
	}}
	msg, err := runtime.buildOpenMessage(WebTTYTransportWebSocket, nil)
	if err != nil {
		t.Fatalf("buildOpenMessage(WebTTYTransportWebSocket, nil) error = %v", err)
	}
	open := msg.GetOpen()
	if open == nil || open.Config == nil {
		t.Fatalf("expected open config, got %#v", msg)
	}
	if !open.Config.Options.Interactive || !open.Config.Options.AllocateTty || !open.Config.Options.SendHeartbeat {
		t.Fatalf("options not applied: %#v", open.Config.Options)
	}
	if open.Config.Workdir == nil || open.Config.Workdir.Value != "/tmp/work" {
		t.Fatalf("workdir not trimmed: %#v", open.Config.Workdir)
	}
	if open.Config.Username.GetId() != 1001 {
		t.Fatalf("username not parsed as numeric id: %#v", open.Config.Username)
	}
	if len(open.Config.CmdArgs) != 3 || open.Config.CmdArgs[0] != "bash" {
		t.Fatalf("cmd args not copied: %#v", open.Config.CmdArgs)
	}
	env := map[string]string{}
	for _, item := range open.Config.EnvVars {
		env[item.Key] = item.Value
	}
	if env["A"] != "1" || env["TERM"] != "xterm-256color" {
		t.Fatalf("environment not built correctly: %#v", env)
	}
}

func TestBuildOpenMessageAdvertisesPayloadCrypto(t *testing.T) {
	runtime := &clientRuntime{cfg: &ClientConfig{
		PayloadCrypto: &PayloadCrypto{
			Capabilities: []OpenCapability{OpenCapabilitySessionKeyGrant},
			SessionKeyGrant: &SessionKeyGrant{
				PayloadSuite:     PayloadCipherSuiteAES256GCM,
				PayloadKeyID:     []byte("workspace-key"),
				KeyEnvelopeSuite: KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
				KeyEnvelopes: []KeyEnvelope{{
					RecipientKeyID:  []byte{1, 2},
					EncapsulatedKey: []byte{3, 4},
					WrappedKey:      []byte{5, 6},
				}},
				KeyContext: []byte{7, 8},
			},
			EncryptStdin: func(context.Context, []byte) (*EncryptedPayload, error) {
				return &EncryptedPayload{Ciphertext: []byte("unused"), PlaintextLength: 6}, nil
			},
		},
	}}
	msg, err := runtime.buildOpenMessage(WebTTYTransportWebSocket, nil)
	if err != nil {
		t.Fatalf("buildOpenMessage(WebTTYTransportWebSocket, nil) error = %v", err)
	}
	open := msg.GetOpen()
	if len(open.Capabilities) != 2 {
		t.Fatalf("capabilities = %#v, want 2 entries", open.Capabilities)
	}
	if open.Capabilities[0] != pb.OpenCapability_OPEN_CAPABILITY_ENCRYPTED_PAYLOAD {
		t.Fatalf("encrypted payload capability missing: %#v", open.Capabilities)
	}
	if open.Capabilities[1] != pb.OpenCapability_OPEN_CAPABILITY_SESSION_CRYPTO {
		t.Fatalf("session key grant capability missing: %#v", open.Capabilities)
	}
	if open.SessionKeyGrant.GetPayloadSuite() != pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_AES_256_GCM ||
		!bytes.Equal(open.SessionKeyGrant.GetPayloadKeyId(), []byte("workspace-key")) ||
		open.SessionKeyGrant.GetKeyEnvelopeSuite() != pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_AES_256_GCM {
		t.Fatalf("session key grant not encoded: %#v", open.SessionKeyGrant)
	}
	if len(open.SessionKeyGrant.KeyEnvelopes) != 1 ||
		!bytes.Equal(open.SessionKeyGrant.KeyEnvelopes[0].RecipientKeyId, []byte{1, 2}) ||
		!bytes.Equal(open.SessionKeyGrant.KeyEnvelopes[0].EncapsulatedKey, []byte{3, 4}) ||
		!bytes.Equal(open.SessionKeyGrant.KeyEnvelopes[0].WrappedKey, []byte{5, 6}) ||
		!bytes.Equal(open.SessionKeyGrant.KeyContext, []byte{7, 8}) {
		t.Fatalf("session key grant bytes not copied: %#v", open.SessionKeyGrant)
	}
}

func TestBuildOpenMessageIncludesClientProofBoundToOpenAndServerHello(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	hello := &pb.ServerHello{
		ProtocolVersion: pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1,
		SessionNonce:    []byte("server-nonce"),
		ServerIdentity:  endpointIdentityPublicToProto(serverPublic),
		AuthRequirement: pb.AuthRequirement_AUTH_REQUIREMENT_CLIENT_PROOF,
		WorkspaceId:     wrapperspb.String("workspace-1"),
		ProjectId:       wrapperspb.String("project-1"),
		ServerId:        wrapperspb.String("server-1"),
		SessionId:       "session-1",
	}
	credential := []byte("workspace-credential")
	runtime := &clientRuntime{cfg: &ClientConfig{
		CmdArgs:                []string{"sh", "-lc", "echo ok"},
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientCredential:       credential,
		ClientPrincipalID:      "device-1",
		ClientDeviceID:         "device-1",
		PayloadCrypto: &PayloadCrypto{
			Capabilities: []OpenCapability{OpenCapabilitySessionKeyGrant},
			SessionKeyGrant: &SessionKeyGrant{
				PayloadSuite:     PayloadCipherSuiteAES256GCM,
				PayloadKeyID:     []byte("payload-key-id"),
				KeyEnvelopeSuite: KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
				KeyEnvelopes: []KeyEnvelope{{
					RecipientKeyID:  serverIdentity.Encryption.KeyID,
					EncapsulatedKey: []byte("encapsulated-key"),
					WrappedKey:      []byte("wrapped-key"),
				}},
				KeyContext: []byte("key-context"),
			},
		},
	}}
	msg, err := runtime.buildOpenMessage(WebTTYTransportWebSocket, hello)
	if err != nil {
		t.Fatalf("buildOpenMessage(WebTTYTransportWebSocket, hello) error = %v", err)
	}
	open := msg.GetOpen()
	if open == nil || open.ClientProof == nil {
		t.Fatalf("expected open client proof, got %#v", open)
	}
	proof := open.ClientProof
	if proof.PrincipalId == nil || proof.PrincipalId.Value != "device-1" {
		t.Fatalf("unexpected proof principal: %#v", proof.PrincipalId)
	}
	if proof.DeviceId == nil || proof.DeviceId.Value != "device-1" {
		t.Fatalf("unexpected proof device: %#v", proof.DeviceId)
	}
	if proof.Credential == nil || !bytes.Equal(proof.Credential.Value, credential) {
		t.Fatalf("unexpected proof credential: %#v", proof.Credential)
	}
	sessionKeyGrantHash, err := HashWebTTYSessionKeyGrant(open.SessionKeyGrant)
	if err != nil {
		t.Fatalf("HashWebTTYSessionKeyGrant() error = %v", err)
	}
	configHash, err := HashWebTTYConfig(open.Config)
	if err != nil {
		t.Fatalf("HashWebTTYConfig() error = %v", err)
	}
	transcript := ClientProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             string(WebTTYTransportWebSocket),
		WorkspaceID:           "workspace-1",
		ProjectID:             "project-1",
		ServerID:              "server-1",
		SessionID:             "session-1",
		ServerSigningKeyID:    serverPublic.SigningKeyID,
		ServerEncryptionKeyID: serverPublic.EncryptionKeyID,
		ServerNonce:           []byte("server-nonce"),
		AuthRequirement:       AuthRequirementClientProof,
		PayloadSuite:          PayloadCipherSuiteAES256GCM,
		KeyEnvelopeSuite:      KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
		SessionKeyGrantHash:   sessionKeyGrantHash,
		CommandConfigHash:     configHash,
		ClientPrincipalID:     "device-1",
		ClientSigningKeyID:    clientIdentity.Signing.KeyID,
		ClientCredentialHash:  HashWebTTYClientCredential(credential),
		IssuedAt:              proof.IssuedAt,
		ExpiresAt:             proof.ExpiresAt,
	}
	if !bytes.Equal(proof.TranscriptHash, transcript.Hash()) {
		t.Fatalf("unexpected proof transcript hash")
	}
	if err := VerifyWebTTYClientProofTranscript(clientIdentity.Signing.PublicKey, transcript, proof.Signature); err != nil {
		t.Fatalf("proof signature is invalid: %v", err)
	}
	transcript.CommandConfigHash = HashWebTTYClientCredential([]byte("different-command-config"))
	if err := VerifyWebTTYClientProofTranscript(clientIdentity.Signing.PublicKey, transcript, proof.Signature); err == nil {
		t.Fatalf("proof signature verified after command config hash changed")
	}
}

func TestBuildOpenMessageRequiresExpectedServerIdentityForClientProof(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	runtime := &clientRuntime{cfg: &ClientConfig{
		CmdArgs:           []string{"sh", "-lc", "echo ok"},
		EndpointIdentity:  clientIdentity,
		ClientCredential:  []byte("workspace-credential"),
		ClientPrincipalID: "device-1",
	}}
	_, err = runtime.buildOpenMessage(WebTTYTransportWebSocket, &pb.ServerHello{
		ProtocolVersion: pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1,
		SessionNonce:    []byte("server-nonce"),
		ServerIdentity:  endpointIdentityPublicToProto(serverIdentity.Public()),
		AuthRequirement: pb.AuthRequirement_AUTH_REQUIREMENT_CLIENT_PROOF,
	})
	if err == nil || !strings.Contains(err.Error(), "expected server endpoint identity") {
		t.Fatalf("expected missing expected server identity error, got %v", err)
	}
}

func TestBuildOpenMessageRequiresServerHelloIdentityForClientProof(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	runtime := &clientRuntime{cfg: &ClientConfig{
		CmdArgs:                []string{"sh", "-lc", "echo ok"},
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientCredential:       []byte("workspace-credential"),
		ClientPrincipalID:      "device-1",
	}}
	_, err = runtime.buildOpenMessage(WebTTYTransportWebSocket, &pb.ServerHello{
		ProtocolVersion: pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1,
		SessionNonce:    []byte("server-nonce"),
		AuthRequirement: pb.AuthRequirement_AUTH_REQUIREMENT_CLIENT_PROOF,
	})
	if err == nil || !strings.Contains(err.Error(), "server hello is missing server identity") {
		t.Fatalf("expected missing server hello identity error, got %v", err)
	}
}

func TestBuildOpenMessageDoesNotAttachProofWhenServerHelloDoesNotRequireIt(t *testing.T) {
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	runtime := &clientRuntime{cfg: &ClientConfig{
		CmdArgs:           []string{"sh", "-lc", "echo ok"},
		EndpointIdentity:  clientIdentity,
		ClientCredential:  []byte("workspace-credential"),
		ClientPrincipalID: "device-1",
	}}
	msg, err := runtime.buildOpenMessage(WebTTYTransportWebSocket, &pb.ServerHello{AuthRequirement: pb.AuthRequirement_AUTH_REQUIREMENT_NONE})
	if err != nil {
		t.Fatalf("buildOpenMessage(WebTTYTransportWebSocket, hello) error = %v", err)
	}
	if proof := msg.GetOpen().GetClientProof(); proof != nil {
		t.Fatalf("unexpected client proof when server hello does not require it: %#v", proof)
	}
}

func TestVerifyServerHelloAcceptsSignedExpectedIdentity(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	runtime := &clientRuntime{cfg: &ClientConfig{ExpectedServerIdentity: &serverPublic}}
	hello := signedServerHelloForTest(t, serverIdentity, WebTTYTransportWebSocket, "workspace-1", "project-1", "server-1", "session-1", AuthRequirementClientProof)
	if err := runtime.verifyServerHello(hello, WebTTYTransportWebSocket); err != nil {
		t.Fatalf("verifyServerHello() error = %v", err)
	}
}

func TestVerifyServerHelloRejectsUnexpectedServerIdentity(t *testing.T) {
	expectedIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(expected) error = %v", err)
	}
	actualIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(actual) error = %v", err)
	}
	expectedPublic := expectedIdentity.Public()
	runtime := &clientRuntime{cfg: &ClientConfig{ExpectedServerIdentity: &expectedPublic}}
	hello := signedServerHelloForTest(t, actualIdentity, WebTTYTransportWebSocket, "workspace-1", "project-1", "server-1", "session-1", AuthRequirementClientProof)
	err = runtime.verifyServerHello(hello, WebTTYTransportWebSocket)
	if err == nil || !strings.Contains(err.Error(), "does not match the expected identity") {
		t.Fatalf("expected server identity mismatch error, got %v", err)
	}
}

func TestVerifyServerHelloRejectsTamperedTranscript(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	runtime := &clientRuntime{cfg: &ClientConfig{ExpectedServerIdentity: &serverPublic}}
	hello := signedServerHelloForTest(t, serverIdentity, WebTTYTransportWebSocket, "workspace-1", "project-1", "server-1", "session-1", AuthRequirementClientProof)
	hello.ProjectId = wrapperspb.String("project-2")
	err = runtime.verifyServerHello(hello, WebTTYTransportWebSocket)
	if err == nil || !strings.Contains(err.Error(), "transcript hash does not match") {
		t.Fatalf("expected transcript hash mismatch error, got %v", err)
	}
}

func signedServerHelloForTest(t *testing.T, identity *WebTTYEndpointIdentity, transport WebTTYTransport, workspaceID, projectID, serverID, sessionID string, authRequirement AuthRequirement) *pb.ServerHello {
	t.Helper()
	public := identity.Public()
	nonce := []byte("server-nonce")
	transcript := ServerProofTranscript{
		ProtocolVersion:       ProtocolVersionWebTTY1,
		Transport:             string(transport),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		ServerID:              serverID,
		SessionID:             sessionID,
		ServerSigningKeyID:    public.SigningKeyID,
		ServerEncryptionKeyID: public.EncryptionKeyID,
		ServerNonce:           nonce,
		AuthRequirement:       authRequirement,
		PayloadSuites:         []PayloadCipherSuite{PayloadCipherSuiteAES256GCM},
		KeyEnvelopeSuites:     []KeyEnvelopeSuite{KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM},
		SignatureSuites:       []SignatureSuite{SignatureSuiteECDSAP256SHA256},
	}
	privateKey, err := ParseWebTTYSigningPrivateKey(identity.Signing.PrivateKey)
	if err != nil {
		t.Fatalf("ParseWebTTYSigningPrivateKey() error = %v", err)
	}
	transcriptHash, signature, err := SignWebTTYServerProofTranscript(rand.Reader, privateKey, transcript)
	if err != nil {
		t.Fatalf("SignWebTTYServerProofTranscript() error = %v", err)
	}
	return &pb.ServerHello{
		ProtocolVersion:   pb.ProtocolVersion_PROTOCOL_VERSION_WEBTTY_1,
		SessionNonce:      cloneBytes(nonce),
		ServerIdentity:    endpointIdentityPublicToProto(public),
		PayloadSuites:     []pb.PayloadCipherSuite{pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_AES_256_GCM},
		KeyEnvelopeSuites: []pb.KeyEnvelopeSuite{pb.KeyEnvelopeSuite_KEY_ENVELOPE_SUITE_HPKE_X25519_HKDF_SHA256_AES_256_GCM},
		SignatureSuites:   []pb.SignatureSuite{pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256},
		AuthRequirement:   pb.AuthRequirement(authRequirement),
		WorkspaceId:       wrapperspb.String(workspaceID),
		ProjectId:         wrapperspb.String(projectID),
		ServerId:          wrapperspb.String(serverID),
		SessionId:         sessionID,
		ServerProof: &pb.ServerProof{
			SignatureSuite: pb.SignatureSuite_SIGNATURE_SUITE_ECDSA_P256_SHA256,
			SigningKeyId:   cloneBytes(public.SigningKeyID),
			TranscriptHash: cloneBytes(transcriptHash),
			Signature:      cloneBytes(signature),
		},
	}
}

func TestBuildAttachMessage(t *testing.T) {
	runtime := &clientRuntime{cfg: &ClientConfig{Attach: &AttachConfig{
		SessionID:     " session-1 ",
		WorkspaceID:   " workspace-1 ",
		ProjectID:     " project-1 ",
		ServerID:      " server-1 ",
		ParticipantID: " participant-1 ",
		AttachGrant:   []byte("grant"),
		RequestedRole: AttachRoleSpectator,
		Transport:     WebTTYTransportWebSocket,
		Capabilities:  []AttachCapability{AttachCapabilityReadStream, AttachCapabilityRequestControl},
		DeviceID:      " device-1 ",
		BrowserID:     " browser-1 ",
	}}}
	msg, err := runtime.buildHandshakeMessage(WebTTYTransportWebSocket, nil)
	if err != nil {
		t.Fatalf("buildHandshakeMessage() error = %v", err)
	}
	attach := msg.GetAttach()
	if attach == nil {
		t.Fatalf("expected attach message, got %#v", msg)
	}
	if attach.SessionId != "session-1" || attach.ParticipantId != "participant-1" || !bytes.Equal(attach.AttachGrant, []byte("grant")) {
		t.Fatalf("attach identity fields not encoded: %#v", attach)
	}
	if attach.RequestedRole != pb.AttachRole_ATTACH_ROLE_SPECTATOR || attach.Transport != pb.AttachTransport_ATTACH_TRANSPORT_WEBSOCKET {
		t.Fatalf("attach role/transport not encoded: %#v", attach)
	}
	if len(attach.Capabilities) != 2 ||
		attach.Capabilities[0] != pb.AttachCapability_ATTACH_CAPABILITY_READ_STREAM ||
		attach.Capabilities[1] != pb.AttachCapability_ATTACH_CAPABILITY_REQUEST_CONTROL {
		t.Fatalf("attach capabilities not encoded: %#v", attach.Capabilities)
	}
	if attach.GetDeviceId().GetValue() != "device-1" || attach.GetBrowserId().GetValue() != "browser-1" {
		t.Fatalf("attach device/browser fields not trimmed: %#v", attach)
	}
}

func TestBuildAttachMessageIncludesClientProof(t *testing.T) {
	identity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	credential := []byte("workspace-credential")
	runtime := &clientRuntime{cfg: &ClientConfig{
		EndpointIdentity:  identity,
		ClientCredential:  credential,
		ClientPrincipalID: "device-1",
		ClientDeviceID:    "device-1",
		Attach: &AttachConfig{
			SessionID:     "session-1",
			WorkspaceID:   "workspace-1",
			ProjectID:     "project-1",
			ServerID:      "server-1",
			ParticipantID: "participant-1",
			AttachGrant:   []byte("grant"),
			RequestedRole: AttachRoleController,
			Transport:     WebTTYTransportWebSocket,
		},
	}}
	msg, err := runtime.buildAttachMessage(WebTTYTransportWebSocket)
	if err != nil {
		t.Fatalf("buildAttachMessage() error = %v", err)
	}
	attach := msg.GetAttach()
	if attach == nil || attach.ClientProof == nil {
		t.Fatalf("attach client proof is missing: %#v", attach)
	}
	proof := attach.ClientProof
	if proof.PrincipalId == nil || proof.PrincipalId.Value != "device-1" {
		t.Fatalf("unexpected proof principal: %#v", proof.PrincipalId)
	}
	if proof.DeviceId == nil || proof.DeviceId.Value != "device-1" {
		t.Fatalf("unexpected proof device: %#v", proof.DeviceId)
	}
	if proof.Credential == nil || !bytes.Equal(proof.Credential.Value, credential) {
		t.Fatalf("unexpected proof credential: %#v", proof.Credential)
	}
	transcript := ClientProofTranscript{
		ProtocolVersion:      ProtocolVersionWebTTY1,
		Transport:            string(WebTTYTransportWebSocket),
		WorkspaceID:          "workspace-1",
		ProjectID:            "project-1",
		ServerID:             "server-1",
		SessionID:            "session-1",
		AuthRequirement:      AuthRequirementClientProof,
		PayloadSuite:         PayloadCipherSuiteAES256GCM,
		KeyEnvelopeSuite:     KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM,
		AttachGrantHash:      HashWebTTYAttachGrant([]byte("grant")),
		RequestedRole:        string(AttachRoleController),
		ClientPrincipalID:    "device-1",
		ClientSigningKeyID:   identity.Signing.KeyID,
		ClientCredentialHash: HashWebTTYClientCredential(credential),
		IssuedAt:             proof.IssuedAt,
		ExpiresAt:            proof.ExpiresAt,
	}
	if !bytes.Equal(proof.TranscriptHash, transcript.Hash()) {
		t.Fatalf("unexpected proof transcript hash")
	}
	if err := VerifyWebTTYClientProofTranscript(identity.Signing.PublicKey, transcript, proof.Signature); err != nil {
		t.Fatalf("proof signature is invalid: %v", err)
	}
}

func TestBuildAttachMessageRejectsCredentialWithoutIdentity(t *testing.T) {
	runtime := &clientRuntime{cfg: &ClientConfig{
		ClientCredential: []byte("workspace-credential"),
		Attach: &AttachConfig{
			SessionID:     "session-1",
			ParticipantID: "participant-1",
			AttachGrant:   []byte("grant"),
		},
	}}
	if _, err := runtime.buildAttachMessage(WebTTYTransportWebSocket); err == nil || !strings.Contains(err.Error(), "client endpoint identity") {
		t.Fatalf("expected missing identity error, got %v", err)
	}
}

func TestBuildAttachMessageDefaultsAndValidation(t *testing.T) {
	valid := &AttachConfig{SessionID: "session", ParticipantID: "participant", AttachGrant: []byte("grant")}
	runtime := &clientRuntime{cfg: &ClientConfig{Attach: valid}}
	msg, err := runtime.buildHandshakeMessage(WebTTYTransportPlain, nil)
	if err != nil {
		t.Fatalf("default attach build error = %v", err)
	}
	attach := msg.GetAttach()
	if attach.RequestedRole != pb.AttachRole_ATTACH_ROLE_SPECTATOR ||
		attach.Transport != pb.AttachTransport_ATTACH_TRANSPORT_PLAIN ||
		len(attach.Capabilities) != 1 ||
		attach.Capabilities[0] != pb.AttachCapability_ATTACH_CAPABILITY_READ_STREAM {
		t.Fatalf("attach defaults not applied: %#v", attach)
	}
	cases := []struct {
		name string
		cfg  *AttachConfig
	}{
		{name: "missing session", cfg: &AttachConfig{ParticipantID: "participant", AttachGrant: []byte("grant")}},
		{name: "missing participant", cfg: &AttachConfig{SessionID: "session", AttachGrant: []byte("grant")}},
		{name: "missing grant", cfg: &AttachConfig{SessionID: "session", ParticipantID: "participant"}},
		{name: "bad role", cfg: &AttachConfig{SessionID: "session", ParticipantID: "participant", AttachGrant: []byte("grant"), RequestedRole: AttachRole("owner")}},
		{name: "bad transport", cfg: &AttachConfig{SessionID: "session", ParticipantID: "participant", AttachGrant: []byte("grant"), Transport: WebTTYTransport("sctp")}},
		{name: "bad capability", cfg: &AttachConfig{SessionID: "session", ParticipantID: "participant", AttachGrant: []byte("grant"), Capabilities: []AttachCapability{"write_all"}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &clientRuntime{cfg: &ClientConfig{Attach: tt.cfg}}
			if _, err := runtime.buildHandshakeMessage(WebTTYTransportWebSocket, nil); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestBuildOpenMessagePreservesExplicitTERM(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	runtime := &clientRuntime{cfg: &ClientConfig{AllocateTTY: true, EnvVars: []string{"TERM=screen"}}}
	msg, err := runtime.buildOpenMessage(WebTTYTransportWebSocket, nil)
	if err != nil {
		t.Fatalf("buildOpenMessage(WebTTYTransportWebSocket, nil) error = %v", err)
	}
	env := msg.GetOpen().Config.EnvVars
	if len(env) != 1 || env[0].Key != "TERM" || env[0].Value != "screen" {
		t.Fatalf("explicit TERM should be preserved without duplicate: %#v", env)
	}
}

func TestWaitForOpen(t *testing.T) {
	deadline := 10 * time.Millisecond
	runtime := &clientRuntime{cfg: &ClientConfig{OpenDeadline: &deadline}}
	events := make(chan clientEvent, 1)
	events <- clientEvent{msg: &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}}}
	if err := runtime.waitForOpen(t.Context(), events); err != nil {
		t.Fatalf("waitForOpen(ack) error = %v", err)
	}
	events = make(chan clientEvent, 1)
	events <- clientEvent{err: os.ErrClosed}
	if err := runtime.waitForOpen(t.Context(), events); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("waitForOpen(event error) = %v", err)
	}
	events = make(chan clientEvent)
	if err := runtime.waitForOpen(t.Context(), events); !errors.Is(err, errClientOperationTimeout) {
		t.Fatalf("waitForOpen(timeout) = %v", err)
	}
}

func TestHandleDataValidation(t *testing.T) {
	var stderr bytes.Buffer
	runtime := &clientRuntime{cfg: &ClientConfig{Stdout: &bytes.Buffer{}, Stderr: &stderr}}
	if err := runtime.handleData(context.TODO(), &pb.Data{Type: pb.Data_TYPE_STDERR, Payload: &pb.Data_Data{Data: []byte("err")}}); err != nil {
		t.Fatalf("handleData(stderr) error = %v", err)
	}
	if stderr.String() != "err" {
		t.Fatalf("stderr payload = %q", stderr.String())
	}
	if err := runtime.handleData(context.TODO(), nil); err == nil {
		t.Fatalf("expected nil data error")
	}
	if err := runtime.handleData(context.TODO(), &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: []byte("in")}}); err == nil {
		t.Fatalf("expected unexpected stream error")
	}
	if err := runtime.handleData(context.TODO(), &pb.Data{Type: pb.Data_TYPE_STDOUT}); err == nil {
		t.Fatalf("expected unexpected payload error")
	}
}

func TestWriteAllHandlesPartialWriters(t *testing.T) {
	writer := &partialWriter{limit: 2}
	if err := writeAll(writer, []byte("hello")); err != nil {
		t.Fatalf("writeAll(partial) error = %v", err)
	}
	if got := writer.String(); got != "hello" {
		t.Fatalf("partial writer got %q", got)
	}
	if err := writeAll(zeroWriter{}, []byte("x")); err == nil {
		t.Fatalf("expected zero writer error")
	}
	if err := writeAll(errorWriter{}, []byte("x")); err == nil {
		t.Fatalf("expected writer error")
	}
}

type partialWriter struct {
	bytes.Buffer
	limit int
}

func (w *partialWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		p = p[:w.limit]
	}
	return w.Buffer.Write(p)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

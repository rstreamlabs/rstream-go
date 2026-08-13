// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func testShellCommand(posixScript, windowsScript string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-NoProfile", "-Command", windowsScript}
	}
	return []string{"/bin/sh", "-c", posixScript}
}

func testPathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func testInterruptHelperCommand() []string {
	return []string{os.Args[0], "-test.run=^TestWebTTYInterruptHelperProcess$"}
}

func TestWebTTYInterruptHelperProcess(t *testing.T) {
	if os.Getenv("RSTREAM_WEBTTY_TEST_INTERRUPT_HELPER") != "1" {
		return
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	defer signal.Stop(signalCh)
	if _, err := os.Stdout.Write([]byte("ready\n")); err != nil {
		os.Exit(8)
	}
	select {
	case <-signalCh:
		os.Exit(7)
	case <-time.After(10 * time.Second):
		os.Exit(9)
	}
}

func TestResolveServerConfigDefaultsAndHandlerDrain(t *testing.T) {
	cfg := resolveServerConfig(nil)
	if cfg.MaxMessageSize == nil || *cfg.MaxMessageSize != defaultMaxMessageSize {
		t.Fatalf("unexpected max message size")
	}
	if cfg.EnvVars == nil || len(*cfg.EnvVars) != 0 {
		t.Fatalf("expected empty env var map")
	}
	if cfg.SessionOpenDeadline == nil || *cfg.SessionOpenDeadline != defaultSessionOpenDeadline {
		t.Fatalf("unexpected open deadline")
	}
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{}))
	sessionObj := &session{}
	if !handler.registerSession(sessionObj) {
		t.Fatalf("expected registration before drain")
	}
	if got := handler.snapshotSessions(); len(got) != 1 || got[0] != sessionObj {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
	handler.unregisterSession(sessionObj)
	if got := handler.snapshotSessions(); len(got) != 0 {
		t.Fatalf("expected empty snapshot after unregister")
	}
	handler.BeginDrain()
	if handler.registerSession(&session{}) {
		t.Fatalf("registration should fail while draining")
	}
	if err := handler.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown without sessions: %v", err)
	}
}

func TestWebTTYHandlerConfigUsesImmutableSnapshots(t *testing.T) {
	maxMessageSize := int64(4096)
	username := "alice"
	env := map[string]string{"MODE": "test"}
	source := &ServerConfig{
		MaxMessageSize: &maxMessageSize,
		EnvVars:        &env,
		AllowedOrigins: []string{"https://terminal.example"},
		PayloadCrypto: &PayloadCrypto{
			Capabilities: []OpenCapability{OpenCapabilityEncryptedPayload},
			SessionKeyGrant: &SessionKeyGrant{
				PayloadKeyID: []byte("payload-key"),
				KeyEnvelopes: []KeyEnvelope{{RecipientKeyID: []byte("recipient")}},
			},
		},
		EndpointIdentity: &WebTTYEndpointIdentity{
			Encryption: E2EIdentity{KeyID: []byte("encryption-key")},
			Signing:    WebTTYSigningIdentity{KeyID: []byte("signing-key")},
		},
		AuthorizedClientSigningKeys: map[string][]byte{"client": []byte("credential")},
		DefaultUsername:             &username,
	}
	handler := NewWebTTYHandler(source)
	maxMessageSize = 1
	env["MODE"] = "mutated"
	source.AllowedOrigins[0] = "https://mutated.example"
	source.PayloadCrypto.Capabilities[0] = OpenCapabilitySessionKeyGrant
	source.PayloadCrypto.SessionKeyGrant.PayloadKeyID[0] = 'X'
	source.PayloadCrypto.SessionKeyGrant.KeyEnvelopes[0].RecipientKeyID[0] = 'X'
	source.EndpointIdentity.Encryption.KeyID[0] = 'X'
	source.EndpointIdentity.Signing.KeyID[0] = 'X'
	source.AuthorizedClientSigningKeys["client"][0] = 'X'
	username = "mallory"
	first := handler.Config()
	if *first.MaxMessageSize != 4096 || (*first.EnvVars)["MODE"] != "test" || first.AllowedOrigins[0] != "https://terminal.example" {
		t.Fatalf("handler config changed through source mutation: %#v", first)
	}
	if first.PayloadCrypto.Capabilities[0] != OpenCapabilityEncryptedPayload || string(first.PayloadCrypto.SessionKeyGrant.PayloadKeyID) != "payload-key" || string(first.PayloadCrypto.SessionKeyGrant.KeyEnvelopes[0].RecipientKeyID) != "recipient" {
		t.Fatalf("handler payload crypto changed through source mutation: %#v", first.PayloadCrypto)
	}
	if string(first.EndpointIdentity.Encryption.KeyID) != "encryption-key" || string(first.EndpointIdentity.Signing.KeyID) != "signing-key" || string(first.AuthorizedClientSigningKeys["client"]) != "credential" || *first.DefaultUsername != "alice" {
		t.Fatalf("handler identity or policy changed through source mutation: %#v", first)
	}
	*first.MaxMessageSize = 2
	(*first.EnvVars)["MODE"] = "returned"
	first.AllowedOrigins[0] = "https://returned.example"
	first.PayloadCrypto.SessionKeyGrant.PayloadKeyID[0] = 'Y'
	first.EndpointIdentity.Encryption.KeyID[0] = 'Y'
	first.AuthorizedClientSigningKeys["client"][0] = 'Y'
	*first.DefaultUsername = "eve"
	second := handler.Config()
	if *second.MaxMessageSize != 4096 || (*second.EnvVars)["MODE"] != "test" || second.AllowedOrigins[0] != "https://terminal.example" || string(second.PayloadCrypto.SessionKeyGrant.PayloadKeyID) != "payload-key" || string(second.EndpointIdentity.Encryption.KeyID) != "encryption-key" || string(second.AuthorizedClientSigningKeys["client"]) != "credential" || *second.DefaultUsername != "alice" {
		t.Fatalf("handler config changed through returned snapshot mutation: %#v", second)
	}
}

func TestHandlerServeHTTPRejectsWhileDraining(t *testing.T) {
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{}))
	handler.BeginDrain()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/webtty", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("ServeHTTP() status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestWebTTYHandlerExecutesProcessAndStreamsOutput(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           testWebTTYURL(server.URL),
		CmdArgs:       testShellCommand("printf stdout; printf stderr >&2; exit 6", "[Console]::Out.Write('stdout'); [Console]::Error.Write('stderr'); exit 6"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 6 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "stdout" || stderr != "stderr" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerDrainsNonTTYOutputBeforeWait(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           testWebTTYURL(server.URL),
		CmdArgs:       testShellCommand("i=0; while [ $i -lt 8192 ]; do printf 0123456789abcdef; i=$((i+1)); done", "[Console]::Out.Write(('0123456789abcdef' * 8192))"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stderr != "" {
		t.Fatalf("stderr=%q, want empty", stderr)
	}
	want := strings.Repeat("0123456789abcdef", 8192)
	if stdout != want {
		t.Fatalf("stdout length=%d, want %d", len(stdout), len(want))
	}
}

func TestWebTTYHandlerShutdownDeliversProtocolClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal trap test")
	}
	zero := time.Duration(0)
	closeDeadline := 2 * time.Second
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:    &zero,
		SessionCloseDeadline: &closeDeadline,
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           testWebTTYURL(server.URL),
		CmdArgs:       testInterruptHelperCommand(),
		EnvVars:       []string{"RSTREAM_WEBTTY_TEST_INTERRUPT_HELPER=1"},
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	waitForClientStdout(t, session, "ready\n")
	shutdownCtx, cancel := context.WithTimeout(t.Context(), closeDeadline)
	defer cancel()
	if err := handler.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	_, _, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("shutdown exit code = %d, want trapped interrupt exit code 7", exitCode)
	}
}

func TestWebTTYHandlerShutdownDeliversProtocolClosePlain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal trap test")
	}
	zero := time.Duration(0)
	closeDeadline := 2 * time.Second
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:    &zero,
		SessionCloseDeadline: &closeDeadline,
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handler.ServeConn(conn)
		}
	}()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           "tcp://" + listener.Addr().String(),
		CmdArgs:       testInterruptHelperCommand(),
		EnvVars:       []string{"RSTREAM_WEBTTY_TEST_INTERRUPT_HELPER=1"},
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	waitForClientStdout(t, session, "ready\n")
	shutdownCtx, cancel := context.WithTimeout(t.Context(), closeDeadline)
	defer cancel()
	if err := handler.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	_, _, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("plain shutdown exit code = %d, want trapped interrupt exit code 7", exitCode)
	}
	listener.Close()
	<-done
}

func TestWebTTYHandlerPassesStdinWorkdirAndEnvironment(t *testing.T) {
	zero := time.Duration(0)
	workdir := t.TempDir()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           testWebTTYURL(server.URL),
		EnvVars:       []string{"CUSTOM=value"},
		Workdir:       &workdir,
		CmdArgs:       testShellCommand("read line; printf \"%s|%s|%s\" \"$line\" \"$CUSTOM\" \"$(pwd)\"", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line + '|' + $env:CUSTOM + '|' + (Get-Location).ProviderPath)"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("typed\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, _, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	wantWorkdirOutput := "typed|value|" + resolvedWorkdir
	if !testPathEqual(strings.TrimPrefix(stdout, "typed|value|"), resolvedWorkdir) || !strings.HasPrefix(stdout, "typed|value|") {
		t.Fatalf("stdout=%q, want %s", stdout, wantWorkdirOutput)
	}
}

func TestWebTTYHandlerLoginProvidesResolvedAdministrativeEnvironment(t *testing.T) {
	zero := time.Duration(0)
	mode := WebTTYExecutionModeLogin
	userInfo, err := GetUserInfo(nil)
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}
	username := userInfo.Name
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval: &zero,
		ExecutionMode:     &mode,
		DefaultUsername:   &username,
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL: testWebTTYURL(server.URL),
		EnvVars: []string{
			"USER=client-user",
			"LOGNAME=client-user",
			"HOME=client-home",
			"SHELL=client-shell",
			"USERNAME=client-user",
			"USERPROFILE=client-home",
			"COMSPEC=client-shell",
		},
		CmdArgs: testShellCommand(
			`printf "%s|%s|%s|%s" "$USER" "$LOGNAME" "$HOME" "$SHELL"`,
			`[Console]::Write($env:USERNAME + '|' + $env:USERPROFILE + '|' + $env:HOME + '|' + $env:COMSPEC)`,
		),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v, stderr=%q", exitCode, err, stderr)
	}
	parts := strings.Split(strings.TrimSpace(stdout), "|")
	if len(parts) != 4 {
		t.Fatalf("administrative environment output = %q", stdout)
	}
	if parts[0] != userInfo.Name {
		t.Fatalf("session username = %q, want %q", parts[0], userInfo.Name)
	}
	if runtime.GOOS != "windows" && parts[1] != userInfo.Name {
		t.Fatalf("session LOGNAME = %q, want %q", parts[1], userInfo.Name)
	}
	if !testPathEqual(parts[2], userInfo.Home) {
		t.Fatalf("session home = %q, want %q", parts[2], userInfo.Home)
	}
	if !testPathEqual(parts[3], userInfo.Shell) {
		t.Fatalf("session shell = %q, want %q", parts[3], userInfo.Shell)
	}
}

func TestWebTTYHandlerPayloadCryptoRoundTrip(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval: &zero,
		PayloadCrypto: &PayloadCrypto{
			DecryptStdin: func(_ context.Context, payload *EncryptedPayload) ([]byte, error) {
				text := string(payload.Ciphertext)
				if !strings.HasPrefix(text, "client:") {
					t.Fatalf("encrypted stdin = %q, want client prefix", text)
				}
				return []byte(strings.TrimPrefix(text, "client:")), nil
			},
			EncryptStdout: func(_ context.Context, payload []byte) (*EncryptedPayload, error) {
				return &EncryptedPayload{
					Ciphertext:      append([]byte("server:"), payload...),
					PlaintextLength: uint32(len(payload)),
				}, nil
			},
		},
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           testWebTTYURL(server.URL),
		CmdArgs:       testShellCommand("read line; printf \"%s\" \"$line\"", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line)"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
		PayloadCrypto: &PayloadCrypto{
			EncryptStdin: func(_ context.Context, payload []byte) (*EncryptedPayload, error) {
				return &EncryptedPayload{
					Ciphertext:      append([]byte("client:"), payload...),
					PlaintextLength: uint32(len(payload)),
				}, nil
			},
			DecryptStdout: func(_ context.Context, payload *EncryptedPayload) ([]byte, error) {
				text := string(payload.Ciphertext)
				if !strings.HasPrefix(text, "server:") {
					t.Fatalf("encrypted stdout = %q, want server prefix", text)
				}
				return []byte(strings.TrimPrefix(text, "server:")), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("typed\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "typed" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerE2ERoundTripWebSocket(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/websocket")
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:     &zero,
		PayloadCryptoResolver: NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		EndpointIdentity:      serverIdentity,
		RequireClientProof:    &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("read line; printf \"%s\" \"$line\"", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line)"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("typed\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "typed" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerMutualAuthAcceptsAuthorizedClient(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("test/mutual-auth"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.Encryption.KeyID,
			PublicKey: serverIdentity.Encryption.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
		ServerID: "shell",
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("printf ok", "[Console]::Out.Write('ok')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientPrincipalID:      "user-1",
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "ok" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerWorkspaceManagedVerifierAcceptsTrustedClient(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/workspace-managed-verifier")
	serverPublic := serverIdentity.Public()
	credential := []byte("workspace-managed-client-credential")
	verifierCalled := false
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		WorkspaceID:            "workspace-1",
		ProjectID:              "project-1",
		ServerID:               "server-1",
		ClientProofVerifier: func(_ context.Context, verification ClientProofVerification) ([]byte, error) {
			verifierCalled = true
			if !bytes.Equal(verification.Credential, credential) {
				return nil, errors.New("unexpected workspace credential")
			}
			if verification.Transcript.WorkspaceID != "workspace-1" ||
				verification.Transcript.ProjectID != "project-1" ||
				verification.Transcript.ServerID != "server-1" ||
				verification.Transcript.ClientPrincipalID != "device-1" {
				return nil, errors.New("unexpected workspace-managed client proof scope")
			}
			if !bytes.Equal(verification.Transcript.ClientSigningKeyID, clientIdentity.Signing.KeyID) {
				return nil, errors.New("unexpected workspace-managed client signing key")
			}
			return verification.Proof.SigningPublicKey, nil
		},
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("printf ok", "[Console]::Out.Write('ok')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientCredential:       credential,
		ClientPrincipalID:      "device-1",
		ClientDeviceID:         "device-1",
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "ok" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	if !verifierCalled {
		t.Fatal("workspace-managed client proof verifier was not called")
	}
}

func TestWebTTYHandlerOpenTimeoutCancelsBlockedClientProofVerifier(t *testing.T) {
	zero := time.Duration(0)
	required := true
	openDeadline := 30 * time.Millisecond
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/workspace-managed-verifier-timeout")
	serverPublic := serverIdentity.Public()
	verifierStarted := make(chan struct{})
	releaseVerifier := make(chan struct{})
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		SessionOpenDeadline:    &openDeadline,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		WorkspaceID:            "workspace-1",
		ProjectID:              "project-1",
		ServerID:               "server-1",
		ClientProofVerifier: func(ctx context.Context, _ ClientProofVerification) ([]byte, error) {
			close(verifierStarted)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseVerifier:
				return nil, errors.New("verifier released by test")
			}
		},
	}))
	server := httptest.NewServer(handler)
	result := make(chan error, 1)
	go func() {
		session, err := OpenClientSession(context.Background(), &SessionConfig{
			URL:                    testWebTTYURL(server.URL),
			CmdArgs:                testShellCommand("printf should-not-run", "[Console]::Out.Write('should-not-run')"),
			OpenDeadline:           durationPtr(2 * time.Second),
			CloseDeadline:          durationPtr(time.Second),
			PayloadCrypto:          clientCrypto,
			EndpointIdentity:       clientIdentity,
			ExpectedServerIdentity: &serverPublic,
			ClientCredential:       []byte("workspace-managed-client-credential"),
			ClientPrincipalID:      "device-1",
			ClientDeviceID:         "device-1",
		})
		if session != nil {
			_ = session.Close()
		}
		result <- err
	}()
	select {
	case <-verifierStarted:
	case <-time.After(time.Second):
		close(releaseVerifier)
		server.Close()
		t.Fatal("client proof verifier was not called")
	}
	completedByTimeout := false
	var openErr error
	select {
	case openErr = <-result:
		completedByTimeout = true
	case <-time.After(500 * time.Millisecond):
		close(releaseVerifier)
		openErr = <-result
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := handler.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	cancel()
	server.Close()
	if !completedByTimeout {
		t.Fatal("session open timeout did not cancel the blocked client proof verifier")
	}
	if openErr == nil {
		t.Fatal("expected session opening to fail after the server timeout")
	}
}

func TestWebTTYHandlerWorkspaceManagedVerifierRejectsUntrustedClient(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/workspace-managed-verifier-reject")
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		WorkspaceID:            "workspace-1",
		ProjectID:              "project-1",
		ServerID:               "server-1",
		ClientProofVerifier: func(context.Context, ClientProofVerification) ([]byte, error) {
			return nil, errors.New("workspace-managed WebTTY client device is not trusted for this server")
		},
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("printf should-not-run", "[Console]::Out.Write('should-not-run')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientCredential:       []byte("workspace-managed-client-credential"),
		ClientPrincipalID:      "device-1",
		ClientDeviceID:         "device-1",
	})
	if err == nil {
		_ = session.Close()
		t.Fatalf("expected untrusted workspace-managed client to be rejected")
	}
	if !strings.Contains(err.Error(), "workspace-managed WebTTY client device is not trusted") {
		t.Fatalf("unexpected workspace-managed auth error: %v", err)
	}
}

func TestWebTTYHandlerWorkspaceManagedVerifierOverridesStaticAuthorizedClient(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/workspace-managed-verifier-authority")
	serverPublic := serverIdentity.Public()
	verifierCalled := false
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		WorkspaceID:            "workspace-1",
		ProjectID:              "project-1",
		ServerID:               "server-1",
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
		ClientProofVerifier: func(context.Context, ClientProofVerification) ([]byte, error) {
			verifierCalled = true
			return nil, errors.New("workspace-managed WebTTY client device is not trusted for this server")
		},
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("printf should-not-run", "[Console]::Out.Write('should-not-run')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientCredential:       []byte("workspace-managed-client-credential"),
		ClientPrincipalID:      "device-1",
		ClientDeviceID:         "device-1",
	})
	if err == nil {
		_ = session.Close()
		t.Fatalf("expected workspace-managed verifier to reject static authorized client fallback")
	}
	if !verifierCalled {
		t.Fatal("workspace-managed verifier was not called")
	}
	if !strings.Contains(err.Error(), "workspace-managed WebTTY client device is not trusted") {
		t.Fatalf("unexpected workspace-managed auth error: %v", err)
	}
}

func TestWebTTYHandlerMutualAuthRejectsUnauthorizedClient(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("test/mutual-auth-reject"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.Encryption.KeyID,
			PublicKey: serverIdentity.Encryption.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		ServerID:               "shell",
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("printf should-not-run", "[Console]::Out.Write('should-not-run')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientPrincipalID:      "user-1",
	})
	if err == nil {
		_ = session.Close()
		t.Fatalf("expected unauthorized client to be rejected")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWebTTYHandlerRequireSessionKeyGrantRejectsPlainOpen(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	serverPublic := serverIdentity.Public()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		CmdArgs:                testShellCommand("printf no", "[Console]::Out.Write('no')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
	})
	if err == nil {
		_ = session.Close()
		t.Fatalf("expected OpenClientSession() to reject missing E2E session key grant")
	}
	if !strings.Contains(err.Error(), "session key grant") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWebTTYHandlerE2ERoundTripPlain(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/plain")
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:     &zero,
		PayloadCryptoResolver: NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		EndpointIdentity:      serverIdentity,
		RequireClientProof:    &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	defer handler.Shutdown(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handler.ServeConn(conn)
		}
	}()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    "tcp://" + listener.Addr().String(),
		CmdArgs:                testShellCommand("read line; printf \"%s\" \"$line\"", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line)"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("typed\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "typed" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	listener.Close()
	<-done
}

func TestWebTTYHandlerMutualAuthRejectsUnauthorizedClientPlain(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/plain-mutual-auth-reject")
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:     &zero,
		PayloadCryptoResolver: NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		EndpointIdentity:      serverIdentity,
		RequireClientProof:    &required,
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handler.ServeConn(conn)
		}
	}()
	defer func() {
		listener.Close()
		<-done
		handler.Shutdown(t.Context())
	}()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    "tcp://" + listener.Addr().String(),
		CmdArgs:                testShellCommand("printf should-not-run", "[Console]::Out.Write('should-not-run')"),
		OpenDeadline:           durationPtr(time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
	})
	if err == nil {
		_ = session.Close()
		t.Fatalf("expected unauthorized plain client to be rejected")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unexpected plain auth error: %v", err)
	}
}

func TestWebTTYHandlerServesPlainTransport(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	defer handler.Shutdown(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handler.ServeConn(conn)
		}
	}()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           "tcp://" + listener.Addr().String(),
		CmdArgs:       testShellCommand("printf plain", "[Console]::Out.Write('plain')"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "plain" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	listener.Close()
	<-done
}

func TestWebTTYHandlerServesWebTransport(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer packetConn.Close()
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       testWebTransportTLSConfig(t),
			EnableDatagrams: true,
		},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtty", func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		go handler.ServeWebTransportSession(session.Context(), session)
	})
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(packetConn) }()
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:           "https://" + packetConn.LocalAddr().String() + "/webtty",
		Transport:     WebTTYTransportWebTransport,
		TLSConfig:     &tls.Config{InsecureSkipVerify: true},
		CmdArgs:       testShellCommand("printf webtransport", "[Console]::Out.Write('webtransport')"),
		OpenDeadline:  durationPtr(3 * time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "webtransport" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	select {
	case err := <-errCh:
		if err != nil && !strings.Contains(err.Error(), "server closed") {
			t.Fatalf("WebTransport server error = %v", err)
		}
	default:
	}
}

func TestWebTTYHandlerE2ERoundTripWebTransport(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, clientIdentity, clientCrypto := newTestE2ECryptoPair(t, "test/webtransport")
	serverPublic := serverIdentity.Public()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:     &zero,
		PayloadCryptoResolver: NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		EndpointIdentity:      serverIdentity,
		RequireClientProof:    &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
	}))
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer packetConn.Close()
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       testWebTransportTLSConfig(t),
			EnableDatagrams: true,
		},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtty", func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		go handler.ServeWebTransportSession(session.Context(), session)
	})
	go func() { _ = server.Serve(packetConn) }()
	defer server.Close()
	defer handler.Shutdown(t.Context())
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    "https://" + packetConn.LocalAddr().String() + "/webtty",
		Transport:              WebTTYTransportWebTransport,
		TLSConfig:              &tls.Config{InsecureSkipVerify: true},
		CmdArgs:                testShellCommand("read line; printf \"%s\" \"$line\"", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line)"),
		OpenDeadline:           durationPtr(3 * time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("typed\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "typed" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerMutualAuthWebTransport(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("test/webtransport-mutual-auth"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.Encryption.KeyID,
			PublicKey: serverIdentity.Encryption.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		AuthorizedClientSigningKeys: map[string][]byte{
			string(clientIdentity.Signing.KeyID): clientIdentity.Signing.PublicKey,
		},
		ServerID: "shell",
	}))
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer packetConn.Close()
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       testWebTransportTLSConfig(t),
			EnableDatagrams: true,
		},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtty", func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		go handler.ServeWebTransportSession(session.Context(), session)
	})
	go func() { _ = server.Serve(packetConn) }()
	defer server.Close()
	defer handler.Shutdown(t.Context())
	serverPublic := serverIdentity.Public()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    "https://" + packetConn.LocalAddr().String() + "/webtty",
		Transport:              WebTTYTransportWebTransport,
		TLSConfig:              &tls.Config{InsecureSkipVerify: true},
		CmdArgs:                testShellCommand("printf ok", "[Console]::Out.Write('ok')"),
		OpenDeadline:           durationPtr(3 * time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientPrincipalID:      "user-1",
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "ok" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerMutualAuthRejectsUnauthorizedClientWebTransport(t *testing.T) {
	zero := time.Duration(0)
	required := true
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte("test/webtransport-mutual-auth-reject"),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.Encryption.KeyID,
			PublicKey: serverIdentity.Encryption.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{
		HeartbeatInterval:      &zero,
		PayloadCryptoResolver:  NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &required,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &required,
		ServerID:               "shell",
	}))
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer packetConn.Close()
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       testWebTransportTLSConfig(t),
			EnableDatagrams: true,
		},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtty", func(w http.ResponseWriter, r *http.Request) {
		session, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		go handler.ServeWebTransportSession(session.Context(), session)
	})
	go func() { _ = server.Serve(packetConn) }()
	defer server.Close()
	defer handler.Shutdown(t.Context())
	serverPublic := serverIdentity.Public()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    "https://" + packetConn.LocalAddr().String() + "/webtty",
		Transport:              WebTTYTransportWebTransport,
		TLSConfig:              &tls.Config{InsecureSkipVerify: true},
		CmdArgs:                testShellCommand("printf should-not-run", "[Console]::Out.Write('should-not-run')"),
		OpenDeadline:           durationPtr(3 * time.Second),
		CloseDeadline:          durationPtr(time.Second),
		PayloadCrypto:          clientCrypto,
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientPrincipalID:      "user-1",
	})
	if err == nil {
		_ = session.Close()
		t.Fatalf("expected unauthorized WebTransport client to be rejected")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unexpected WebTransport auth error: %v", err)
	}
}

func TestRunClientInteractivePipeSession(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err := RunClient(t.Context(), &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Interactive:   true,
		Stdin:         strings.NewReader("typed\n"),
		Stdout:        &stdout,
		Stderr:        &stderr,
		CmdArgs:       testShellCommand("read line; printf \"%s\" \"$line\"; printf err >&2; exit 5", "$line = [Console]::In.ReadLine(); [Console]::Out.Write($line); [Console]::Error.Write('err'); exit 5"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 5 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	if stdout.String() != "typed" || stderr.String() != "err" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunClientNonInteractivePipeSession(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer stdin.Close()
	if _, err := input.WriteString("piped\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := input.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	var stdout strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	exitCode, err := RunClient(ctx, &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Interactive:   false,
		Stdin:         stdin,
		Stdout:        &stdout,
		CmdArgs:       testShellCommand("cat", "$input | ForEach-Object { [Console]::Out.WriteLine($_) }"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	if stdout.String() != "piped\n" {
		t.Fatalf("stdout = %q, want piped input", stdout.String())
	}
}

func TestRunClientNonInteractiveReaderSession(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	var stdout strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	exitCode, err := RunClient(ctx, &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Stdin:         strings.NewReader("reader\n"),
		Stdout:        &stdout,
		CmdArgs:       testShellCommand("cat", "$input | ForEach-Object { [Console]::Out.WriteLine($_) }"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	if stdout.String() != "reader\n" {
		t.Fatalf("stdout = %q, want reader input", stdout.String())
	}
}

func TestRunClientNonInteractiveEmptyPipeSendsEOF(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer stdin.Close()
	if err := input.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	exitCode, err := RunClient(ctx, &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Stdin:         stdin,
		CmdArgs:       testShellCommand("cat", "$input | Out-Null"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
}

func TestRunClientStdinEOFDoesNotEndRunningCommand(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer stdin.Close()
	if err := input.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	var stdout strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	exitCode, err := RunClient(ctx, &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Stdin:         stdin,
		Stdout:        &stdout,
		CmdArgs:       testShellCommand("sleep 0.2; printf survived", "Start-Sleep -Milliseconds 200; [Console]::Out.Write('survived')"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	if stdout.String() != "survived" {
		t.Fatalf("stdout = %q, want survived", stdout.String())
	}
}

func TestRunClientNonInteractiveLargePipeIsNotTruncated(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer stdin.Close()
	const inputSize = 2 << 20
	writeResult := make(chan error, 1)
	go func() {
		_, err := input.Write(bytes.Repeat([]byte("x"), inputSize))
		if closeErr := input.Close(); err == nil {
			err = closeErr
		}
		writeResult <- err
	}()
	var stdout strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	exitCode, err := RunClient(ctx, &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Stdin:         stdin,
		Stdout:        &stdout,
		CmdArgs:       testShellCommand("wc -c", "$data = [Console]::OpenStandardInput(); $buffer = New-Object byte[] 32768; $total = 0; while (($count = $data.Read($buffer, 0, $buffer.Length)) -gt 0) { $total += $count }; [Console]::Out.Write($total)"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("stdin write error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != strconv.Itoa(inputSize) {
		t.Fatalf("received byte count = %q, want %d", got, inputSize)
	}
}

func TestRunClientRemoteExitDoesNotWaitForOpenStdin(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer stdin.Close()
	defer input.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	exitCode, err := RunClient(ctx, &ClientConfig{
		URL:           testWebTTYURL(server.URL),
		Stdin:         stdin,
		CmdArgs:       testShellCommand("exit 0", "exit 0"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
}

func TestRunClientJoinsContextStdinReader(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	started := make(chan struct{})
	stopped := make(chan struct{})
	var startedOnce sync.Once
	var stoppedOnce sync.Once
	exitCode, err := RunClient(t.Context(), &ClientConfig{
		URL:   testWebTTYURL(server.URL),
		Stdin: strings.NewReader(""),
		StdinReadContext: func(ctx context.Context, _ []byte) (int, error) {
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
			stoppedOnce.Do(func() { close(stopped) })
			return 0, ctx.Err()
		},
		CmdArgs:       testShellCommand("exit 0", "exit 0"),
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	select {
	case <-started:
	default:
		t.Fatal("stdin reader did not start")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("RunClient returned before its stdin reader stopped")
	}
}

type uncancelableStdinReader struct{}

func (uncancelableStdinReader) Read([]byte) (int, error) { return 0, nil }

func TestRunClientRejectsUncancelableStdinReader(t *testing.T) {
	_, err := RunClient(t.Context(), &ClientConfig{Stdin: uncancelableStdinReader{}})
	if err == nil || !strings.Contains(err.Error(), "stdin reader must support cancellation") {
		t.Fatalf("RunClient() error = %v, want cancellable stdin error", err)
	}
}

func TestRunClientParallelNonInteractivePipes(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	const clients = 24
	results := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			stdin, input, err := os.Pipe()
			if err != nil {
				results <- err
				return
			}
			defer stdin.Close()
			expected := "client-" + strconv.Itoa(index)
			if _, err := input.WriteString(expected); err != nil {
				_ = input.Close()
				results <- err
				return
			}
			if err := input.Close(); err != nil {
				results <- err
				return
			}
			var stdout strings.Builder
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			exitCode, err := RunClient(ctx, &ClientConfig{
				URL:           testWebTTYURL(server.URL),
				Stdin:         stdin,
				Stdout:        &stdout,
				CmdArgs:       testShellCommand("cat", "$input | ForEach-Object { [Console]::Out.Write($_) }"),
				OpenDeadline:  durationPtr(time.Second),
				CloseDeadline: durationPtr(time.Second),
			})
			if err != nil {
				results <- err
				return
			}
			if exitCode != 0 || stdout.String() != expected {
				results <- errors.New("parallel WebTTY output mismatch")
			}
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("parallel RunClient() error = %v", err)
		}
	}
}

func TestWebTTYHandlerReportsOpenConfigErrors(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Open{Open: &pb.Open{}}})
	msg := readWebTTYMessage(t, conn)
	if msg.GetError() == nil || !strings.Contains(msg.GetError().Msg, "missing open config") {
		t.Fatalf("unexpected error message: %#v", msg)
	}
}

func TestWebTTYHandlerRejectsManagedAttach(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Attach{Attach: &pb.Attach{
		SessionId:     "session-1",
		ParticipantId: "participant-1",
		AttachGrant:   []byte("grant"),
		RequestedRole: pb.AttachRole_ATTACH_ROLE_SPECTATOR,
		Transport:     pb.AttachTransport_ATTACH_TRANSPORT_WEBSOCKET,
		Capabilities:  []pb.AttachCapability{pb.AttachCapability_ATTACH_CAPABILITY_READ_STREAM},
	}}})
	msg := readWebTTYMessage(t, conn)
	if msg.GetError() == nil || msg.GetError().Msg != managedAttachUnsupportedMessage {
		t.Fatalf("unexpected error message: %#v", msg)
	}
}

func TestWebTTYHandlerOpenTimeoutClosesIdleConnection(t *testing.T) {
	openDeadline := 20 * time.Millisecond
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{SessionOpenDeadline: &openDeadline}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected idle connection to be closed")
	}
}

func TestSessionRejectsMalformedMessages(t *testing.T) {
	session := &session{}
	cases := []*pb.Message{
		nil,
		{Payload: &pb.Message_Data{}},
		{Payload: &pb.Message_Error{}},
		{Payload: &pb.Message_Parameter{}},
		{Payload: &pb.Message_Parameter{Parameter: &pb.Parameter{Parameter: &pb.Parameter_TerminalSize{}}}},
	}
	for i, msg := range cases {
		if err := session.handleMessage(msg); err == nil {
			t.Fatalf("case %d: expected malformed message error", i)
		}
	}
	if err := session.handleData(nil); err == nil {
		t.Fatalf("expected nil data error")
	}
	if err := session.doResize(nil); err == nil {
		t.Fatalf("expected nil terminal size error")
	}
}

func TestWebTTYHandlerRequiresBearerToken(t *testing.T) {
	token := "test-token"
	handler := NewWebTTYHandler(&ServerConfig{AuthToken: &token})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	if _, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil); err == nil {
		t.Fatalf("expected unauthenticated websocket dial to fail")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), header)
	if err != nil {
		t.Fatalf("authenticated websocket dial failed: %v", err)
	}
	conn.Close()
}

func TestWebTTYHandlerRejectsCrossOrigin(t *testing.T) {
	allow := true
	handler := NewWebTTYHandler(&ServerConfig{AllowUnauthenticated: &allow})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	header := http.Header{}
	header.Set("Origin", "https://evil.example")
	if _, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), header); err == nil {
		t.Fatalf("expected cross-origin websocket dial to fail")
	}
}

func TestWebTTYHandlerAllowsExplicitWildcardOrigin(t *testing.T) {
	allow := true
	handler := NewWebTTYHandler(&ServerConfig{
		AllowUnauthenticated: &allow,
		AllowedOrigins:       []string{"*"},
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	header := http.Header{}
	header.Set("Origin", "http://localhost:3000")
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), header)
	if err != nil {
		t.Fatalf("expected wildcard origin websocket dial to pass: %v", err)
	}
	conn.Close()
}

func TestWebTTYOriginRejectsSameHostWhenUnauthenticated(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/webtty", nil)
	request.Host = "attacker.example"
	request.Header.Set("Origin", "http://attacker.example")
	if webTTYOriginAllowed(request, nil, false) {
		t.Fatalf("same-host origin should not be enough when unauthenticated mode is enabled")
	}
	if !webTTYOriginAllowed(request, nil, true) {
		t.Fatalf("same-host origin should remain allowed for authenticated handlers")
	}
	if !webTTYOriginAllowed(request, []string{"http://attacker.example"}, false) {
		t.Fatalf("explicit allowed origin should be honored")
	}
}

func TestWebTTYOriginNormalizesDefaultPorts(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://terminal.example/webtty", nil)
	request.Host = "terminal.example"
	request.Header.Set("Origin", "https://terminal.example:443")
	if !webTTYOriginAllowed(request, nil, true) {
		t.Fatalf("same-host origin with default HTTPS port should be allowed")
	}
	if !webTTYOriginAllowed(request, []string{"https://terminal.example"}, false) {
		t.Fatalf("explicit allowed origin should allow default HTTPS port")
	}
	request.Header.Set("Origin", "https://terminal.example:444")
	if webTTYOriginAllowed(request, nil, true) {
		t.Fatalf("same-host origin with non-default port should be rejected")
	}
}

func testServerConfig(cfg ServerConfig) *ServerConfig {
	allow := true
	cfg.AllowUnauthenticated = &allow
	return &cfg
}

func newTestE2ECryptoPair(t *testing.T, keyContext string) (*WebTTYEndpointIdentity, *WebTTYEndpointIdentity, *PayloadCrypto) {
	t.Helper()
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	clientCrypto, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{
		KeyContext: []byte(keyContext),
		Recipients: []E2ERecipient{{
			KeyID:     serverIdentity.Encryption.KeyID,
			PublicKey: serverIdentity.Encryption.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	return serverIdentity, clientIdentity, clientCrypto
}

func testWebTransportTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{http3.NextProtoH3}}
}

func collectClientSessionOutput(t *testing.T, session *ClientSession) (string, string, int, error) {
	t.Helper()
	resultCh := make(chan clientSessionResult, 1)
	go func() {
		exitCode, err := session.Wait()
		resultCh <- clientSessionResult{exitCode: exitCode, err: err}
	}()
	events := session.Events()
	var stdout strings.Builder
	var stderr strings.Builder
	result := clientSessionResult{exitCode: -1}
	haveResult := false
	for events != nil || !haveResult {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Stream == ClientSessionStderr {
				stderr.Write(event.Data)
			} else {
				stdout.Write(event.Data)
			}
		case result = <-resultCh:
			haveResult = true
			resultCh = nil
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for webtty session output")
		}
	}
	return stdout.String(), stderr.String(), result.exitCode, result.err
}

func waitForClientStdout(t *testing.T, session *ClientSession, want string) {
	t.Helper()
	events := session.Events()
	var stdout strings.Builder
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("webtty session events closed before stdout %q", want)
			}
			if event.Stream != ClientSessionStdout {
				continue
			}
			stdout.Write(event.Data)
			if strings.Contains(stdout.String(), want) {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for stdout %q; got %q", want, stdout.String())
		}
	}
}

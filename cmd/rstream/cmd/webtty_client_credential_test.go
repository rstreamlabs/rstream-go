// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestPreparedWorkspaceCredentialFIPSCompatibleReplayUsesOnlySelectedKeys(t *testing.T) {
	_, identity, raw := testWebTTYWorkspaceClientCredentialFixtureForOwner(t, true)
	credential, err := decodeWebTTYWorkspaceClientCredential(raw)
	if err != nil {
		t.Fatal(err)
	}
	config, err := webTTYWorkspaceRecordingConfig(webtty.E2EPayloadCryptoConfig{}, credential, identity)
	if err != nil {
		t.Fatal(err)
	}
	crypto, err := webtty.NewE2EClientPayloadCrypto(config)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := crypto.EncryptStdout(t.Context(), []byte("prepared recording"))
	if err != nil {
		t.Fatal(err)
	}
	identityJSON, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "workspace", "project", "server", "missing-scope", "identity", "missing-identity", "oversized", "invalid", "missing-grant", "denied", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("RSTREAM_DATA_DIR", directory)
			identityPath := filepath.Join(directory, "identity.json")
			credentialPath := filepath.Join(directory, "client-credential.json")
			t.Setenv(webTTYIdentityFileEnv, identityPath)
			t.Setenv(webTTYIdentityEnv, "")
			t.Setenv(webTTYClientCredentialFileEnv, credentialPath)
			selectedIdentity, selectedCredential := identityJSON, raw
			if scenario == "identity" {
				other, err := webtty.GenerateWebTTYEndpointIdentity()
				if err != nil {
					t.Fatal(err)
				}
				selectedIdentity, err = webtty.EncodeWebTTYEndpointIdentityJSON(*other)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing-identity" {
				t.Setenv(webTTYIdentityFileEnv, "")
			}
			if scenario == "oversized" {
				selectedCredential = bytes.Repeat([]byte("x"), webTTYMaxClientCredentialBytes+1)
			}
			if scenario == "invalid" {
				selectedCredential = []byte(`{"v":1}`)
			}
			if err := os.WriteFile(identityPath, selectedIdentity, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(credentialPath, selectedCredential, 0o600); err != nil {
				t.Fatal(err)
			}
			session := &rstream.WebTTYSession{
				ID: "session-prepared", WorkspaceID: config.WorkspaceID,
				ProjectID: config.ProjectID, ServerID: config.ServerID,
				EncryptionMode: rstream.WebTTYEncryptionModeE2E,
			}
			switch scenario {
			case "workspace":
				session.WorkspaceID = "other-workspace"
			case "project":
				session.ProjectID = "other-project"
			case "server":
				session.ServerID = "other-server"
			case "missing-scope":
				session.ServerID = ""
			}
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if !strings.HasSuffix(req.URL.Path, "/webtty/sessions/session-prepared/key-grants/decrypt-material") ||
					req.URL.Query().Get("recipient_id") != config.Recipients[0].ID ||
					req.URL.Query().Get("recipient_kind") != webtty.E2ERecipientKindWorkspaceDevice {
					t.Errorf("unexpected prepared recipient request: %s", req.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				if scenario == "denied" {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				grants := rstream.ListWebTTYKeyGrantDecryptMaterialResponse{}
				if scenario != "missing-grant" {
					grants = append(grants, rstream.WebTTYKeyGrantDecryptMaterial{
						ID: "grant-prepared", SessionID: session.ID,
						RecipientID: config.Recipients[0].ID, RecipientKind: webtty.E2ERecipientKindWorkspaceDevice,
						Crypto: testWebTTYKeyGrantMetadata(t, crypto.SessionKeyGrant),
					})
				}
				_ = json.NewEncoder(w).Encode(grants)
			}))
			defer server.Close()
			engine := strings.TrimPrefix(server.URL, "https://")
			token := "current-engine-token"
			client := &rstream.Client{
				EngineURL: &engine, Token: &token,
				TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone(),
			}
			ctx := t.Context()
			if scenario == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			prepared, err := webTTYSessionAttachPayloadCrypto(ctx, nil, client, session)
			if scenario != "valid" {
				if err == nil || prepared != nil {
					t.Fatalf("unsafe prepared replay was accepted: %v", err)
				}
				wantRequests := int64(0)
				if scenario == "missing-grant" || scenario == "denied" {
					wantRequests = 1
				}
				if requests.Load() != wantRequests {
					t.Fatalf("invalid selection triggered %d requests, want %d", requests.Load(), wantRequests)
				}
				return
			}
			if err != nil || prepared == nil {
				t.Fatalf("prepared replay failed: %v", err)
			}
			if requests.Load() != 1 || prepared.DeviceID != config.Recipients[0].ID || !bytes.Equal(prepared.ClientCredential, raw) {
				t.Fatalf("prepared replay lost its selected device: %+v, requests %d", prepared, requests.Load())
			}
			plaintext, err := prepared.PayloadCrypto.DecryptStdout(ctx, encrypted)
			if err != nil || string(plaintext) != "prepared recording" {
				t.Fatalf("prepared recording did not decrypt: %q, %v", plaintext, err)
			}
		})
	}
}

func TestPreparedWorkspaceCredentialFIPSCompatibleRecordingRecipients(t *testing.T) {
	_, identity, credential := testWebTTYWorkspaceClientCredentialFixtureForOwner(t, true)
	envelope, err := decodeWebTTYWorkspaceClientCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	server, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public := server.Public()
	for _, existingClientRecipient := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-client-recipient-%t", existingClientRecipient), func(t *testing.T) {
			payloadConfig := webtty.E2EPayloadCryptoConfig{Recipients: []webtty.E2ERecipient{{
				KeyID: server.Encryption.KeyID, PublicKey: server.Encryption.PublicKey,
			}}}
			if existingClientRecipient {
				payloadConfig.Recipients, err = appendWebTTYE2EClientEndpointRecipient(payloadConfig.Recipients, *identity)
				if err != nil {
					t.Fatal(err)
				}
			}
			payloadCrypto, err := webtty.NewE2EClientPayloadCrypto(payloadConfig)
			if err != nil {
				t.Fatal(err)
			}
			config, err := withWebTTYWorkspaceRecordingRecipient(webTTYClientCryptoConfig{
				PayloadCrypto: payloadCrypto, payloadConfig: &payloadConfig,
				EndpointIdentity: identity, ExpectedServerIdentity: &public,
			}, envelope)
			if err != nil {
				t.Fatal(err)
			}
			if len(config.payloadConfig.Recipients) != 2 {
				t.Fatalf("duplicate or missing recording recipient: %+v", config.payloadConfig.Recipients)
			}
			if got := config.payloadConfig.Recipients[1]; got.ID != workspaceTrustString(envelope.Payload, "device_key_id") || got.Kind != webtty.E2ERecipientKindWorkspaceDevice {
				t.Fatalf("recording recipient does not identify the approved device: %+v", got)
			}
			var keyContext map[string]any
			if err := json.Unmarshal(config.PayloadCrypto.SessionKeyGrant.KeyContext, &keyContext); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"workspace_id", "project_id", "server_id"} {
				if keyContext[field] != envelope.Payload[field] {
					t.Fatalf("recording context %s = %v, want %v", field, keyContext[field], envelope.Payload[field])
				}
			}
			if _, err := webtty.NewE2EServerPayloadCrypto(config.PayloadCrypto.SessionKeyGrant, identity.Encryption); err != nil {
				t.Fatalf("approved device cannot decrypt its own recording grant: %v", err)
			}
			if _, err := webtty.NewE2EServerPayloadCrypto(config.PayloadCrypto.SessionKeyGrant, server.Encryption); err != nil {
				t.Fatalf("registered server recipient was lost: %v", err)
			}
		})
	}
	for _, scenario := range []string{"identity", "workspace", "project", "server"} {
		t.Run("reject-"+scenario, func(t *testing.T) {
			payloadConfig := webtty.E2EPayloadCryptoConfig{}
			client := identity
			switch scenario {
			case "identity":
				client = server
			case "workspace":
				payloadConfig.WorkspaceID = "other-workspace"
			case "project":
				payloadConfig.ProjectID = "other-project"
			case "server":
				payloadConfig.ServerID = "other-server"
			}
			if _, err := withWebTTYWorkspaceRecordingRecipient(webTTYClientCryptoConfig{payloadConfig: &payloadConfig, EndpointIdentity: client}, envelope); err == nil {
				t.Fatal("mismatched prepared credential was accepted")
			}
		})
	}
}

func TestWebTTYClientPreparationPortableFiles(t *testing.T) {
	_, identity, credential := testWebTTYWorkspaceClientCredentialFixture(t)
	server, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "prepared")
	files, err := writeWebTTYClientPreparation(directory, "server-runtime", *identity, server.Public(), credential)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v", files)
	}
	loadedIdentity, err := webtty.LoadWebTTYEndpointIdentityFile(files["identity.json"])
	if err != nil || !reflect.DeepEqual(loadedIdentity, identity) {
		t.Fatalf("prepared identity differs from approved device: %v", err)
	}
	loadedServer, err := webtty.LoadKnownServerEndpointIdentitiesFile(files["known-servers.json"])
	if err != nil || len(loadedServer) != 1 || !reflect.DeepEqual(loadedServer[0], server.Public()) {
		t.Fatalf("prepared trust differs from resolved server: %v", err)
	}
	loadedCredential, err := os.ReadFile(files["client-credential.json"])
	if err != nil || !bytes.Equal(loadedCredential, credential) {
		t.Fatalf("prepared credential differs: %v", err)
	}
	for _, path := range append([]string{directory}, files["identity.json"], files["known-servers.json"], files["client-credential.json"]) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("prepared path accessible to another user: %v", info.Mode())
		}
	}
	if _, err := writeWebTTYClientPreparation(directory, "other-server", *identity, server.Public(), credential); err == nil {
		t.Fatal("existing preparation was overwritten")
	}
	unchanged, err := os.ReadFile(files["client-credential.json"])
	if err != nil || !bytes.Equal(unchanged, credential) {
		t.Fatalf("existing preparation changed: %v", err)
	}
}

func TestWebTTYClientPreparationRejectsInvalidMaterialBeforeWriting(t *testing.T) {
	_, identity, credential := testWebTTYWorkspaceClientCredentialFixture(t)
	server, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"credential", "oversized", "identity", "server"} {
		t.Run(scenario, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "prepared")
			client := *identity
			public := server.Public()
			raw := credential
			switch scenario {
			case "credential":
				raw = []byte(`{"v":2}`)
			case "oversized":
				raw = bytes.Repeat([]byte("x"), webTTYMaxClientCredentialBytes+1)
			case "identity":
				client = webtty.WebTTYEndpointIdentity{}
			case "server":
				public = webtty.WebTTYEndpointIdentityPublic{}
			}
			if _, err := writeWebTTYClientPreparation(directory, "server", client, public, raw); err == nil {
				t.Fatal("invalid preparation accepted")
			}
			if _, err := os.Lstat(directory); !os.IsNotExist(err) {
				t.Fatalf("invalid preparation left files: %v", err)
			}
		})
	}
}

func TestWebTTYClientPreparationFIPSCompatibleServerSuite(t *testing.T) {
	_, identity, credential := testWebTTYWorkspaceClientCredentialFixtureForOwner(t, true)
	server, err := webtty.GenerateWebTTYEndpointIdentityForSuite(webtty.KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce)
	if err != nil {
		t.Fatal(err)
	}
	files, err := writeWebTTYClientPreparation(filepath.Join(t.TempDir(), "prepared"), "server-p256", *identity, server.Public(), credential)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := webtty.LoadKnownServerEndpointIdentitiesFile(files["known-servers.json"])
	if err != nil || len(loaded) != 1 || !reflect.DeepEqual(loaded[0], server.Public()) {
		t.Fatalf("P-256 trust did not round trip: %v", err)
	}
}

func TestWebTTYLocalWorkspaceCredentialExecutesWithoutControlPlane(t *testing.T) {
	enrollment, clientIdentity, credential := testWebTTYWorkspaceClientCredentialFixture(t)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatal(err)
	}
	serverPublic := serverIdentity.Public()
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{
		AllowUnauthenticated:   rstream.BoolPtr(true),
		RequireSessionKeyGrant: rstream.BoolPtr(true),
		RequireClientProof:     rstream.BoolPtr(true),
		PayloadCryptoResolver:  webtty.NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		EndpointIdentity:       serverIdentity,
		ClientProofVerifier:    webTTYWorkspaceClientProofVerifier(enrollment),
		WorkspaceID:            enrollment.WorkspaceID,
		ProjectID:              enrollment.ProjectID,
		ServerID:               enrollment.ServerID,
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	for _, scenario := range []string{"valid", "tampered", "oversized", "without-trust"} {
		t.Run(scenario, func(t *testing.T) {
			payloadCrypto, err := webtty.NewE2EClientPayloadCrypto(webtty.E2EPayloadCryptoConfig{
				Recipients: []webtty.E2ERecipient{{KeyID: serverIdentity.Encryption.KeyID, PublicKey: serverIdentity.Encryption.PublicKey}},
			})
			if err != nil {
				t.Fatal(err)
			}
			config := webTTYClientCryptoConfig{PayloadCrypto: payloadCrypto, ExpectedServerIdentity: &serverPublic, EndpointIdentity: clientIdentity}
			raw := credential
			switch scenario {
			case "tampered":
				raw = testMutateWebTTYWorkspaceClientCredentialWithoutResigning(t, credential, func(payload map[string]any) { payload["workspaceId"] = "other-workspace" })
			case "oversized":
				raw = bytes.Repeat([]byte("x"), webTTYMaxClientCredentialBytes+1)
			case "without-trust":
				config.ExpectedServerIdentity = nil
			}
			path := filepath.Join(t.TempDir(), "credential.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			config, err = withWebTTYClientCredential(config, path)
			if scenario == "oversized" || scenario == "without-trust" {
				if err == nil {
					t.Fatal("unsafe credential configuration was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			command := []string{"sh", "-c", "printf offline-workspace-credential"}
			if runtime.GOOS == "windows" {
				command = []string{"cmd.exe", "/c", "echo offline-workspace-credential"}
			}
			var stdout, stderr bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			exitCode, err := webtty.RunClient(ctx, &webtty.ClientConfig{
				URL:       "ws" + strings.TrimPrefix(server.URL, "http"),
				Transport: webtty.WebTTYTransportWebSocket,
				CmdArgs:   command,
				Stdin:     strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
				PayloadCrypto:          config.PayloadCrypto,
				EndpointIdentity:       config.EndpointIdentity,
				ExpectedServerIdentity: config.ExpectedServerIdentity,
				ClientCredential:       config.ClientCredential,
			})
			if scenario == "tampered" {
				if err == nil || stdout.Len() != 0 {
					t.Fatalf("tampered credential executed: %d, %v, %q", exitCode, err, stdout.String())
				}
				return
			}
			if err != nil || exitCode != 0 || strings.TrimSpace(stdout.String()) != "offline-workspace-credential" || stderr.Len() != 0 {
				t.Fatalf("offline execution = %d, %v, %q, %q", exitCode, err, stdout.String(), stderr.String())
			}
		})
	}
	t.Run("MCP with prepared files", func(t *testing.T) {
		clearRstreamTestEnv(t)
		files, err := writeWebTTYClientPreparation(filepath.Join(t.TempDir(), "prepared"), "server", *clientIdentity, serverPublic, credential)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(webTTYIdentityFileEnv, files["identity.json"])
		t.Setenv(webTTYKnownServersFileEnv, files["known-servers.json"])
		t.Setenv(webTTYClientCredentialFileEnv, files["client-credential.json"])
		command := []string{"sh", "-c", "printf offline-mcp-credential"}
		if runtime.GOOS == "windows" {
			command = []string{"cmd.exe", "/c", "echo offline-mcp-credential"}
		}
		url, err := json.Marshal("ws" + strings.TrimPrefix(server.URL, "http"))
		if err != nil {
			t.Fatal(err)
		}
		commandJSON, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		params := map[string]json.RawMessage{"url": url, "command": commandJSON}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		result, err := mcpWebTTYExec(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		content, ok := result["structuredContent"].(map[string]any)
		stdout, _ := content["stdout"].(string)
		if !ok || result["isError"] == true || content["exit_code"] != float64(0) ||
			strings.TrimSpace(stdout) != "offline-mcp-credential" || content["stderr"] != "" {
			t.Fatalf("prepared credential did not authorize the MCP command: %#v", result)
		}
		otherIdentity := filepath.Join(t.TempDir(), "identity.json")
		if _, err := webtty.LoadOrCreateWebTTYEndpointIdentityFile(otherIdentity); err != nil {
			t.Fatal(err)
		}
		t.Setenv(webTTYIdentityFileEnv, otherIdentity)
		if _, err := mcpWebTTYExec(ctx, params); err == nil || !strings.Contains(err.Error(), "does not match the approved endpoint identity") {
			t.Fatalf("prepared credential incorrectly accepted a different identity: %v", err)
		}
	})
}

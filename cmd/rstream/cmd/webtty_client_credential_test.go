// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

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
}

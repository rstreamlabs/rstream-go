// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestBrowserLoginRetainsExplicitPermissionsForBothProtocolsAndServerGenerations(t *testing.T) {
	requested := []string{"account.user.read-only", "network.webhooks.read-write"}
	for _, flow := range []string{loginAuthFlowOAuth, loginAuthFlowLegacy} {
		for _, ownerAware := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/owner-aware-%t", flow, ownerAware), func(t *testing.T) {
				t.Setenv("PATH", t.TempDir())
				version := 0
				var versions []int
				if ownerAware {
					version = controlplane.NativeCredentialOwnerVersion
					versions = []int{version}
				}
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/.well-known/oauth-authorization-server":
						_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token", CredentialOwnerVersionsSupported: versions})
					case "/oauth/device_authorization":
						requests++
						form := readOAuthTestForm(t, r)
						wantVersion := ""
						if ownerAware {
							wantVersion = "1"
						}
						if form.Get("scope") != strings.Join(requested, " ") || form.Get("rstream_credential_owner_version") != wantVersion {
							t.Errorf("explicit OAuth request changed: %s", form.Encode())
						}
						_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device", UserCode: "ABCD-EFGH", VerificationURI: serverURL(r, "/activate"), ExpiresIn: 60})
					case "/api/rstream/login/requests":
						requests++
						var body controlplane.RstreamLoginRequest
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if !reflect.DeepEqual(body.Permissions, requested) || body.CredentialOwnerVersion != version {
							t.Errorf("explicit JSON request changed: %+v", body)
						}
						_ = json.NewEncoder(w).Encode(controlplane.RstreamLoginResponse{RequestID: "request", RequestSecret: "secret", URL: serverURL(r, "/activate")})
					case "/oauth/token":
						_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceTokenResponse{AccessToken: "issued-token", TokenType: "Bearer", Scope: strings.Join(requested, " ")})
					case "/api/rstream/login/requests/request/token":
						_ = json.NewEncoder(w).Encode(controlplane.RstreamLoginTokenResponse{Status: "issued", Token: "issued-token"})
					case "/api/whoami":
						if r.Header.Get("Authorization") != "Bearer issued-token" {
							t.Error("issued proof was not used for identity verification")
						}
						_, _ = w.Write([]byte(`{"owner":{"kind":"user","userId":"operator"},"role":"user","credentialId":"issued","permissions":["account.user.read-only","network.webhooks.read-write"]}`))
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				cmd := loginTokenStorageCommand()
				cmd.SetContext(t.Context())
				cmd.Flags().StringArray("permission", nil, "")
				cmd.Flags().String("output", "json", "")
				for _, permission := range requested {
					if err := cmd.Flags().Set("permission", " "+permission+" "); err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(t.TempDir(), "config.yaml")
				output, err := captureStdout(t, func() error {
					if flow == loginAuthFlowLegacy {
						return runLegacyDeviceLogin(cmd, path, config.Config{}, server.URL)
					}
					return runOAuthDeviceLogin(cmd, path, config.Config{}, server.URL)
				})
				if err != nil || requests != 1 || strings.Contains(output, "issued-token") {
					t.Fatalf("requests=%d output=%q error=%v", requests, output, err)
				}
				var result loginResult
				if err := json.Unmarshal([]byte(output), &result); err != nil || result.Identity == nil || result.Identity.Permissions == nil || !reflect.DeepEqual(*result.Identity.Permissions, requested) {
					t.Fatalf("issued scope was not reported: %s (%v)", output, err)
				}
			})
		}
	}
}

func TestLoginExplicitPermissionInputRejectsEmptyInsteadOfSelectingDefaults(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().StringArray("permission", nil, "")
	permissions, err := loginRequestedPermissions(cmd)
	if err != nil || permissions != nil {
		t.Fatalf("omission did not select presets: %v, %v", permissions, err)
	}
	if err := cmd.Flags().Set("permission", "  "); err != nil {
		t.Fatal(err)
	}
	if _, err := loginRequestedPermissions(cmd); err == nil {
		t.Fatal("an empty explicit request selected default permissions")
	}
}

func TestLoginPermissionFlagCannotBeCombinedWithManualTokenInput(t *testing.T) {
	if loginCmd.Flags().Lookup("permission") == nil {
		t.Fatal("login does not expose explicit permission requests")
	}
	for _, name := range []string{"token-stdin", "token-file", "stdin"} {
		command := &cobra.Command{}
		loginCmd.Flags().VisitAll(func(flag *pflag.Flag) {
			copy := *flag
			copy.Changed = false
			command.Flags().AddFlag(&copy)
		})
		command.Flags().Lookup("permission").Changed = true
		command.Flags().Lookup(name).Changed = true
		err := command.ValidateFlagGroups()
		if err == nil || !strings.Contains(err.Error(), "permission") || !strings.Contains(err.Error(), name) {
			t.Errorf("manual %s input accepted permission changes: %v", name, err)
		}
	}
}

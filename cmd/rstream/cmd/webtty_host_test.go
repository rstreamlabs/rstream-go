// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"testing"

	"github.com/rstreamlabs/rstream-go/webtty"
)

// Host configuration must survive registration and reconnects without changing
// transport, edge authentication, or the registered server's E2E identity.
func TestWebTTYHostPublicationMatrix(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"cli", "yaml"} {
		for _, transport := range []string{"websocket", "webtransport"} {
			for _, mode := range []string{"lightweight", "registered", "registered-e2e"} {
				for _, hostname := range []string{"shell.example.com", "shell.t.engine.example.com", ""} {
					t.Run(source+"/"+transport+"/"+mode+"/"+hostname, func(t *testing.T) {
						cmd := newTestWebTTYServerCommand()
						var cfg *webTTYServerRuntimeConfig
						serverID := ""
						if mode != "lightweight" {
							serverID = "server-1"
						}
						if source == "cli" {
							args := []string{"--rstream", "--transport=" + transport}
							if serverID != "" {
								args = append(args, "--server-id="+serverID, "--login-user=operator")
							}
							if hostname != "" {
								args = append(args, "--host="+hostname)
							}
							if err := cmd.ParseFlags(args); err != nil {
								t.Fatal(err)
							}
						} else {
							contents := fmt.Sprintf("server:\n  rstream: true\n  transport: %s\n", transport)
							if serverID != "" {
								contents += "  serverId: " + serverID + "\n  loginUser: operator\n"
							}
							if hostname != "" {
								contents += "  host: " + hostname + "\n"
							}
							var err error
							cfg, err = loadWebTTYServerRuntimeConfig(writeWebTTYRuntimeConfigFixture(t, contents))
							if err != nil {
								t.Fatal(err)
							}
						}
						if err := applyWebTTYServerRuntimeConfigValues(cmd, cfg); err != nil {
							t.Fatal(err)
						}
						if err := applyWebTTYServerDerivedDefaults(cmd); err != nil {
							t.Fatal(err)
						}
						if err := validateWebTTYServerFlags(cmd); err != nil {
							t.Fatal(err)
						}
						var enrollment *webTTYServerEnrollmentFile
						if serverID != "" {
							enrollment = &webTTYServerEnrollmentFile{ServerID: serverID, ServerPublicKey: webtty.EncodeE2EKeyMaterial(identity.PublicKey)}
							if mode == "registered-e2e" {
								enrollment.EncryptionPolicy = webTTYServerEncryptionPolicyExplicitKey
							}
						}
						var stable *string
						for attempt := 0; attempt < 3; attempt++ {
							props := newWebTTYServerTunnelProperties(cmd, enrollment)
							if err := applyWebTTYStableHostname(&props, "https://engine.example.com", &stable); err != nil {
								t.Fatal(err)
							}
							if props.Hostname == nil || *props.Hostname == "" || stable == nil || *props.Hostname != *stable || (hostname != "" && *props.Hostname != hostname) {
								t.Fatal("published hostname was lost or changed")
							}
							if props.Publish == nil || !*props.Publish || props.TokenAuth == nil || !*props.TokenAuth {
								t.Fatal("published WebTTY endpoint lost token authentication")
							}
							wantProtocol := "http"
							if enrollment != nil {
								wantProtocol = "webtty"
								if props.Name == nil || *props.Name != serverID || props.Labels[webtty.WebTTYServerIDLabelKey] != serverID || props.HTTPVersion != nil {
									t.Fatal("registered server routing identity changed")
								}
							}
							if props.Protocol == nil || string(*props.Protocol) != wantProtocol || props.Labels[webtty.WebTTYTransportLabelKey] != transport {
								t.Fatalf("unexpected protocol/transport: %#v", props)
							}
							if transport == "webtransport" && (props.Type == nil || string(*props.Type) != "datagram") {
								t.Fatal("WebTransport lost datagram tunnel type")
							}
							if mode == "registered-e2e" && (props.Labels[webtty.WebTTYE2ELabelKey] != webtty.WebTTYE2ERequired || props.Labels[webtty.WebTTYClientProofLabelKey] != webtty.WebTTYClientProofRequired || props.Labels[webtty.WebTTYHostKeyIDLabelKey] != webtty.EncodeE2EKeyMaterial(webtty.E2EKeyID(identity.PublicKey))) {
								t.Fatal("hostname selection changed the registered server's E2E identity or requirements")
							}
						}
					})
				}
			}
		}
	}
}

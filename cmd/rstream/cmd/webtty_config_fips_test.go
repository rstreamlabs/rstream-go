//go:build rstream_fips

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestFIPSProfileWebTTYEffectiveConfiguration(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	for _, transport := range []string{"plain", "websocket"} {
		for _, source := range []string{"cli", "yaml"} {
			t.Run(transport+"/"+source, func(t *testing.T) {
				root := newRootCmd()
				root.SetOut(io.Discard)
				root.SetErr(io.Discard)
				parent := &cobra.Command{Use: "webtty"}
				server := newTestWebTTYServerCommand()
				server.RunE = webttyServerCmd.RunE
				if err := server.Flags().Lookup("transport").Value.Set(defaultWebTTYServerTransportFlag()); err != nil {
					t.Fatal(err)
				}
				root.AddCommand(parent)
				parent.AddCommand(server)
				args := []string{"webtty", "server", "--allow-unauthenticated"}
				if source == "cli" {
					args = append(args, "--transport", transport)
				} else {
					path := writeWebTTYRuntimeConfigFixture(t, "version: 1\nserver:\n  transport: "+transport+"\n")
					args = append(args, "--webtty-config", path)
				}
				root.SetArgs(args)
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				err := root.ExecuteContext(ctx)
				if err == nil || !strings.Contains(err.Error(), "requires --transport=webtransport") {
					t.Fatalf("expected early FIPS refusal: %v", err)
				}
				t.Logf("%s %s: %v", source, transport, err)
			})
		}
	}
}

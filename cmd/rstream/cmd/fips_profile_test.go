//go:build rstream_fips

// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

func TestFIPSProfileVersionIdentifiesModule(t *testing.T) {
	version := rootVersion()
	if !strings.Contains(version, "FIPS 140-3 profile") || !strings.Contains(version, rstream.CurrentFIPSStatus().ModuleBuild) {
		t.Fatalf("rootVersion() = %q", version)
	}
}

func TestFIPSProfileCommandPolicy(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "webtty client", want: "requires --transport=webtransport"},
		{path: "ui", want: "ui is not available"},
		{path: "mcp serve", want: "mcp is not available"},
		{path: "webtty sessions join", want: "managed participant streams do not yet support WebTransport"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			root := &cobra.Command{Use: "rstream"}
			parent := root
			parts := strings.Fields(test.path)
			for _, part := range parts {
				child := &cobra.Command{Use: part}
				parent.AddCommand(child)
				parent = child
			}
			if strings.HasPrefix(test.path, "webtty ") {
				parent.Flags().String("transport", "websocket", "")
			}
			err := validateFIPSCommand(parent)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFIPSCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFIPSProfileAllowsWebTTYWebTransport(t *testing.T) {
	root := &cobra.Command{Use: "rstream"}
	webttyCommand := &cobra.Command{Use: "webtty"}
	clientCommand := &cobra.Command{Use: "client"}
	clientCommand.Flags().String("transport", "webtransport", "")
	root.AddCommand(webttyCommand)
	webttyCommand.AddCommand(clientCommand)
	if err := validateFIPSCommand(clientCommand); err != nil {
		t.Fatalf("validateFIPSCommand() error = %v", err)
	}
}

func TestFIPSProfileAllowsWorkspaceDeviceEnrollment(t *testing.T) {
	root := &cobra.Command{Use: "rstream"}
	workspaceCommand := &cobra.Command{Use: "workspace"}
	deviceCommand := &cobra.Command{Use: "device"}
	enrollCommand := &cobra.Command{Use: "enroll"}
	root.AddCommand(workspaceCommand)
	workspaceCommand.AddCommand(deviceCommand)
	deviceCommand.AddCommand(enrollCommand)
	if err := validateFIPSCommand(enrollCommand); err != nil {
		t.Fatalf("validateFIPSCommand() error = %v", err)
	}
}

func TestFIPSProfileWorkspaceDeviceUsesP256WebTTYIdentity(t *testing.T) {
	material, err := generateWorkspaceDeviceMaterial("workspace-fips", workspaceDeviceKindCLI, "FIPS CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	if material.file.WebTTYKeyAlgorithm != webtty.WebTTYKeyAlgorithmP256 {
		t.Fatalf("WebTTY key algorithm = %q", material.file.WebTTYKeyAlgorithm)
	}
	if material.webttyIdentity.KeyEnvelopeSuite != webtty.KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce {
		t.Fatalf("WebTTY key envelope suite = %d", material.webttyIdentity.KeyEnvelopeSuite)
	}
}

func TestFIPSProfileWebTTYTransportDefaults(t *testing.T) {
	if got := defaultWebTTYServerTransportFlag(); got != string(webtty.WebTTYTransportWebTransport) {
		t.Fatalf("server transport default = %q", got)
	}
	if got := defaultWebTTYClientTransportFlag(); got != string(webtty.WebTTYTransportWebTransport) {
		t.Fatalf("client transport default = %q", got)
	}
}

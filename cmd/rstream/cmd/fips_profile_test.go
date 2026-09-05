//go:build rstream_fips

// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
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
		{path: "webtty client", want: "webtty is not available"},
		{path: "ui", want: "ui is not available"},
		{path: "mcp serve", want: "mcp is not available"},
		{path: "workspace device enroll", want: "workspace device is not available"},
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
			err := validateFIPSCommand(parent)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFIPSCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}

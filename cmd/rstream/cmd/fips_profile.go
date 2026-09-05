// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

func rootVersion() string {
	if !rstream.FIPSProfileEnabled() {
		return rstream.Version
	}
	status := rstream.CurrentFIPSStatus()
	return fmt.Sprintf("%s (FIPS 140-3 profile, Go module %s)", rstream.Version, status.ModuleBuild)
}

func validateFIPSRuntime() error {
	if !rstream.FIPSProfileEnabled() {
		return nil
	}
	return rstream.RequireFIPS()
}

func validateFIPSCommand(cmd *cobra.Command) error {
	if !rstream.FIPSProfileEnabled() {
		return nil
	}
	if err := validateFIPSRuntime(); err != nil {
		return err
	}
	path := cmd.CommandPath()
	for _, prefix := range []string{
		"rstream webtty",
		"rstream ui",
		"rstream mcp",
		"rstream workspace device",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+" ") {
			return fmt.Errorf("%s is not available in the current rstream FIPS profile", strings.TrimPrefix(prefix, "rstream "))
		}
	}
	if path == "rstream events" {
		transport, err := cmd.Flags().GetString("transport")
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(transport), "websocket") {
			return fmt.Errorf("WebSocket events are not available in the current rstream FIPS profile; use --transport=sse")
		}
		insecure, err := cmd.Flags().GetBool("forward-insecure-tls")
		if err != nil {
			return err
		}
		if insecure {
			return fmt.Errorf("--forward-insecure-tls is not available in the rstream FIPS profile")
		}
	}
	return nil
}

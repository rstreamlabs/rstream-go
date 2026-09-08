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
	return fmt.Sprintf("%s (FIPS 140-3 profile, Go module %s, quic-go %s, webtransport-go %s)", rstream.Version, status.ModuleBuild, status.QUICVersion, status.WebTransportVersion)
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
		"rstream ui",
		"rstream mcp",
		"rstream files",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+" ") {
			return fmt.Errorf("%s is not available in the current rstream FIPS profile", strings.TrimPrefix(prefix, "rstream "))
		}
	}
	if path == "rstream webtty fs" || strings.HasPrefix(path, "rstream webtty fs ") {
		return fmt.Errorf("webtty fs is not available in the current rstream FIPS profile")
	}
	if path == "rstream webtty sessions join" {
		return fmt.Errorf("webtty sessions join is not available in the current rstream FIPS profile because managed participant streams do not yet support WebTransport")
	}
	for _, livePath := range []string{
		"rstream webtty server",
		"rstream webtty client",
		"rstream webtty exec",
	} {
		if path != livePath {
			continue
		}
		transport, err := cmd.Flags().GetString("transport")
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(transport), "webtransport") {
			return fmt.Errorf("%s requires --transport=webtransport in the rstream FIPS profile", strings.TrimPrefix(livePath, "rstream "))
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

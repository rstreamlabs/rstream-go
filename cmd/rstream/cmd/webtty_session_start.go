// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type webTTYSessionStartResult struct {
	SessionID string `json:"session_id"`
}

var webttySessionsStartCmd = &cobra.Command{
	Use:          "start [--] cmd...",
	Short:        "Start a bounded detached WebTTY command and return its session ID",
	GroupID:      "webtty-session-primary",
	SilenceUsage: true,
	Args:         cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYClientWithOptions(cmd, "", args, webTTYClientRunOptions{DefaultOutput: "json", ForceNoInteractive: true, ForceNoTTY: true, Subcommand: "sessions start", Detached: true})
	},
}

func init() {
	addWebTTYClientFlags(webttySessionsStartCmd, "json")
	webttySessionsStartCmd.Flags().Int("execution-seconds", 0, "required absolute execution budget in seconds (1..86400, also bounded by the Engine)")
	webttySessionsCmd.AddCommand(webttySessionsStartCmd)
}

func mcpWebTTYSessionStartTool() map[string]any {
	return mcpTool("rstream_webtty_session_start", "Start a bounded detached non-interactive command and return its acknowledged session ID. Requires a published WebTTY endpoint discovered through the configured Engine. Stdin is empty; inspect or join the returned session instead of relaunching the command. Never automatically retry an unconfirmed start.", mcpProjectSelectorProperties(mcpWebTTYConnectionProperties(map[string]any{
		"url":               mcpStringSchema("rstrm:// target in the configured Engine inventory."),
		"exec_path":         mcpStringSchema("Advertised execution path; defaults to /."),
		"known_server":      mcpStringSchema("Optional local known WebTTY server name."),
		"command":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
		"execution_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 86400, "description": "Absolute execution budget; the Engine may advertise a smaller maximum."},
		"workdir":           mcpStringSchema("Optional working directory."),
		"env":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"user":              mcpStringSchema("Optional username or UID."),
	})), []string{"url", "command", "execution_seconds"})
}

func validateWebTTYDetachedStart(rawURL string, seconds int, noDiscovery, interactive, tty bool) error {
	if seconds < 1 || seconds > 86400 {
		return fmt.Errorf("execution-seconds must be between 1 and 86400")
	}
	if _, err := webTTYClientRstreamTarget(rawURL); err != nil {
		return fmt.Errorf("detached WebTTY start requires an rstrm:// target discovered through the configured engine")
	}
	if noDiscovery || interactive || tty {
		return fmt.Errorf("detached WebTTY start requires discovery and a non-interactive command without a TTY")
	}
	return nil
}

func configureWebTTYDetachedStart(ctx context.Context, cfg *webtty.ClientConfig, client *rstream.Client, resolution webTTYClientRstreamResolution, seconds int) error {
	if resolution.PublishedHost == "" {
		return fmt.Errorf("detached WebTTY start requires a published endpoint so the engine can own the execution lifetime")
	}
	capabilities, err := client.GetWebTTYCapabilities(ctx)
	if err != nil {
		return fmt.Errorf("failed to read WebTTY capabilities: %w", err)
	}
	if !capabilities.ManagedProtocol || !capabilities.DetachedExecution {
		return fmt.Errorf("detached WebTTY execution is not available on this engine")
	}
	endpoint, err := url.Parse(cfg.URL)
	if err != nil {
		return err
	}
	endpoint.Host = resolution.PublishedHost
	endpoint.Scheme = "https"
	if cfg.Transport == webtty.WebTTYTransportWebSocket {
		endpoint.Scheme = "wss"
	}
	cfg.URL = endpoint.String()
	if cfg.AuthToken == nil && client.Token != nil && strings.TrimSpace(*client.Token) != "" {
		cfg.AuthToken = new(strings.TrimSpace(*client.Token))
	}
	if cfg.TLSConfig == nil && client.TLSClientConfig != nil {
		cfg.TLSConfig = client.TLSClientConfig.Clone()
		cfg.TLSConfig.ServerName = webTTYPublishedServerName(resolution.PublishedHost)
	}
	cfg.Managed = &webtty.ManagedSessionConfig{Capabilities: *capabilities, ExecutionSeconds: seconds}
	return nil
}

func startWebTTYSession(ctx context.Context, cfg *webtty.ClientConfig) (webTTYSessionStartResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	stream, err := webtty.OpenClientSession(ctx, webTTYSessionConfigFromClientConfig(cfg))
	if err != nil {
		return webTTYSessionStartResult{}, fmt.Errorf("detached WebTTY start was not confirmed; do not automatically retry: %w", err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range stream.Events() {
		}
	}()
	defer func() {
		_ = stream.Close()
		<-drained
	}()
	result := webTTYSessionStartResult{SessionID: stream.ManagedSessionID()}
	if err := stream.SendEOF(); err != nil {
		return result, fmt.Errorf("detached WebTTY command was acknowledged but closing stdin failed: %w", err)
	}
	if err := stream.Disconnect(ctx); err != nil {
		return result, fmt.Errorf("detached WebTTY command was acknowledged but reader disconnect was not confirmed: %w", err)
	}
	return result, nil
}

func mcpWebTTYSessionStart(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	seconds, err := mcpOptionalIntArg(args, "execution_seconds")
	if err != nil || seconds == nil || *seconds < 1 || *seconds > 86400 {
		return nil, fmt.Errorf("execution_seconds must be an integer between 1 and 86400")
	}
	command, err := mcpRequiredStringSliceArg(args, "command")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 65*time.Second)
	defer cancel()
	cfg, err := mcpWebTTYCommandConfig(ctx, args, command, *seconds)
	if err != nil {
		return nil, err
	}
	result, startErr := startWebTTYSession(ctx, cfg.ClientConfig)
	startErr = errors.Join(startErr, cfg.Close())
	payload := map[string]any{"surface": "cli", "session_id": result.SessionID}
	if startErr != nil {
		payload["error"] = "WebTTY start or reader cleanup was not confirmed; do not automatically retry."
		payload["error_kind"] = mcpWebTTYExecErrorKind(startErr)
	}
	return mcpJSONResult(payload, startErr != nil)
}

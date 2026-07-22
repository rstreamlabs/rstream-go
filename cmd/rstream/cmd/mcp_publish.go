// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

const (
	mcpApplicationProtocol    = "rstream.mcp"
	mcpApplicationProtocolKey = "application-protocol"
	mcpHTTPPath               = "/mcp"
	mcpPathLabel              = "rstream.mcp.path"
	mcpTransportLabel         = "rstream.mcp.transport"
	mcpTransportStreamable    = "streamable-http"
)

type mcpPublishStatus struct {
	Forwarding string  `json:"forwarding"`
	Host       *string `json:"host,omitempty"`
	Name       *string `json:"name,omitempty"`
	Path       string  `json:"path"`
	Published  bool    `json:"published"`
	TokenAuth  bool    `json:"token_auth"`
	TunnelID   *string `json:"tunnel_id,omitempty"`
	URL        string  `json:"url"`
}

var mcpPublishCmd = &cobra.Command{
	Use:          "publish",
	Short:        "Publish the local rstream MCP server through a tunnel",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMCPPublish(cmd)
	},
}

func init() {
	mcpPublishCmd.Flags().SortFlags = false
	mcpPublishCmd.PersistentFlags().SortFlags = false
	mcpPublishCmd.Flags().String("name", "rstream-mcp", "published MCP tunnel name")
	mcpPublishCmd.Flags().Bool("publish", true, "publish the MCP tunnel")
	mcpPublishCmd.Flags().Bool("no-publish", false, "keep the MCP tunnel private")
	mcpPublishCmd.MarkFlagsMutuallyExclusive("publish", "no-publish")
	mcpPublishCmd.Flags().String("host", "", "stable domain for publishing")
	mcpPublishCmd.Flags().StringArray("label", nil, "set MCP tunnel labels (key=value, may be specified multiple times)")
	mcpPublishCmd.Flags().StringP("output", "o", "text", "output mode (text, json)")
	mcpCmd.AddCommand(mcpPublishCmd)
}

func runMCPPublish(cmd *cobra.Command) error {
	ctx := cmd.Context()
	logger := slog.With("cmd", "mcp", "subcmd", "publish")
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return fmt.Errorf("failed to resolve runtime: %w", err)
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return fmt.Errorf("failed to create rstream client: %w", err)
	}
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	props, err := newMCPPublishTunnelProperties(cmd)
	if err != nil {
		return err
	}
	if err := rstream.MaybeSetGeneratedStableDomain(&props, runtime.Resolved.StableDomainEndpoint()); err != nil {
		return fmt.Errorf("failed to generate stable domain: %w", err)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	listener, ok := tunnel.(interface{ net.Listener })
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	status, err := newMCPPublishStatus(tunnel)
	if err != nil {
		return err
	}
	if err := printMCPPublishStatus(cmd, status); err != nil {
		return err
	}
	server := &http.Server{Handler: newMCPHTTPHandler(logger)}
	return serveMCPHTTP(ctx, server, listener, logger)
}

func newMCPPublishTunnelProperties(cmd *cobra.Command) (rstream.TunnelProperties, error) {
	publish, _ := cmd.Flags().GetBool("publish")
	noPublish, _ := cmd.Flags().GetBool("no-publish")
	if noPublish {
		publish = false
	}
	labels := getStringArrayMap(cmd, "label")
	if labels == nil {
		labels = map[string]string{}
	}
	labels[mcpApplicationProtocolKey] = mcpApplicationProtocol
	labels[mcpPathLabel] = mcpHTTPPath
	labels[mcpTransportLabel] = mcpTransportStreamable
	props := rstream.TunnelProperties{Name: getStringPtr(cmd, "name"), Publish: rstream.BoolPtr(publish), Labels: labels, Hostname: getStringPtr(cmd, "host")}
	if publish {
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP1_1)
		props.TokenAuth = rstream.BoolPtr(true)
	}
	return props, nil
}

func newMCPPublishStatus(tunnel rstream.Tunnel) (mcpPublishStatus, error) {
	props, err := tunnel.Properties()
	if err != nil {
		return mcpPublishStatus{}, fmt.Errorf("failed to get tunnel properties: %w", err)
	}
	forwarding, err := tunnel.ForwardingAddress()
	if err != nil {
		return mcpPublishStatus{}, fmt.Errorf("failed to get forwarding address: %w", err)
	}
	return mcpPublishStatus{Forwarding: forwarding, Host: props.Host, Name: props.Name, Path: mcpHTTPPath, Published: props.Publish != nil && *props.Publish, TokenAuth: props.TokenAuth != nil && *props.TokenAuth, TunnelID: props.ID, URL: mcpPublishEndpointURL(forwarding)}, nil
}

func mcpPublishEndpointURL(forwarding string) string {
	cleaned := strings.TrimSuffix(strings.TrimSpace(forwarding), " (unpublished)")
	return strings.TrimRight(cleaned, "/") + mcpHTTPPath
}

func printMCPPublishStatus(cmd *cobra.Command, status mcpPublishStatus) error {
	output, _ := cmd.Flags().GetString("output")
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "json":
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	case "text":
		if status.Published {
			fmt.Fprintf(cmd.OutOrStdout(), "MCP server published at %s\n", status.URL)
			if status.TokenAuth {
				fmt.Fprintln(cmd.OutOrStdout(), "Published MCP requests must include Authorization: Bearer <token> or rstream.token=<token>.")
			}
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "MCP server available at %s\n", status.URL)
		return nil
	default:
		return fmt.Errorf("invalid --output %q (valid: text, json)", output)
	}
}

func newMCPHTTPHandler(logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc(mcpHTTPPath, func(w http.ResponseWriter, r *http.Request) {
		handleMCPHTTP(w, r, logger)
	})
	mux.HandleFunc(mcpHTTPPath+"/", func(w http.ResponseWriter, r *http.Request) {
		handleMCPHTTP(w, r, logger)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return mux
}

func handleMCPHTTP(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	var message mcpMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&message); err != nil {
		logger.Warn("mcp http request parse failed", "error", err)
		writeMCPHTTPJSON(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32700, Message: "parse error"}})
		return
	}
	if message.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeMCPHTTPJSON(w, http.StatusOK, handleMCPMessage(r.Context(), message))
}

func writeMCPHTTPJSON(w http.ResponseWriter, status int, response mcpResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("MCP-Protocol-Version", mcpProtocolVersion)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func serveMCPHTTP(ctx context.Context, server *http.Server, listener net.Listener, logger *slog.Logger) error {
	shutdownOnce := sync.Once{}
	shutdown := func(reason string) {
		shutdownOnce.Do(func() {
			logger.Info("stopping mcp http server", "reason", reason)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("mcp http shutdown failed", "error", err)
			}
		})
	}
	stopShutdownWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown("context canceled")
		case <-stopShutdownWatcher:
		}
	}()
	err := server.Serve(listener)
	close(stopShutdownWatcher)
	shutdown("serve loop ended")
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

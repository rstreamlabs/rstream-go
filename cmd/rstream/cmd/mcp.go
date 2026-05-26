// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolCallParams struct {
	Name      string                     `json:"name"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
}

type mcpEnvelope struct {
	Message mcpMessage
	Framing mcpFraming
}

type mcpFraming string

const mcpProtocolVersion = "2025-06-18"
const mcpFramingContentLength mcpFraming = "content-length"
const mcpFramingLineDelimited mcpFraming = "line-delimited"
const mcpMaxMessageBytes = 8 * 1024 * 1024

var mcpCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "mcp",
	Short:        "Run rstream MCP integrations",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var mcpServeCmd = &cobra.Command{
	Use:          "serve",
	Short:        "Serve rstream tools over MCP stdio",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		restoreEnv := applyMCPServeFlagEnvironment(cmd)
		defer restoreEnv()
		return serveMCP(cmd.Context(), os.Stdin, os.Stdout)
	},
}

func init() {
	mcpCmd.Flags().SortFlags = false
	mcpCmd.PersistentFlags().SortFlags = false
	mcpServeCmd.Flags().SortFlags = false
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}

func applyMCPServeFlagEnvironment(cmd *cobra.Command) func() {
	previous := map[string]*string{}
	for flag, env := range map[string]string{"api-url": "RSTREAM_API_URL", "config": "RSTREAM_CONFIG", "context": "RSTREAM_CONTEXT"} {
		value := mcpStringFlag(cmd, flag)
		if strings.TrimSpace(value) == "" {
			continue
		}
		if old, ok := os.LookupEnv(env); ok {
			previous[env] = &old
		} else {
			previous[env] = nil
		}
		_ = os.Setenv(env, value)
	}
	return func() {
		for env, old := range previous {
			if old == nil {
				_ = os.Unsetenv(env)
			} else {
				_ = os.Setenv(env, *old)
			}
		}
	}
}

func mcpStringFlag(cmd *cobra.Command, name string) string {
	value, err := cmd.Flags().GetString(name)
	if err == nil && strings.TrimSpace(value) != "" {
		return value
	}
	value, err = cmd.InheritedFlags().GetString(name)
	if err == nil {
		return value
	}
	return ""
}

func serveMCP(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	for {
		envelope, err := readMCPEnvelope(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if envelope.Message.ID == nil {
			continue
		}
		response := handleMCPMessage(ctx, envelope.Message)
		if err := writeMCPResponseWithFraming(output, response, envelope.Framing); err != nil {
			return err
		}
	}
}

func readMCPMessage(reader *bufio.Reader) (mcpMessage, error) {
	envelope, err := readMCPEnvelope(reader)
	if err != nil {
		return mcpMessage{}, err
	}
	return envelope.Message, nil
}

func readMCPEnvelope(reader *bufio.Reader) (mcpEnvelope, error) {
	contentLength := -1
	line, err := reader.ReadString('\n')
	if err != nil {
		return mcpEnvelope{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		message, err := decodeMCPMessage([]byte(strings.TrimSpace(line)))
		if err != nil {
			return mcpEnvelope{}, err
		}
		return mcpEnvelope{Message: message, Framing: mcpFramingLineDelimited}, nil
	}
	if parsed, ok, err := parseMCPContentLengthHeader(line); err != nil {
		return mcpEnvelope{}, err
	} else if ok {
		contentLength = parsed
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			return mcpEnvelope{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if parsed, ok, err := parseMCPContentLengthHeader(line); err != nil {
			return mcpEnvelope{}, err
		} else if ok {
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return mcpEnvelope{}, errors.New("missing MCP Content-Length")
	}
	if contentLength > mcpMaxMessageBytes {
		return mcpEnvelope{}, fmt.Errorf("MCP Content-Length exceeds %d bytes", mcpMaxMessageBytes)
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return mcpEnvelope{}, err
	}
	message, err := decodeMCPMessage(payload)
	if err != nil {
		return mcpEnvelope{}, err
	}
	return mcpEnvelope{Message: message, Framing: mcpFramingContentLength}, nil
}

func parseMCPContentLengthHeader(line string) (int, bool, error) {
	key, value, ok := strings.Cut(line, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false, fmt.Errorf("invalid MCP Content-Length: %w", err)
	}
	if parsed < 0 {
		return 0, false, fmt.Errorf("invalid MCP Content-Length: negative value")
	}
	return parsed, true, nil
}

func decodeMCPMessage(payload []byte) (mcpMessage, error) {
	var message mcpMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return mcpMessage{}, fmt.Errorf("invalid MCP JSON-RPC payload: %w", err)
	}
	return message, nil
}

func writeMCPResponse(output io.Writer, response mcpResponse) error {
	return writeMCPResponseWithFraming(output, response, mcpFramingContentLength)
}

func writeMCPResponseWithFraming(output io.Writer, response mcpResponse, framing mcpFraming) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if framing == mcpFramingLineDelimited {
		if _, err := output.Write(append(payload, '\n')); err != nil {
			return err
		}
		return nil
	}
	if _, err := fmt.Fprintf(output, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = output.Write(payload)
	return err
}

func handleMCPMessage(ctx context.Context, message mcpMessage) mcpResponse {
	response := mcpResponse{JSONRPC: "2.0", ID: message.ID}
	switch message.Method {
	case "initialize":
		response.Result = mcpInitializeResult()
	case "tools/list":
		response.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		result, err := handleMCPToolCall(ctx, message.Params)
		if err != nil {
			response.Result = mcpToolTextResult(err.Error(), true)
		} else {
			response.Result = result
		}
	default:
		response.Error = &mcpError{Code: -32601, Message: "method not found"}
	}
	return response
}

func mcpInitializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "rstream", "version": rstream.Version},
	}
}

func mcpTools() []map[string]any {
	return []map[string]any{
		mcpTool("rstream_auth_start", "Start the local rstream OAuth device login flow and return a user approval URL without exposing tokens. The default permissions are intentionally limited; pass permissions only when the user explicitly asks for broader actions such as project creation or settings changes.", map[string]any{"api_url": mcpStringSchema("Optional rstream API URL. Defaults to RSTREAM_API_URL or the public rstream API."), "permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional permissions to request instead of the default limited CLI scope."}}, []string{}),
		mcpTool("rstream_auth_poll", "Poll a local rstream OAuth device login session and store the approved token in the CLI config without returning it.", map[string]any{"id": mcpStringSchema("Auth session ID returned by rstream_auth_start."), "wait": map[string]any{"type": "boolean", "description": "Wait for approval instead of polling once."}, "timeout_seconds": map[string]any{"type": "number", "description": "Optional wait timeout in seconds when wait is true."}}, []string{"id"}),
		mcpTool("rstream_context_list", "List local rstream CLI contexts available to this MCP server.", map[string]any{}, []string{}),
		mcpTool("rstream_context_get", "Get one local rstream CLI context, or the default context when name is omitted.", map[string]any{"name": mcpStringSchema("Optional local rstream context name.")}, []string{}),
		mcpTool("rstream_project_creation_options", "Return project creation options and billing actions for a workspace.", map[string]any{"workspace_id": mcpStringSchema("Workspace ID.")}, []string{"workspace_id"}),
		mcpTool("rstream_project_create", "Create a tunnel project, or start Stripe checkout only when start_checkout is true.", map[string]any{"workspace_id": mcpStringSchema("Workspace ID."), "name": mcpStringSchema("Project name."), "provider": mcpStringSchema("Project provider from creation options."), "region": mcpStringSchema("Project region from creation options."), "plan": mcpStringSchema("Project plan from creation options."), "creation_fingerprint": mcpStringSchema("Creation fingerprint from project creation options."), "idempotency_key": mcpStringSchema("Optional idempotency key. A safe key is generated when omitted."), "start_checkout": map[string]any{"type": "boolean", "description": "Start Stripe checkout instead of direct creation. Set only after explicit user approval."}}, []string{"workspace_id", "name", "provider", "region", "plan", "creation_fingerprint"}),
		mcpTool("rstream_project_list", "List tunnel projects available through the local rstream Control plane context.", map[string]any{"workspace_id": mcpStringSchema("Optional workspace ID. When omitted, lists across accessible workspaces."), "q": mcpStringSchema("Optional project search query."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{}),
		mcpTool("rstream_project_logs", "List tunnel request and connection logs for a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "timeline": mcpStringSchema("Optional timeline: 30m, 1h, 12h, 24h, 3d, 1w, or 30d."), "start": mcpStringSchema("Optional start date."), "end": mcpStringSchema("Optional end date."), "event_type": mcpStringSchema("Optional event type filter."), "after_event_id": mcpStringSchema("Optional event ID cursor."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id"}),
		mcpTool("rstream_project_usage", "Return current-period tunnel and TURN bandwidth usage for a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_plan_get", "Return a tunnel project's current plan, feature list, and quota metadata before using plan-gated features.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_turn_usage", "Return TURN relay usage breakdowns for the last 30 days.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_turn_credentials_create", "Create short-lived TURN credentials for a tunnel project after explicit user approval. The response contains TURN secrets; do not log or paste it unless the user explicitly needs the credential material.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID. Required unless project_endpoint is provided."), "project_endpoint": mcpStringSchema("Tunnel project endpoint. Required unless project_id is provided."), "ttl_seconds": map[string]any{"type": "number", "description": "Optional credential TTL in seconds, between 1 and 3600."}}, []string{}),
		mcpTool("rstream_project_domains_list", "List stable custom domains configured on a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "q": mcpStringSchema("Optional domain search query."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{"project_id"}),
		mcpTool("rstream_project_domain_create", "Attach a stable custom domain to a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "hostname": mcpStringSchema("Custom hostname to attach.")}, []string{"project_id", "hostname"}),
		mcpTool("rstream_project_domain_get", "Return verification and DNS details for a project domain.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "domain_id": mcpStringSchema("Project domain ID.")}, []string{"project_id", "domain_id"}),
		mcpTool("rstream_project_domain_delete", "Remove a custom domain from a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "domain_id": mcpStringSchema("Project domain ID.")}, []string{"project_id", "domain_id"}),
		mcpTool("rstream_project_domain_verify", "Re-check ownership and DNS configuration for a project domain.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "domain_id": mcpStringSchema("Project domain ID.")}, []string{"project_id", "domain_id"}),
		mcpTool("rstream_project_domain_connect", "Return a Domain Connect apply URL for a project domain when the DNS provider supports it.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "domain_id": mcpStringSchema("Project domain ID.")}, []string{"project_id", "domain_id"}),
		mcpTool("rstream_project_settings_get", "Return access and transport settings for a tunnel project.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_project_settings_patch", "Patch access and transport settings for a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID."), "settings": map[string]any{"type": "object", "description": "Partial project settings object."}, "settings_json": mcpStringSchema("Partial project settings JSON object.")}, []string{"project_id"}),
		mcpTool("rstream_project_settings_reset", "Reset access and transport settings for a tunnel project after explicit user approval.", map[string]any{"project_id": mcpStringSchema("Tunnel project ID.")}, []string{"project_id"}),
		mcpTool("rstream_preview_expose", "Expose a local HTTP service through a persistent rstream preview process.", map[string]any{"port": mcpStringSchema("Local port to expose, such as 3000."), "host": mcpStringSchema("Optional local host, defaults to localhost."), "name": mcpStringSchema("Optional tunnel name."), "stable_domain": mcpStringSchema("Optional stable published host."), "token_auth": map[string]any{"type": "boolean", "description": "Require rstream token authentication at the edge."}}, []string{"port"}),
		mcpTool("rstream_preview_list", "List preview tunnels started through the local rstream MCP preview registry.", map[string]any{}, []string{}),
		mcpTool("rstream_preview_stop", "Stop a preview tunnel from the local rstream MCP preview registry.", map[string]any{"id": mcpStringSchema("Preview ID or tunnel ID returned by rstream_preview_expose.")}, []string{"id"}),
		mcpTool("rstream_remote_expose", "Start rstream forward on a POSIX WebTTY remote host to expose a remote-local network service or MCP surface.", map[string]any{"webtty_url": mcpStringSchema("WebTTY URL, for example rstrm://robot-shell."), "exec_path": mcpStringSchema("Advertised exec_path from rstream_webtty_list. Defaults to /."), "port": mcpStringSchema("Remote-local port to expose from the WebTTY host."), "host": mcpStringSchema("Optional remote-local host, defaults to 127.0.0.1."), "id": mcpStringSchema("Optional remote expose ID used for later stop."), "name": mcpStringSchema("Optional rstream tunnel name."), "protocol": mcpStringSchema("Optional protocol: http, h2c, h3, tls, tcp, udp, dtls, or quic."), "publish": map[string]any{"type": "boolean", "description": "Publish the exposed resource. Defaults to true."}, "stable_domain": mcpStringSchema("Optional stable published host."), "token_auth": map[string]any{"type": "boolean", "description": "Require rstream token authentication at the edge."}, "rstream_auth": map[string]any{"type": "boolean", "description": "Require rstream account authentication at the edge."}, "mcp_path": mcpStringSchema("Optional remote MCP HTTP path, usually /mcp; adds MCP discovery labels."), "labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Additional rstream labels as key=value entries."}, "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Environment variables passed to the WebTTY command as KEY=value entries."}, "workdir": mcpStringSchema("Optional remote working directory."), "user": mcpStringSchema("Optional remote username or UID."), "timeout_seconds": map[string]any{"type": "number", "description": "Seconds to wait for the remote tunnel to report online."}, "rstream_command": mcpStringSchema("Optional rstream executable path on the remote host.")}, []string{"webtty_url", "port"}),
		mcpTool("rstream_remote_expose_stop", "Stop a remote expose process previously started through rstream_remote_expose.", map[string]any{"webtty_url": mcpStringSchema("WebTTY URL used to reach the remote host."), "exec_path": mcpStringSchema("Advertised exec_path from rstream_webtty_list. Defaults to /."), "id": mcpStringSchema("Remote expose ID returned by rstream_remote_expose."), "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Environment variables passed to the WebTTY command as KEY=value entries."}, "workdir": mcpStringSchema("Optional remote working directory."), "user": mcpStringSchema("Optional remote username or UID.")}, []string{"webtty_url", "id"}),
		mcpTool("rstream_remote_mcp_discover", "Discover online MCP surfaces exposed through rstream labels.", map[string]any{"filter": mcpStringSchema("Optional additional rstream tunnel filter.")}, []string{}),
		mcpTool("rstream_remote_mcp_tools", "List tools from a remote MCP server reached through rstream or a published URL.", map[string]any{"url": mcpStringSchema("Remote MCP URL, such as rstrm://robot-mcp or https://robot.example.com/mcp."), "path": mcpStringSchema("Optional MCP path when url does not include one."), "token": mcpStringSchema("Optional bearer token for token-auth protected published MCP surfaces.")}, []string{"url"}),
		mcpTool("rstream_remote_mcp_call", "Call a tool on a remote MCP server reached through rstream or a published URL.", map[string]any{"url": mcpStringSchema("Remote MCP URL, such as rstrm://robot-mcp or https://robot.example.com/mcp."), "tool": mcpStringSchema("Remote MCP tool name."), "path": mcpStringSchema("Optional MCP path when url does not include one."), "token": mcpStringSchema("Optional bearer token for token-auth protected published MCP surfaces."), "arguments": map[string]any{"type": "object", "description": "Remote MCP tool arguments."}, "arguments_json": mcpStringSchema("Remote MCP tool arguments as a JSON object string.")}, []string{"url", "tool"}),
		mcpTool("rstream_runtime_status", "Return local rstream CLI runtime status without exposing secrets.", map[string]any{}, []string{}),
		mcpTool("rstream_token_create", "Mint a short-lived rstream auth token from the local Control plane context. The response contains a bearer token; do not log or paste it unless the user explicitly needs the token value. When scoping Engine access, pass the full token resource object under resources_json. Example for a read-only project token: {\"tunnels\":{\"projects\":[\"PROJECT_ID\"],\"scopes\":{\"tunnels\":{\"list\":true}}}}. Scope actions must match permissions: tunnels.resources.read-only requires list, tunnels.tunnels.create-delete requires create, and tunnels.streams.create-delete requires connect.", map[string]any{"permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Permissions to include in the minted token."}, "resources_json": mcpStringSchema("Optional full resource boundary JSON object, for example {\"tunnels\":{\"projects\":[\"PROJECT_ID\"],\"scopes\":{\"tunnels\":{\"list\":true}}}} for tunnels.resources.read-only.")}, []string{"permissions"}),
		mcpTool("rstream_workspace_list", "List workspaces available through the local rstream Control plane context.", map[string]any{}, []string{}),
		mcpTool("rstream_workspace_members_list", "List users and invited users with access to a workspace and their roles.", map[string]any{"workspace_id": mcpStringSchema("Workspace ID."), "q": mcpStringSchema("Optional member search query."), "page": map[string]any{"type": "number", "description": "Optional result page."}, "page_size": map[string]any{"type": "number", "description": "Optional page size."}, "sort": mcpStringSchema("Optional sort key."), "order": mcpStringSchema("Optional sort direction.")}, []string{"workspace_id"}),
		mcpTool("rstream_webtty_list", "List online WebTTY servers exposed through rstream.", map[string]any{"filter": mcpStringSchema("Optional rstream tunnel filter, such as labels.rstream.webtty.label.role=codex.")}, []string{}),
		mcpTool("rstream_webtty_exec", "Execute a non-interactive command through a WebTTY server.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "exec_path": mcpStringSchema("Advertised exec_path from rstream_webtty_list. Defaults to /."), "command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}, "workdir": mcpStringSchema("Optional working directory."), "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "user": mcpStringSchema("Optional username or UID.")}, []string{"url", "command"}),
		mcpTool("rstream_webtty_fs_list", "List a directory exposed by a WebTTY filesystem sidecar. Paths are relative to the server --fs-root; use / for that root, not the host filesystem root.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("Path inside the advertised filesystem root, for example / or /compose.yaml's parent directory.")}, []string{"url"}),
		mcpTool("rstream_webtty_fs_read", "Read a file exposed by a WebTTY filesystem sidecar. Paths are relative to the server --fs-root; use /compose.yaml when --fs-root is the directory containing compose.yaml.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File path inside the advertised filesystem root, for example /README.md."), "encoding": mcpStringSchema("Optional output encoding: text or base64.")}, []string{"url", "path"}),
		mcpTool("rstream_webtty_fs_write", "Write a file through a WebTTY filesystem sidecar. Paths are relative to the server --fs-root; do not pass absolute host paths unless --fs-root is the host root.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File path inside the advertised filesystem root, for example /compose.yaml."), "content": mcpStringSchema("File content to write."), "encoding": mcpStringSchema("Optional input encoding: text or base64.")}, []string{"url", "path", "content"}),
		mcpTool("rstream_webtty_fs_mkdir", "Create a directory through a WebTTY filesystem sidecar. Paths are relative to the server --fs-root.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("Directory path inside the advertised filesystem root.")}, []string{"url", "path"}),
		mcpTool("rstream_webtty_fs_delete", "Delete a file or directory through a WebTTY filesystem sidecar. Paths are relative to the server --fs-root.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "fs_path": mcpStringSchema("Advertised fs_path from rstream_webtty_list. Defaults to /fs."), "path": mcpStringSchema("File or directory path inside the advertised filesystem root.")}, []string{"url", "path"}),
	}
}

func mcpTool(name string, description string, properties map[string]any, required []string) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
}

func mcpStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func handleMCPToolCall(ctx context.Context, params json.RawMessage) (map[string]any, error) {
	var call mcpToolCallParams
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}
	switch call.Name {
	case "rstream_auth_start":
		return mcpAuthStart(ctx, call.Arguments)
	case "rstream_auth_poll":
		return mcpAuthPoll(ctx, call.Arguments)
	case "rstream_context_list":
		return mcpContextList(call.Arguments)
	case "rstream_context_get":
		return mcpContextGet(call.Arguments)
	case "rstream_project_creation_options":
		return mcpProjectCreationOptions(ctx, call.Arguments)
	case "rstream_project_create":
		return mcpProjectCreate(ctx, call.Arguments)
	case "rstream_project_list":
		return mcpProjectList(ctx, call.Arguments)
	case "rstream_project_logs":
		return mcpProjectLogs(ctx, call.Arguments)
	case "rstream_project_usage":
		return mcpProjectUsage(ctx, call.Arguments)
	case "rstream_project_plan_get":
		return mcpProjectPlanGet(ctx, call.Arguments)
	case "rstream_project_turn_usage":
		return mcpProjectTURNUsage(ctx, call.Arguments)
	case "rstream_project_turn_credentials_create":
		return mcpProjectTURNCredentialsCreate(ctx, call.Arguments)
	case "rstream_project_domains_list":
		return mcpProjectDomainsList(ctx, call.Arguments)
	case "rstream_project_domain_create":
		return mcpProjectDomainCreate(ctx, call.Arguments)
	case "rstream_project_domain_get":
		return mcpProjectDomainGet(ctx, call.Arguments)
	case "rstream_project_domain_delete":
		return mcpProjectDomainDelete(ctx, call.Arguments)
	case "rstream_project_domain_verify":
		return mcpProjectDomainVerify(ctx, call.Arguments)
	case "rstream_project_domain_connect":
		return mcpProjectDomainConnect(ctx, call.Arguments)
	case "rstream_project_settings_get":
		return mcpProjectSettingsGet(ctx, call.Arguments)
	case "rstream_project_settings_patch":
		return mcpProjectSettingsPatch(ctx, call.Arguments)
	case "rstream_project_settings_reset":
		return mcpProjectSettingsReset(ctx, call.Arguments)
	case "rstream_preview_expose":
		return mcpPreviewExpose(ctx, call.Arguments)
	case "rstream_preview_list":
		return mcpPreviewList()
	case "rstream_preview_stop":
		return mcpPreviewStop(call.Arguments)
	case "rstream_remote_expose":
		return mcpRemoteExpose(ctx, call.Arguments)
	case "rstream_remote_expose_stop":
		return mcpRemoteExposeStop(ctx, call.Arguments)
	case "rstream_remote_mcp_discover":
		return mcpRemoteMCPDiscover(ctx, call.Arguments)
	case "rstream_remote_mcp_tools":
		return mcpRemoteMCPTools(ctx, call.Arguments)
	case "rstream_remote_mcp_call":
		return mcpRemoteMCPCall(ctx, call.Arguments)
	case "rstream_runtime_status":
		return mcpRuntimeStatus()
	case "rstream_token_create":
		return mcpTokenCreate(ctx, call.Arguments)
	case "rstream_workspace_list":
		return mcpWorkspaceList(ctx)
	case "rstream_workspace_members_list":
		return mcpWorkspaceMembersList(ctx, call.Arguments)
	case "rstream_webtty_list":
		return mcpWebTTYList(ctx, call.Arguments)
	case "rstream_webtty_exec":
		return mcpWebTTYExec(ctx, call.Arguments)
	case "rstream_webtty_fs_list":
		return mcpWebTTYFSList(ctx, call.Arguments)
	case "rstream_webtty_fs_read":
		return mcpWebTTYFSRead(ctx, call.Arguments)
	case "rstream_webtty_fs_write":
		return mcpWebTTYFSWrite(ctx, call.Arguments)
	case "rstream_webtty_fs_mkdir":
		return mcpWebTTYFSMkdir(ctx, call.Arguments)
	case "rstream_webtty_fs_delete":
		return mcpWebTTYFSDelete(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func mcpWebTTYList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, err := resolveMCPRuntime(true, true)
	if err != nil {
		return nil, err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return nil, err
	}
	filter, err := mcpOptionalStringArg(args, "filter", "")
	if err != nil {
		return nil, err
	}
	servers, err := listWebTTYServers(ctx, client, filter)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(servers, false)
}

func mcpWebTTYExec(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	command, err := mcpRequiredStringSliceArg(args, "command")
	if err != nil {
		return nil, err
	}
	urlValue, err := mcpRequiredStringArg(args, "url")
	if err != nil {
		return nil, err
	}
	execPath, err := mcpOptionalStringArg(args, "exec_path", "")
	if err != nil {
		return nil, err
	}
	urlValue, err = resolveWebTTYExecURL(urlValue, execPath)
	if err != nil {
		return nil, err
	}
	envVars, err := mcpOptionalStringSliceArg(args, "env")
	if err != nil {
		return nil, err
	}
	workdir, err := mcpOptionalStringPtrArg(args, "workdir")
	if err != nil {
		return nil, err
	}
	username, err := mcpOptionalStringPtrArg(args, "user")
	if err != nil {
		return nil, err
	}
	cfg := &webtty.ClientConfig{URL: urlValue, Interactive: false, AllocateTTY: false, SendHeartbeat: true, EnvVars: envVars, Workdir: workdir, Username: username, CmdArgs: command}
	if webttyClientUsesRstream(urlValue) {
		runtime, err := resolveMCPRuntime(true, true)
		if err != nil {
			return nil, err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return nil, err
		}
		cfg.DialContext = newWebTTYClientDialContext(client)
	}
	result, err := runWebTTYClientCapture(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(result, result.ExitCode != 0)
}

func mcpWebTTYFSList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(args)
	if err != nil {
		return nil, err
	}
	remotePath, err := mcpOptionalStringArg(args, "path", "/")
	if err != nil {
		return nil, err
	}
	items, err := client.list(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(items, false)
}

func mcpWebTTYFSRead(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(args)
	if err != nil {
		return nil, err
	}
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	buffer := bytes.Buffer{}
	if err := client.read(ctx, remotePath, &buffer); err != nil {
		return nil, err
	}
	encoding, err := mcpOptionalStringArg(args, "encoding", "text")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "text":
		return mcpToolTextResult(buffer.String(), false), nil
	case "base64":
		return mcpJSONResult(map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(buffer.Bytes())}, false)
	default:
		return nil, fmt.Errorf("invalid encoding %q (valid: text, base64)", encoding)
	}
}

func mcpWebTTYFSWrite(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(args)
	if err != nil {
		return nil, err
	}
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := mcpStringArg(args, "content")
	if err != nil {
		return nil, err
	}
	reader, err := mcpContentReader(args, content)
	if err != nil {
		return nil, err
	}
	if err := client.write(ctx, remotePath, reader); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}

func mcpWebTTYFSMkdir(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(args)
	if err != nil {
		return nil, err
	}
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	if err := client.mkcol(ctx, remotePath); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}

func mcpWebTTYFSDelete(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, err := newWebTTYFSMCPClient(args)
	if err != nil {
		return nil, err
	}
	remotePath, err := mcpRequiredStringArg(args, "path")
	if err != nil {
		return nil, err
	}
	if err := client.delete(ctx, remotePath); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}

func mcpContentReader(args map[string]json.RawMessage, content string) (io.Reader, error) {
	encoding, err := mcpOptionalStringArg(args, "encoding", "text")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "text":
		return strings.NewReader(content), nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 content: %w", err)
		}
		return bytes.NewReader(decoded), nil
	default:
		return nil, fmt.Errorf("invalid encoding %q (valid: text, base64)", encoding)
	}
}

func newWebTTYFSMCPClient(args map[string]json.RawMessage) (*webTTYFSClient, error) {
	rawURL, err := mcpRequiredStringArg(args, "url")
	if err != nil {
		return nil, err
	}
	fsPath, err := mcpOptionalStringArg(args, "fs_path", "")
	if err != nil {
		return nil, err
	}
	baseURL, target, err := resolveWebTTYFSBaseURL(rawURL, fsPath)
	if err != nil {
		return nil, err
	}
	httpClient := http.DefaultClient
	if target != "" {
		runtime, err := resolveMCPRuntime(true, true)
		if err != nil {
			return nil, err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return nil, err
		}
		httpClient = &http.Client{Transport: &http.Transport{DialContext: newWebTTYFSDialContext(client, target)}}
	}
	return &webTTYFSClient{client: httpClient, baseURL: baseURL}, nil
}

func resolveMCPRuntime(requireEngine bool, requireToken bool) (*resolvedRuntime, error) {
	env := config.ReadEnv()
	path := env.ConfigPath
	if path == "" {
		var err error
		path, err = config.DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	envAPIURL := env.APIURL
	if envAPIURL == "" && env.Context == "" && cfg.Defaults.Context == nil && len(cfg.Environments) == 1 {
		envAPIURL = cfg.Environments[0].APIURL
	}
	input := config.ResolveInput{Config: cfg, EnvAPIURL: envAPIURL, EnvContext: env.Context, EnvEngine: env.Engine, EnvToken: env.Token, EnvMTLSCert: env.MTLSCert, EnvMTLSKey: env.MTLSKey, RequireEngine: requireEngine, RequireToken: requireToken, ResolveToken: true}
	resolved, err := config.Resolve(input)
	if err != nil {
		return nil, err
	}
	resolved = applyEnvTransportOverrides(resolved, env)
	return &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: resolved}, nil
}

func mcpRequiredStringArg(args map[string]json.RawMessage, name string) (string, error) {
	value, err := mcpOptionalStringArg(args, name, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing required argument %q", name)
	}
	return value, nil
}

func mcpStringArg(args map[string]json.RawMessage, name string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("argument %q must be a string", name)
	}
	return value, nil
}

func mcpOptionalStringArg(args map[string]json.RawMessage, name string, fallback string) (string, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("argument %q must be a string", name)
	}
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return value, nil
}

func mcpOptionalStringPtrArg(args map[string]json.RawMessage, name string) (*string, error) {
	value, err := mcpOptionalStringArg(args, name, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return &value, nil
}

func mcpOptionalBoolArg(args map[string]json.RawMessage, name string, fallback bool) (bool, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("argument %q must be a boolean", name)
	}
	return value, nil
}

func mcpOptionalIntArg(args map[string]json.RawMessage, name string) (*int, error) {
	raw, ok := args[name]
	if !ok {
		return nil, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("argument %q must be a number", name)
	}
	return &value, nil
}

func mcpRequiredStringSliceArg(args map[string]json.RawMessage, name string) ([]string, error) {
	values, err := mcpOptionalStringSliceArg(args, name)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("missing required argument %q", name)
	}
	return values, nil
}

func mcpOptionalStringSliceArg(args map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := args[name]
	if !ok {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("argument %q must be an array of strings", name)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}

func mcpJSONResult(value any, isError bool) (map[string]any, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcpToolTextResult(string(payload), isError), nil
}

func mcpToolTextResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isError}
}

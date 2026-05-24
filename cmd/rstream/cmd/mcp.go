// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"context"
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

const mcpProtocolVersion = "2025-06-18"

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

func serveMCP(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	for {
		message, err := readMCPMessage(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if message.ID == nil {
			continue
		}
		response := handleMCPMessage(ctx, message)
		if err := writeMCPResponse(output, response); err != nil {
			return err
		}
	}
}

func readMCPMessage(reader *bufio.Reader) (mcpMessage, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return mcpMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return mcpMessage{}, fmt.Errorf("invalid MCP Content-Length: %w", err)
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return mcpMessage{}, errors.New("missing MCP Content-Length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return mcpMessage{}, err
	}
	var message mcpMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return mcpMessage{}, fmt.Errorf("invalid MCP JSON-RPC payload: %w", err)
	}
	return message, nil
}

func writeMCPResponse(output io.Writer, response mcpResponse) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
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
		mcpTool("rstream_webtty_list", "List online WebTTY servers exposed through rstream.", map[string]any{"filter": mcpStringSchema("Optional rstream tunnel filter, such as labels.role=codex.")}, []string{}),
		mcpTool("rstream_webtty_exec", "Execute a non-interactive command through a WebTTY server.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}, "workdir": mcpStringSchema("Optional working directory."), "env": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "user": mcpStringSchema("Optional username or UID.")}, []string{"url", "command"}),
		mcpTool("rstream_webtty_fs_list", "List a directory exposed by a WebTTY filesystem sidecar.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "path": mcpStringSchema("Remote directory path.")}, []string{"url"}),
		mcpTool("rstream_webtty_fs_read", "Read a file exposed by a WebTTY filesystem sidecar.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "path": mcpStringSchema("Remote file path.")}, []string{"url", "path"}),
		mcpTool("rstream_webtty_fs_write", "Write a UTF-8 file through a WebTTY filesystem sidecar.", map[string]any{"url": mcpStringSchema("WebTTY URL, for example rstrm://shell."), "path": mcpStringSchema("Remote file path."), "content": mcpStringSchema("File content to write.")}, []string{"url", "path", "content"}),
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
	return mcpToolTextResult(buffer.String(), false), nil
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
	content, err := mcpRequiredStringArg(args, "content")
	if err != nil {
		return nil, err
	}
	if err := client.write(ctx, remotePath, strings.NewReader(content)); err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]bool{"ok": true}, false)
}

func newWebTTYFSMCPClient(args map[string]json.RawMessage) (*webTTYFSClient, error) {
	rawURL, err := mcpRequiredStringArg(args, "url")
	if err != nil {
		return nil, err
	}
	baseURL, target, err := resolveWebTTYFSBaseURL(rawURL)
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
	path, err := config.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	env := config.ReadEnv()
	input := config.ResolveInput{Config: cfg, EnvAPIURL: env.APIURL, EnvContext: env.Context, EnvEngine: env.Engine, EnvToken: env.Token, EnvMTLSCert: env.MTLSCert, EnvMTLSKey: env.MTLSKey, RequireEngine: requireEngine, RequireToken: requireToken, ResolveToken: true}
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

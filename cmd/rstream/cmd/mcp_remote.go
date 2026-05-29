// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

const (
	remoteExposeIDPrefix       = "remote-expose-"
	remoteExposeKindLabel      = "rstream.remote.kind"
	remoteExposePathLabel      = "rstream.remote.path"
	remoteExposePortLabel      = "rstream.remote.port"
	remoteExposeSourceLabel    = "rstream.remote.source"
	remoteExposeSourceWebTTY   = "webtty"
	remoteExposeStatusJSONEnd  = "RSTREAM_REMOTE_EXPOSE_JSON_END"
	remoteExposeStatusJSONLine = "RSTREAM_REMOTE_EXPOSE_JSON_LINE="
	remoteExposeStatusLine     = "RSTREAM_REMOTE_EXPOSE_STATUS="
	mcpRemoteRoleLabel         = "rstream.mcp.role"
	mcpRemoteRoleValue         = "remote-surface"
)

type remoteExposeArgs struct {
	Env          []string
	ExecPath     string
	ID           string
	Host         string
	Labels       map[string]string
	MCPPath      string
	Name         string
	Port         string
	Protocol     string
	Publish      bool
	RstreamAuth  bool
	RstreamCmd   string
	StableDomain string
	Timeout      int
	TokenAuth    bool
	URL          string
	User         *string
	Workdir      *string
}

type remoteExposeResult struct {
	ID      string             `json:"id"`
	LogPath string             `json:"log_path,omitempty"`
	PID     int                `json:"pid,omitempty"`
	Status  string             `json:"status"`
	Forward *forwardStatus     `json:"forward,omitempty"`
	Command []string           `json:"command"`
	WebTTY  webTTYClientResult `json:"webtty"`
}

type remoteMCPEndpoint struct {
	Client *http.Client
	URL    string
}

func mcpRemoteExpose(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	expose, err := mcpRemoteExposeArgs(args)
	if err != nil {
		return nil, err
	}
	command := []string{"/bin/sh", "-lc", remoteExposeShellScript(expose)}
	result, err := runMCPWebTTYCommand(ctx, args, expose.URL, expose.ExecPath, command, expose.Env, expose.Workdir, expose.User)
	if err != nil {
		return nil, err
	}
	parsed, err := parseRemoteExposeResult(expose, command, result)
	if err != nil {
		return nil, err
	}
	return mcpJSONResourceLinkResult(parsed, parsed.Status != "online", remoteExposeForwardingURL(parsed), parsed.ID, "Public rstream remote exposure URL", "text/html")
}

func mcpRemoteExposeStop(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	rawID, err := mcpRequiredStringArg(args, "id")
	if err != nil {
		return nil, err
	}
	rawURL, err := mcpRequiredStringArg(args, "webtty_url")
	if err != nil {
		return nil, err
	}
	execPath, err := mcpOptionalStringArg(args, "exec_path", "")
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
	id := safeRemoteExposeID(rawID)
	script := "set -eu; id=" + shellSingleQuote(id) + "; dir=${RSTREAM_REMOTE_EXPOSE_DIR:-$HOME/.rstream/remote-exposes}; pidfile=\"$dir/$id.pid\"; if [ ! -f \"$pidfile\" ]; then echo stopped=false; echo reason=not_found; exit 0; fi; pid=$(cat \"$pidfile\"); stopped=false; if kill -0 \"$pid\" 2>/dev/null; then kill \"$pid\"; sleep 1; if kill -0 \"$pid\" 2>/dev/null; then kill -KILL \"$pid\" 2>/dev/null || true; fi; stopped=true; fi; rm -f \"$pidfile\"; echo stopped=$stopped; echo pid=$pid"
	result, err := runMCPWebTTYCommand(ctx, args, rawURL, execPath, []string{"/bin/sh", "-lc", script}, envVars, workdir, username)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"id": id, "stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode}, result.ExitCode != 0)
}

func mcpRemoteMCPDiscover(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, err := resolveMCPRuntimeForArgs(ctx, args)
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
	params, err := remoteMCPListParams(filter)
	if err != nil {
		return nil, err
	}
	tunnels, err := client.ListTunnels(ctx, params)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(remoteMCPEndpointsFromTunnels(*tunnels), false)
}

func mcpRemoteMCPTools(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	endpoint, token, err := remoteMCPEndpointFromArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	result, err := remoteMCPJSONRPC(ctx, endpoint, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(result, false)
}

func mcpRemoteMCPCall(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	endpoint, token, err := remoteMCPEndpointFromArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	tool, err := mcpRequiredStringArg(args, "tool")
	if err != nil {
		return nil, err
	}
	arguments, err := remoteMCPArguments(args)
	if err != nil {
		return nil, err
	}
	result, err := remoteMCPJSONRPC(ctx, endpoint, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": arguments}})
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(result, false)
}

func mcpRemoteExposeArgs(args map[string]json.RawMessage) (remoteExposeArgs, error) {
	rawURL, err := mcpRequiredStringArg(args, "webtty_url")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	execPath, err := mcpOptionalStringArg(args, "exec_path", "")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	port, err := mcpRequiredStringArg(args, "port")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return remoteExposeArgs{}, fmt.Errorf("port must be numeric")
	}
	idArg, err := mcpOptionalStringArg(args, "id", "")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	host, err := mcpOptionalStringArg(args, "host", "127.0.0.1")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	name, err := mcpOptionalStringArg(args, "name", "")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	protocol, err := mcpOptionalStringArg(args, "protocol", "http")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	publish, err := mcpOptionalBoolArg(args, "publish", true)
	if err != nil {
		return remoteExposeArgs{}, err
	}
	stableDomain, err := mcpOptionalStringArg(args, "stable_domain", "")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	mcpPath, err := mcpOptionalStringArg(args, "mcp_path", "")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	tokenAuth, err := mcpOptionalBoolArg(args, "token_auth", mcpPath != "")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	rstreamAuth, err := mcpOptionalBoolArg(args, "rstream_auth", false)
	if err != nil {
		return remoteExposeArgs{}, err
	}
	if !publish && stableDomain != "" {
		return remoteExposeArgs{}, fmt.Errorf("stable_domain requires publish=true")
	}
	if !publish {
		tokenAuth = false
		rstreamAuth = false
	}
	timeout, err := mcpOptionalIntArg(args, "timeout_seconds")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	rstreamCmd, err := mcpOptionalStringArg(args, "rstream_command", "rstream")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	envVars, err := mcpOptionalStringSliceArg(args, "env")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	workdir, err := mcpOptionalStringPtrArg(args, "workdir")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	username, err := mcpOptionalStringPtrArg(args, "user")
	if err != nil {
		return remoteExposeArgs{}, err
	}
	labels, err := mcpOptionalLabelMap(args)
	if err != nil {
		return remoteExposeArgs{}, err
	}
	id := remoteExposeID()
	if idArg != "" {
		id = safeRemoteExposeID(idArg)
	}
	if name == "" {
		name = id
	}
	return remoteExposeArgs{Env: envVars, ExecPath: execPath, ID: id, Host: host, Labels: remoteExposeLabels(labels, port, mcpPath), MCPPath: mcpPath, Name: name, Port: port, Protocol: protocol, Publish: publish, RstreamAuth: rstreamAuth, RstreamCmd: rstreamCmd, StableDomain: stableDomain, Timeout: boundedRemoteExposeTimeout(timeout), TokenAuth: tokenAuth, URL: rawURL, User: username, Workdir: workdir}, nil
}

func remoteExposeLabels(labels map[string]string, port string, mcpPath string) map[string]string {
	out := make(map[string]string, len(labels)+7)
	for key, value := range labels {
		out[key] = value
	}
	out[remoteExposeKindLabel] = "network"
	out[remoteExposePortLabel] = port
	out[remoteExposeSourceLabel] = remoteExposeSourceWebTTY
	if mcpPath != "" {
		out[mcpApplicationProtocolKey] = mcpApplicationProtocol
		out[mcpPathLabel] = mcpPath
		out[mcpRemoteRoleLabel] = mcpRemoteRoleValue
		out[mcpTransportLabel] = mcpTransportStreamable
		out[remoteExposeKindLabel] = "mcp"
		out[remoteExposePathLabel] = mcpPath
	}
	return out
}

func remoteExposeForwardArgs(expose remoteExposeArgs) ([]string, error) {
	args := []string{expose.RstreamCmd, "forward", net.JoinHostPort(expose.Host, expose.Port), "--output", "json", "--name", expose.Name}
	if expose.Publish {
		args = append(args, "--publish")
	} else {
		args = append(args, "--no-publish")
	}
	protocol, err := remoteExposeForwardProtocolArgs(expose.Protocol, expose.Publish)
	if err != nil {
		return nil, err
	}
	args = append(args, protocol...)
	if expose.StableDomain != "" && !expose.Publish {
		return nil, fmt.Errorf("stable_domain requires publish=true")
	}
	if expose.StableDomain != "" {
		args = append(args, "--host", expose.StableDomain)
	}
	if expose.TokenAuth && expose.Publish {
		args = append(args, "--token-auth")
	}
	if expose.RstreamAuth && expose.Publish {
		args = append(args, "--rstream-auth")
	}
	for _, key := range sortedStringKeys(expose.Labels) {
		args = append(args, "--label", key+"="+expose.Labels[key])
	}
	return args, nil
}

func remoteExposeProtocolArgs(protocol string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "http", "http/1.1":
		return []string{"--http", "--http-version", string(rstream.HTTP1_1)}, nil
	case "h2c", "http/2":
		return []string{"--http", "--http-version", string(rstream.HTTP2)}, nil
	case "h3", "http/3":
		return []string{"--http", "--http-version", string(rstream.HTTP3)}, nil
	case "tls":
		return []string{"--tls"}, nil
	case "bytestream", "tcp":
		return []string{"--bytestream"}, nil
	case "datagram", "udp":
		return []string{"--datagram"}, nil
	case "dtls":
		return []string{"--dtls"}, nil
	case "quic":
		return []string{"--quic"}, nil
	default:
		return nil, fmt.Errorf("invalid protocol %q", protocol)
	}
}

func remoteExposeForwardProtocolArgs(protocol string, publish bool) ([]string, error) {
	if publish {
		return remoteExposeProtocolArgs(protocol)
	}
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "http", "http/1.1", "h2c", "http/2", "tls", "bytestream", "tcp":
		return []string{"--bytestream"}, nil
	case "h3", "http/3", "datagram", "udp", "dtls", "quic":
		return []string{"--datagram"}, nil
	default:
		return nil, fmt.Errorf("invalid protocol %q", protocol)
	}
}

func remoteExposeShellScript(expose remoteExposeArgs) string {
	args, err := remoteExposeForwardArgs(expose)
	if err != nil {
		return "echo " + shellSingleQuote(err.Error()) + "; exit 1"
	}
	cmd := shellJoin(args)
	timeout := strconv.Itoa(expose.Timeout)
	return "set -eu; id=" + shellSingleQuote(safeRemoteExposeID(expose.ID)) + "; dir=${RSTREAM_REMOTE_EXPOSE_DIR:-$HOME/.rstream/remote-exposes}; mkdir -p \"$dir\"; chmod 700 \"$dir\"; log=\"$dir/$id.log\"; pidfile=\"$dir/$id.pid\"; : > \"$log\"; nohup " + cmd + " > \"$log\" 2>&1 & pid=$!; echo \"$pid\" > \"$pidfile\"; deadline=$(($(date +%s)+" + timeout + ")); while kill -0 \"$pid\" 2>/dev/null; do line=$(grep -m 1 '\"status\":\"online\"' \"$log\" || true); if [ -n \"$line\" ]; then printf '%s\\n' " + shellSingleQuote(remoteExposeStatusLine+"online") + "; printf 'RSTREAM_REMOTE_EXPOSE_ID=%s\\n' \"$id\"; printf 'RSTREAM_REMOTE_EXPOSE_PID=%s\\n' \"$pid\"; printf 'RSTREAM_REMOTE_EXPOSE_LOG=%s\\n' \"$log\"; printf '%s%s\\n' " + shellSingleQuote(remoteExposeStatusJSONLine) + " \"$line\"; printf '%s\\n' " + shellSingleQuote(remoteExposeStatusJSONEnd) + "; exit 0; fi; if [ \"$(date +%s)\" -ge \"$deadline\" ]; then printf '%s\\n' " + shellSingleQuote(remoteExposeStatusLine+"starting") + "; printf 'RSTREAM_REMOTE_EXPOSE_ID=%s\\n' \"$id\"; printf 'RSTREAM_REMOTE_EXPOSE_PID=%s\\n' \"$pid\"; printf 'RSTREAM_REMOTE_EXPOSE_LOG=%s\\n' \"$log\"; tail -n 20 \"$log\" || true; exit 0; fi; sleep 1; done; printf '%s\\n' " + shellSingleQuote(remoteExposeStatusLine+"failed") + "; printf 'RSTREAM_REMOTE_EXPOSE_ID=%s\\n' \"$id\"; printf 'RSTREAM_REMOTE_EXPOSE_PID=%s\\n' \"$pid\"; printf 'RSTREAM_REMOTE_EXPOSE_LOG=%s\\n' \"$log\"; tail -n 40 \"$log\" || true; exit 1"
}

func runMCPWebTTYCommand(ctx context.Context, runtimeArgs map[string]json.RawMessage, rawURL string, execPath string, command []string, envVars []string, workdir *string, username *string) (*webTTYClientResult, error) {
	urlValue, err := resolveWebTTYExecURL(rawURL, execPath)
	if err != nil {
		return nil, err
	}
	cfg := &webtty.ClientConfig{URL: urlValue, Interactive: false, AllocateTTY: false, SendHeartbeat: true, EnvVars: envVars, Workdir: workdir, Username: username, CmdArgs: command}
	if webttyClientUsesRstream(urlValue) {
		runtime, err := resolveMCPRuntimeForArgs(ctx, runtimeArgs)
		if err != nil {
			return nil, err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return nil, err
		}
		cfg.DialContext = newWebTTYClientDialContext(client)
	}
	return runWebTTYClientCapture(ctx, cfg)
}

func parseRemoteExposeResult(expose remoteExposeArgs, command []string, result *webTTYClientResult) (remoteExposeResult, error) {
	parsed := remoteExposeResult{ID: expose.ID, Status: "unknown", Command: command, WebTTY: *result}
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, remoteExposeStatusLine):
			parsed.Status = strings.TrimPrefix(line, remoteExposeStatusLine)
		case strings.HasPrefix(line, "RSTREAM_REMOTE_EXPOSE_PID="):
			pid, _ := strconv.Atoi(strings.TrimPrefix(line, "RSTREAM_REMOTE_EXPOSE_PID="))
			parsed.PID = pid
		case strings.HasPrefix(line, "RSTREAM_REMOTE_EXPOSE_LOG="):
			parsed.LogPath = strings.TrimPrefix(line, "RSTREAM_REMOTE_EXPOSE_LOG=")
		case strings.HasPrefix(line, remoteExposeStatusJSONLine):
			status := forwardStatus{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, remoteExposeStatusJSONLine)), &status); err != nil {
				return parsed, err
			}
			parsed.Forward = &status
		}
	}
	if result.ExitCode != 0 && parsed.Status == "unknown" {
		parsed.Status = "failed"
	}
	return parsed, nil
}

func remoteExposeForwardingURL(result remoteExposeResult) string {
	if result.Forward != nil && result.Forward.Forwarding != nil {
		return strings.TrimSpace(*result.Forward.Forwarding)
	}
	return ""
}

func remoteMCPListParams(filter string) (*rstream.ListTunnelsParams, error) {
	params, err := buildTunnelListParams(filter)
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = &rstream.ListTunnelsParams{}
	}
	if params.Filters == nil {
		params.Filters = &rstream.ListTunnelsFilters{}
	}
	if params.Filters.Labels == nil {
		params.Filters.Labels = map[string]*string{}
	}
	status := "online"
	protocol := mcpApplicationProtocol
	params.Filters.Status = &status
	params.Filters.Labels[mcpApplicationProtocolKey] = &protocol
	return params, nil
}

func remoteMCPEndpointsFromTunnels(tunnels []rstream.TunnelInventory) []map[string]any {
	out := make([]map[string]any, 0, len(tunnels))
	for _, tunnel := range tunnels {
		out = append(out, remoteMCPEndpointPayload(tunnel))
	}
	return out
}

func remoteMCPEndpointPayload(tunnel rstream.TunnelInventory) map[string]any {
	path := tunnel.Labels[mcpPathLabel]
	if path == "" {
		path = mcpHTTPPath
	}
	forwarding, _ := rstream.FormatForwardingAddr(tunnel.TunnelProperties)
	payload := map[string]any{"id": statusString(tunnel.ID), "name": statusString(tunnel.Name), "path": path, "rstrm_url": "rstrm://" + remoteMCPTunnelTarget(tunnel), "status": tunnel.Status, "token_auth": tunnel.TokenAuth != nil && *tunnel.TokenAuth}
	if forwarding != "" {
		payload["url"] = strings.TrimRight(strings.TrimSuffix(forwarding, " (unpublished)"), "/") + path
	}
	if len(tunnel.Labels) > 0 {
		payload["labels"] = tunnel.Labels
	}
	return payload
}

func remoteMCPTunnelTarget(tunnel rstream.TunnelInventory) string {
	if tunnel.Name != nil && strings.TrimSpace(*tunnel.Name) != "" {
		return strings.TrimSpace(*tunnel.Name)
	}
	return statusString(tunnel.ID)
}

func remoteMCPEndpointFromArgs(ctx context.Context, args map[string]json.RawMessage) (remoteMCPEndpoint, string, error) {
	rawURL, err := mcpRequiredStringArg(args, "url")
	if err != nil {
		return remoteMCPEndpoint{}, "", err
	}
	token, err := mcpOptionalStringArg(args, "token", "")
	if err != nil {
		return remoteMCPEndpoint{}, "", err
	}
	endpoint, err := resolveRemoteMCPEndpoint(ctx, rawURL, args)
	return endpoint, token, err
}

func resolveRemoteMCPEndpoint(ctx context.Context, rawURL string, args map[string]json.RawMessage) (remoteMCPEndpoint, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return remoteMCPEndpoint{}, err
	}
	switch strings.ToLower(u.Scheme) {
	case "rstrm":
		target := strings.TrimSpace(u.Host)
		if target == "" {
			return remoteMCPEndpoint{}, fmt.Errorf("rstrm MCP URL is missing tunnel id or name")
		}
		remotePath := u.EscapedPath()
		if remotePath == "" {
			remotePath, err = mcpOptionalStringArg(args, "path", mcpHTTPPath)
			if err != nil {
				return remoteMCPEndpoint{}, err
			}
		}
		runtime, err := resolveMCPRuntimeForArgs(ctx, args)
		if err != nil {
			return remoteMCPEndpoint{}, err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return remoteMCPEndpoint{}, err
		}
		httpClient := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return client.Dial(ctx, rstream.Addr{IdOrName: target})
		}}}
		return remoteMCPEndpoint{Client: httpClient, URL: "http://" + target + normalizeRemoteMCPPath(remotePath)}, nil
	case "http", "https":
		if u.EscapedPath() == "" || u.EscapedPath() == "/" {
			remotePath, err := mcpOptionalStringArg(args, "path", "")
			if err != nil {
				return remoteMCPEndpoint{}, err
			}
			if remotePath != "" {
				u.Path = normalizeRemoteMCPPath(remotePath)
			}
		}
		return remoteMCPEndpoint{Client: http.DefaultClient, URL: u.String()}, nil
	default:
		return remoteMCPEndpoint{}, fmt.Errorf("unsupported MCP URL scheme %q", u.Scheme)
	}
}

func remoteMCPArguments(args map[string]json.RawMessage) (map[string]any, error) {
	raw, ok := args["arguments"]
	if ok {
		values := map[string]any{}
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("arguments must be an object: %w", err)
		}
		return values, nil
	}
	rawJSON, err := mcpOptionalStringArg(args, "arguments_json", "{}")
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if err := json.Unmarshal([]byte(rawJSON), &values); err != nil {
		return nil, fmt.Errorf("arguments_json must be a JSON object: %w", err)
	}
	return values, nil
}

func remoteMCPJSONRPC(ctx context.Context, endpoint remoteMCPEndpoint, token string, request map[string]any) (map[string]any, error) {
	client := endpoint.Client
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote MCP request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote MCP request returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("failed to decode remote MCP response: %w", err)
	}
	return out, nil
}

func mcpOptionalLabelMap(args map[string]json.RawMessage) (map[string]string, error) {
	values, err := mcpOptionalStringSliceArg(args, "labels")
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for _, value := range values {
		key, labelValue, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid label %q", value)
		}
		labels[strings.TrimSpace(key)] = strings.TrimSpace(labelValue)
	}
	return labels, nil
}

func boundedRemoteExposeTimeout(value *int) int {
	if value == nil {
		return 30
	}
	if *value < 1 {
		return 1
	}
	if *value > 120 {
		return 120
	}
	return *value
}

func remoteExposeID() string {
	return remoteExposeIDPrefix + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

func safeRemoteExposeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return remoteExposeID()
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return remoteExposeID()
	}
	return b.String()
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellSingleQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func normalizeRemoteMCPPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return mcpHTTPPath
	}
	if !strings.HasPrefix(value, "/") {
		return "/" + value
	}
	return value
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

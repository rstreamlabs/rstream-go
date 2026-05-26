// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

type mcpPreviewSession struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TunnelID   string    `json:"tunnel_id"`
	URL        string    `json:"url"`
	Forwarded  string    `json:"forwarded"`
	Host       string    `json:"host"`
	Port       string    `json:"port"`
	PID        int       `json:"pid"`
	TokenAuth  bool      `json:"token_auth"`
	ConfigPath string    `json:"config_path,omitempty"`
	LogPath    string    `json:"log_path,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type mcpPreviewRegistryFile struct {
	Sessions []mcpPreviewSession `json:"previews"`
}

func mcpPreviewExpose(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, err := resolveMCPRuntime(true, true)
	if err != nil {
		return nil, err
	}
	host, port, props, tokenAuth, err := mcpPreviewArgs(args, runtime.Resolved.Engine)
	if err != nil {
		return nil, err
	}
	path, err := mcpPreviewRegistryPath(runtime.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := mcpPreviewPruneRegistry(path); err != nil {
		return nil, err
	}
	cmd, logPath, err := startMCPPreviewProcess(runtime.ConfigPath, host, port, props)
	if err != nil {
		return nil, err
	}
	session, err := waitMCPPreviewProcessOnline(ctx, cmd, logPath, props, host, port, tokenAuth, runtime.ConfigPath)
	if err != nil {
		if cmd.Process != nil {
			_ = terminateMCPPreviewProcess(cmd.Process.Pid)
		}
		return nil, err
	}
	if err := mcpPreviewAddSession(path, *session); err != nil {
		_ = terminateMCPPreviewProcess(session.PID)
		return nil, err
	}
	return mcpJSONResult(session, false)
}

func mcpPreviewList() (map[string]any, error) {
	path, err := mcpPreviewRegistryPathFromConfig()
	if err != nil {
		return nil, err
	}
	sessions, err := mcpPreviewListFromPath(path)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"previews": sessions}, false)
}

func mcpPreviewStop(args map[string]json.RawMessage) (map[string]any, error) {
	id, err := mcpRequiredStringArg(args, "id")
	if err != nil {
		return nil, err
	}
	path, err := mcpPreviewRegistryPathFromConfig()
	if err != nil {
		return nil, err
	}
	result, err := mcpPreviewStopFromPath(path, id)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(result, false)
}

func mcpPreviewArgs(args map[string]json.RawMessage, engine string) (string, string, *rstream.TunnelProperties, bool, error) {
	port, err := mcpRequiredStringArg(args, "port")
	if err != nil {
		return "", "", nil, false, err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", "", nil, false, fmt.Errorf("port must be numeric")
	}
	host, err := mcpOptionalStringArg(args, "host", "localhost")
	if err != nil {
		return "", "", nil, false, err
	}
	name, err := mcpOptionalStringArg(args, "name", "")
	if err != nil {
		return "", "", nil, false, err
	}
	if name == "" {
		name = fmt.Sprintf("codex-preview-%d", time.Now().Unix())
	}
	stableDomain, err := mcpOptionalStringArg(args, "stable_domain", "")
	if err != nil {
		return "", "", nil, false, err
	}
	tokenAuth, err := mcpOptionalBoolArg(args, "token_auth", false)
	if err != nil {
		return "", "", nil, false, err
	}
	props := mcpPreviewTunnelProperties(name, stableDomain, port, tokenAuth)
	if err := rstream.MaybeSetGeneratedStableDomain(props, engine); err != nil {
		return "", "", nil, false, fmt.Errorf("failed to generate stable domain: %w", err)
	}
	return host, port, props, tokenAuth, nil
}

func mcpPreviewTunnelProperties(name string, stableDomain string, port string, tokenAuth bool) *rstream.TunnelProperties {
	labels := map[string]string{"application-protocol": "rstream.preview", "rstream.preview.kind": "codex", "rstream.preview.port": port}
	props := &rstream.TunnelProperties{Name: &name, Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream), Publish: rstream.BoolPtr(true), Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP), Labels: labels, HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1)}
	if stableDomain != "" {
		props.Hostname = &stableDomain
	}
	if tokenAuth {
		props.TokenAuth = rstream.BoolPtr(true)
	}
	return props
}

func startMCPPreviewProcess(configPath string, host string, port string, props *rstream.TunnelProperties) (*exec.Cmd, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	registryPath, err := mcpPreviewRegistryPath(configPath)
	if err != nil {
		return nil, "", err
	}
	logDir := filepath.Join(filepath.Dir(registryPath), "mcp-previews")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%d.log", safeMCPPreviewFilePart(statusString(props.Name)), time.Now().UnixNano()))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer logFile.Close()
	cmd := exec.Command(executable, mcpPreviewForwardArgs(host, port, props)...)
	cmd.Env = append(os.Environ(), "RSTREAM_CONFIG="+configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureMCPPreviewProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	return cmd, logPath, nil
}

func mcpPreviewForwardArgs(host string, port string, props *rstream.TunnelProperties) []string {
	args := []string{"forward", net.JoinHostPort(host, port), "--output", "json", "--name", statusString(props.Name), "--publish", "--http", "--http-version", string(rstream.HTTP1_1)}
	if props.Hostname != nil && strings.TrimSpace(*props.Hostname) != "" {
		args = append(args, "--host", *props.Hostname)
	}
	if props.TokenAuth != nil && *props.TokenAuth {
		args = append(args, "--token-auth")
	}
	keys := make([]string, 0, len(props.Labels))
	for key := range props.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+props.Labels[key])
	}
	return args
}

func waitMCPPreviewProcessOnline(ctx context.Context, cmd *exec.Cmd, logPath string, props *rstream.TunnelProperties, host string, port string, tokenAuth bool, configPath string) (*mcpPreviewSession, error) {
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		session, statusErr := readMCPPreviewOnlineStatus(logPath, props, host, port, tokenAuth, configPath, cmd.Process.Pid)
		if session != nil || statusErr != nil {
			return session, statusErr
		}
		select {
		case err := <-exitCh:
			if err == nil {
				return nil, fmt.Errorf("preview tunnel stopped before becoming online")
			}
			return nil, fmt.Errorf("preview tunnel failed before becoming online: %w", err)
		case <-time.After(200 * time.Millisecond):
		case <-timeout.C:
			return nil, fmt.Errorf("timed out waiting for preview tunnel")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func readMCPPreviewOnlineStatus(logPath string, props *rstream.TunnelProperties, host string, port string, tokenAuth bool, configPath string, pid int) (*mcpPreviewSession, error) {
	file, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var status forwardStatus
		if err := json.Unmarshal(scanner.Bytes(), &status); err != nil {
			continue
		}
		if status.Status != nil && strings.Contains(*status.Status, "failed") {
			return nil, fmt.Errorf("%s", *status.Status)
		}
		if status.Status != nil && *status.Status == "online" && status.Forwarding != nil && status.TunnelID != nil {
			return &mcpPreviewSession{ID: *status.TunnelID, Name: statusString(props.Name), TunnelID: *status.TunnelID, URL: *status.Forwarding, Forwarded: statusString(status.Forwarded), Host: host, Port: port, PID: pid, TokenAuth: tokenAuth, ConfigPath: configPath, LogPath: logPath, CreatedAt: time.Now().UTC()}, nil
		}
	}
	return nil, scanner.Err()
}

func mcpPreviewRegistryPathFromConfig() (string, error) {
	path, _, err := mcpLoadConfig()
	if err != nil {
		return "", err
	}
	return mcpPreviewRegistryPath(path)
}

func mcpPreviewRegistryPath(configPath string) (string, error) {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.DefaultConfigPath()
		if err != nil {
			return "", err
		}
		configPath = path
	}
	return filepath.Join(filepath.Dir(configPath), "mcp-previews.json"), nil
}

func mcpPreviewListFromPath(path string) ([]mcpPreviewSession, error) {
	if err := mcpPreviewPruneRegistry(path); err != nil {
		return nil, err
	}
	return readMCPPreviewRegistry(path)
}

func mcpPreviewStopFromPath(path string, id string) (map[string]any, error) {
	var matched *mcpPreviewSession
	if err := updateMCPPreviewRegistry(path, func(sessions []mcpPreviewSession) ([]mcpPreviewSession, error) {
		next := make([]mcpPreviewSession, 0, len(sessions))
		for _, session := range sessions {
			if session.ID == id || session.TunnelID == id {
				copy := session
				matched = &copy
				continue
			}
			next = append(next, session)
		}
		return next, nil
	}); err != nil {
		return nil, err
	}
	if matched == nil {
		return nil, fmt.Errorf("preview %q not found", id)
	}
	stopped := false
	if mcpPreviewProcessRunning(matched.PID) {
		if err := terminateMCPPreviewProcess(matched.PID); err != nil {
			return nil, err
		}
		stopped = true
	}
	return map[string]any{"stopped": stopped, "id": matched.ID, "tunnel_id": matched.TunnelID, "pid": matched.PID}, nil
}

func mcpPreviewAddSession(path string, session mcpPreviewSession) error {
	return updateMCPPreviewRegistry(path, func(sessions []mcpPreviewSession) ([]mcpPreviewSession, error) {
		next := make([]mcpPreviewSession, 0, len(sessions)+1)
		for _, existing := range sessions {
			if existing.ID != session.ID && existing.TunnelID != session.TunnelID && mcpPreviewProcessRunning(existing.PID) {
				next = append(next, existing)
			}
		}
		next = append(next, session)
		return next, nil
	})
}

func mcpPreviewPruneRegistry(path string) error {
	return updateMCPPreviewRegistry(path, func(sessions []mcpPreviewSession) ([]mcpPreviewSession, error) {
		next := make([]mcpPreviewSession, 0, len(sessions))
		for _, session := range sessions {
			if mcpPreviewProcessRunning(session.PID) {
				next = append(next, session)
			}
		}
		return next, nil
	})
}

func readMCPPreviewRegistry(path string) ([]mcpPreviewSession, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []mcpPreviewSession{}, nil
		}
		return nil, err
	}
	defer file.Close()
	var data mcpPreviewRegistryFile
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, err
	}
	return data.Sessions, nil
}

func updateMCPPreviewRegistry(path string, update func([]mcpPreviewSession) ([]mcpPreviewSession, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	sessions, err := readMCPPreviewRegistry(path)
	if err != nil {
		return err
	}
	next, err := update(sessions)
	if err != nil {
		return err
	}
	return writeMCPPreviewRegistry(path, next)
}

func writeMCPPreviewRegistry(path string, sessions []mcpPreviewSession) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-previews-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(mcpPreviewRegistryFile{Sessions: sessions}); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func statusString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func safeMCPPreviewFilePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "preview"
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
		return "preview"
	}
	return b.String()
}

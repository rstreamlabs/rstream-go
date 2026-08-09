// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const codexMCPVerifyTimeout = 10 * time.Second

var codexEssentialTools = []string{
	"rstream_runtime_status",
	"rstream_context_list",
	"rstream_webtty_list",
	"rstream_webtty_exec",
}

type codexDiagnostic struct {
	Code    string `json:"code" yaml:"code"`
	Level   string `json:"level" yaml:"level"`
	Message string `json:"message" yaml:"message"`
}

type codexMCPVerification struct {
	Status          string   `json:"status" yaml:"status"`
	ProtocolVersion string   `json:"protocol_version,omitempty" yaml:"protocol_version,omitempty"`
	ServerName      string   `json:"server_name,omitempty" yaml:"server_name,omitempty"`
	ServerVersion   string   `json:"server_version,omitempty" yaml:"server_version,omitempty"`
	ToolCount       int      `json:"tool_count,omitempty" yaml:"tool_count,omitempty"`
	RequiredTools   []string `json:"required_tools" yaml:"required_tools"`
}

type codexSetupResult struct {
	Changed        bool                  `json:"changed" yaml:"changed"`
	Status         string                `json:"status" yaml:"status"`
	Config         string                `json:"config" yaml:"config"`
	Command        string                `json:"command" yaml:"command"`
	ReloadRequired bool                  `json:"reload_required" yaml:"reload_required"`
	Diagnostics    []codexDiagnostic     `json:"diagnostics" yaml:"diagnostics"`
	Verification   *codexMCPVerification `json:"verification,omitempty" yaml:"verification,omitempty"`
}

type codexRstreamConfig struct {
	Command           string
	CommandSet        bool
	CommandValid      bool
	Args              []string
	ArgsSet           bool
	ArgsValid         bool
	StartupTimeout    int64
	StartupTimeoutSet bool
	StartupValid      bool
	ToolTimeout       int64
	ToolTimeoutSet    bool
	ToolTimeoutValid  bool
	Env               map[string]string
	EnvSet            bool
	EnvValid          bool
	UnknownMainKeys   bool
	UnknownEnvKeys    bool
}

type codexSetupConflictError struct {
	reason string
}

func (e *codexSetupConflictError) Error() string {
	return fmt.Sprintf("existing rstream MCP configuration appears intentionally customized (%s); re-run with --force to replace the complete [mcp_servers.rstream] subtree", e.reason)
}

var codexCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "codex",
	Short:        "Configure Codex integrations",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var codexSetupCmd = newCodexSetupCommand()

func newCodexSetupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "setup",
		Short:        "Configure Codex to use rstream MCP tools",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE:         runCodexSetup,
	}
	cmd.Flags().SortFlags = false
	cmd.Flags().String("config", "", "Codex config.toml path (defaults to $CODEX_HOME/config.toml or ~/.codex/config.toml)")
	cmd.Flags().String("command", "", "rstream executable path (defaults to this executable)")
	cmd.Flags().Bool("force", false, "replace the complete existing [mcp_servers.rstream] subtree")
	cmd.Flags().Bool("print", false, "print the TOML section instead of editing config")
	cmd.Flags().Bool("verify", false, "start the configured MCP server and verify its protocol and essential tools")
	cmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	return cmd
}

func init() {
	codexCmd.Flags().SortFlags = false
	codexCmd.PersistentFlags().SortFlags = false
	codexCmd.AddCommand(codexSetupCmd)
	rootCmd.AddCommand(codexCmd)
}

func runCodexSetup(cmd *cobra.Command, args []string) error {
	output, _ := cmd.Flags().GetString("output")
	if err := validateOutputMode(output, "none", "json", "yaml"); err != nil {
		return err
	}
	printOnly, _ := cmd.Flags().GetBool("print")
	verify, _ := cmd.Flags().GetBool("verify")
	if printOnly && verify {
		return errors.New("--print and --verify cannot be used together")
	}

	configPath, _ := cmd.Flags().GetString("config")
	if strings.TrimSpace(configPath) == "" {
		path, err := defaultCodexConfigPath()
		if err != nil {
			return err
		}
		configPath = path
	}
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("failed to resolve Codex config path: %w", err)
	}
	configPath = absConfigPath

	commandPath, _ := cmd.Flags().GetString("command")
	if strings.TrimSpace(commandPath) == "" {
		path, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to resolve current executable: %w", err)
		}
		commandPath = path
	}
	resolvedCommand, err := resolveCodexExecutable(commandPath)
	if err != nil {
		return err
	}
	env := codexRuntimeOverrides()
	if err := validateCodexRuntimeOverrides(env); err != nil {
		return err
	}
	block := codexRstreamMCPBlockWithEnv(resolvedCommand, env)
	if printOnly {
		_, err := fmt.Fprint(cmd.OutOrStdout(), block)
		return err
	}

	force, _ := cmd.Flags().GetBool("force")
	result, setupErr := configureCodexRstreamMCP(configPath, resolvedCommand, env, block, force)
	if setupErr == nil && verify {
		verification, verifyErr := verifyCodexMCP(cmd.Context(), resolvedCommand, []string{"mcp", "serve"}, env, codexMCPVerifyTimeout)
		result.Verification = &verification
		if verifyErr != nil {
			result.Status = "verification_failed"
			result.Diagnostics = append(result.Diagnostics, codexDiagnostic{Code: "verification_failed", Level: "error", Message: verifyErr.Error()})
			setupErr = verifyErr
		} else {
			result.Diagnostics = append(result.Diagnostics, codexDiagnostic{Code: "verification_succeeded", Level: "info", Message: "MCP initialize and tools/list completed successfully."})
		}
	}
	if err := writeCodexSetupResult(cmd.OutOrStdout(), output, result); err != nil {
		return err
	}
	return setupErr
}

func defaultCodexConfigPath() (string, error) {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func codexRuntimeOverrides() map[string]string {
	env := map[string]string{}
	for _, name := range []string{"RSTREAM_API_URL", "RSTREAM_CONFIG", "RSTREAM_CONTEXT"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			env[name] = value
		}
	}
	return env
}

func codexRstreamMCPBlock(commandPath string) string {
	return codexRstreamMCPBlockWithEnv(commandPath, codexRuntimeOverrides())
}

func codexRstreamMCPBlockWithEnv(commandPath string, env map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.rstream]\ncommand = %s\nargs = [\"mcp\", \"serve\"]\nstartup_timeout_sec = 30\ntool_timeout_sec = 300\n", tomlString(commandPath))
	keys := make([]string, 0, len(env))
	for key, value := range env {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		b.WriteString("env = { ")
		for i, key := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s = %s", key, tomlString(env[key]))
		}
		b.WriteString(" }\n")
	}
	return b.String()
}

func tomlString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func configureCodexRstreamMCP(configPath string, commandPath string, desiredEnv map[string]string, block string, force bool) (codexSetupResult, error) {
	result := codexSetupResult{
		Config:      configPath,
		Command:     commandPath,
		Diagnostics: []codexDiagnostic{},
	}
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("failed to read Codex config: %w", err)
	}
	content := string(data)
	existing, exists, err := parseCodexRstreamConfig(content)
	if err != nil {
		return result, err
	}
	if !exists {
		next, _, err := replaceTomlTree(content, []string{"mcp_servers", "rstream"}, block)
		if err != nil {
			return result, err
		}
		if err := writeCodexConfigAtomically(configPath, next); err != nil {
			return result, err
		}
		result.Changed = true
		result.Status = "installed"
		result.ReloadRequired = true
		result.Diagnostics = append(result.Diagnostics, codexDiagnostic{Code: "configuration_installed", Level: "info", Message: "The rstream MCP server entry was added to Codex."})
		return result, nil
	}

	analysis := analyzeCodexRstreamConfig(existing, commandPath, desiredEnv)
	result.Diagnostics = append(result.Diagnostics, analysis.diagnostics...)
	if analysis.equivalent && analysis.usable && !force {
		result.Status = "already_configured"
		result.Diagnostics = append(result.Diagnostics, codexDiagnostic{Code: "configuration_valid", Level: "info", Message: "The existing rstream MCP configuration is equivalent and usable."})
		return result, nil
	}
	if analysis.customized && !force {
		result.Status = "conflict"
		result.Diagnostics = append(result.Diagnostics, codexDiagnostic{Code: "force_required", Level: "error", Message: "The existing configuration looks customized; use --force to replace its complete rstream subtree."})
		return result, &codexSetupConflictError{reason: analysis.customReason}
	}

	next, replaced, err := replaceTomlTree(content, []string{"mcp_servers", "rstream"}, block)
	if err != nil {
		return result, err
	}
	if !replaced {
		return result, errors.New("failed to locate the existing [mcp_servers.rstream] subtree safely")
	}
	if err := writeCodexConfigAtomically(configPath, next); err != nil {
		return result, err
	}
	result.Changed = true
	result.Status = "repaired"
	result.ReloadRequired = true
	result.Diagnostics = append(result.Diagnostics, codexDiagnostic{Code: "configuration_repaired", Level: "info", Message: "The complete rstream MCP subtree was replaced with a usable configuration."})
	return result, nil
}

type codexConfigAnalysis struct {
	equivalent   bool
	usable       bool
	customized   bool
	customReason string
	diagnostics  []codexDiagnostic
}

func analyzeCodexRstreamConfig(existing codexRstreamConfig, desiredCommand string, desiredEnv map[string]string) codexConfigAnalysis {
	analysis := codexConfigAnalysis{usable: true, diagnostics: []codexDiagnostic{}}
	configuredCommand := ""
	commandUsable := false
	if !existing.CommandSet || !existing.CommandValid {
		analysis.usable = false
		analysis.diagnostics = append(analysis.diagnostics, codexDiagnostic{Code: "command_invalid", Level: "warning", Message: "The configured MCP command is missing or invalid."})
	} else if resolved, err := resolveCodexExecutable(existing.Command); err != nil {
		analysis.usable = false
		analysis.diagnostics = append(analysis.diagnostics, codexDiagnostic{Code: "command_not_executable", Level: "warning", Message: "The configured MCP command does not exist or is not executable."})
	} else {
		configuredCommand = resolved
		commandUsable = true
	}

	argsExpected := existing.ArgsSet && existing.ArgsValid && stringSlicesEqual(existing.Args, []string{"mcp", "serve"})
	startupExpected := existing.StartupTimeoutSet && existing.StartupValid && existing.StartupTimeout == 30
	toolExpected := existing.ToolTimeoutSet && existing.ToolTimeoutValid && existing.ToolTimeout == 300
	if !argsExpected || !startupExpected || !toolExpected || !existing.EnvValid {
		analysis.usable = false
	}

	for key, value := range existing.Env {
		if key != "RSTREAM_CONFIG" {
			continue
		}
		if err := readableRegularFile(value); err != nil {
			analysis.usable = false
			analysis.diagnostics = append(analysis.diagnostics, codexDiagnostic{Code: "rstream_config_unreadable", Level: "warning", Message: "The configured RSTREAM_CONFIG file does not exist or is not readable."})
		}
	}

	commandEquivalent := commandUsable && sameResolvedExecutable(configuredCommand, desiredCommand)
	envEquivalent := stringMapsEqual(existing.Env, desiredEnv)
	analysis.equivalent = commandEquivalent && argsExpected && startupExpected && toolExpected && existing.EnvValid && envEquivalent && !existing.UnknownMainKeys && !existing.UnknownEnvKeys
	if analysis.equivalent && analysis.usable {
		return analysis
	}

	customReasons := []string{}
	if existing.UnknownMainKeys {
		customReasons = append(customReasons, "it contains additional rstream server settings")
	}
	if existing.UnknownEnvKeys {
		customReasons = append(customReasons, "it contains additional environment tables or variables")
	}
	if commandUsable && !commandEquivalent {
		customReasons = append(customReasons, "its command points to a different executable")
	}
	if existing.ArgsSet && existing.ArgsValid && !argsExpected {
		customReasons = append(customReasons, "its command arguments differ")
	}
	if existing.StartupTimeoutSet && existing.StartupValid && !startupExpected {
		customReasons = append(customReasons, "its startup timeout differs")
	}
	if existing.ToolTimeoutSet && existing.ToolTimeoutValid && !toolExpected {
		customReasons = append(customReasons, "its tool timeout differs")
	}
	if existing.EnvValid && !envEquivalent {
		for key, value := range existing.Env {
			_, wanted := desiredEnv[key]
			if wanted {
				continue
			}
			if key == "RSTREAM_CONFIG" && readableRegularFile(value) != nil {
				continue
			}
			customReasons = append(customReasons, "its runtime environment differs")
			break
		}
	}
	if len(customReasons) > 0 {
		analysis.customized = true
		analysis.customReason = customReasons[0]
		analysis.diagnostics = append(analysis.diagnostics, codexDiagnostic{Code: "custom_configuration", Level: "warning", Message: "The existing rstream MCP entry contains intentional-looking custom values."})
	}
	return analysis
}

func parseCodexRstreamConfig(content string) (codexRstreamConfig, bool, error) {
	if strings.TrimSpace(content) == "" {
		return codexRstreamConfig{}, false, nil
	}
	var document map[string]any
	if err := toml.Unmarshal([]byte(content), &document); err != nil {
		return codexRstreamConfig{}, false, errors.New("codex config.toml is invalid TOML; fix its syntax before running setup")
	}
	mcpServers, ok := document["mcp_servers"].(map[string]any)
	if !ok {
		return codexRstreamConfig{}, false, nil
	}
	raw, exists := mcpServers["rstream"]
	if !exists {
		return codexRstreamConfig{}, false, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return codexRstreamConfig{UnknownMainKeys: true}, true, nil
	}
	config := codexRstreamConfig{Env: map[string]string{}, EnvValid: true}
	for key, value := range table {
		switch key {
		case "command":
			config.CommandSet = true
			config.Command, config.CommandValid = value.(string)
		case "args":
			config.ArgsSet = true
			config.Args, config.ArgsValid = tomlStringArray(value)
		case "startup_timeout_sec":
			config.StartupTimeoutSet = true
			config.StartupTimeout, config.StartupValid = tomlInteger(value)
		case "tool_timeout_sec":
			config.ToolTimeoutSet = true
			config.ToolTimeout, config.ToolTimeoutValid = tomlInteger(value)
		case "env":
			config.EnvSet = true
			envTable, ok := value.(map[string]any)
			if !ok {
				config.EnvValid = false
				continue
			}
			for envKey, rawEnvValue := range envTable {
				if envKey != "RSTREAM_API_URL" && envKey != "RSTREAM_CONFIG" && envKey != "RSTREAM_CONTEXT" {
					config.UnknownEnvKeys = true
					continue
				}
				envValue, ok := rawEnvValue.(string)
				if !ok {
					config.EnvValid = false
					continue
				}
				config.Env[envKey] = envValue
			}
		default:
			config.UnknownMainKeys = true
		}
	}
	return config, true, nil
}

func tomlStringArray(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.(string)
		if !ok {
			return nil, false
		}
		result = append(result, item)
	}
	return result, true
}

func tomlInteger(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	default:
		return 0, false
	}
}

func replaceTomlTree(content string, prefix []string, block string) (string, bool, error) {
	lines := strings.SplitAfter(content, "\n")
	type section struct {
		start int
		end   int
		path  []string
	}
	sections := []section{}
	for i, line := range lines {
		path, header, err := parseTomlTableHeader(line)
		if err != nil {
			return "", false, errors.New("failed to inspect Codex TOML table headers safely")
		}
		if !header {
			continue
		}
		if len(sections) > 0 {
			sections[len(sections)-1].end = tomlTablePreambleStart(lines, sections[len(sections)-1].start+1, i)
		}
		sections = append(sections, section{start: i, end: len(lines), path: path})
	}
	targets := map[int]int{}
	firstTarget := -1
	for _, section := range sections {
		if !tomlPathHasPrefix(section.path, prefix) {
			continue
		}
		targets[section.start] = section.end
		if firstTarget == -1 {
			firstTarget = section.start
		}
	}
	if firstTarget == -1 {
		if strings.TrimSpace(content) == "" {
			return block, false, nil
		}
		separator := "\n\n"
		if strings.HasSuffix(content, "\n") {
			separator = "\n"
		}
		return content + separator + block, false, nil
	}

	var result strings.Builder
	inserted := false
	for i := 0; i < len(lines); {
		if end, target := targets[i]; target {
			if !inserted {
				result.WriteString(block)
				inserted = true
			}
			i = end
			continue
		}
		result.WriteString(lines[i])
		i++
	}
	return result.String(), true, nil
}

func tomlTablePreambleStart(lines []string, lowerBound int, header int) int {
	start := header
	for start > lowerBound {
		trimmed := strings.TrimSpace(lines[start-1])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		start--
	}
	return start
}

func parseTomlTableHeader(line string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false, nil
	}
	arrayTable := strings.HasPrefix(trimmed, "[[")
	openLength, closeLength := 1, 1
	if arrayTable {
		openLength, closeLength = 2, 2
	}
	quote := byte(0)
	escaped := false
	closeAt := -1
	for i := openLength; i < len(trimmed); i++ {
		char := trimmed[i]
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && char == '\\' {
				escaped = true
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '"' || char == '\'' {
			quote = char
			continue
		}
		if char != ']' {
			continue
		}
		if closeLength == 2 && (i+1 >= len(trimmed) || trimmed[i+1] != ']') {
			continue
		}
		closeAt = i
		break
	}
	if closeAt == -1 || quote != 0 {
		return nil, false, errors.New("invalid TOML table header")
	}
	rest := strings.TrimSpace(trimmed[closeAt+closeLength:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, false, errors.New("invalid TOML table header suffix")
	}
	path, err := parseTomlDottedKey(trimmed[openLength:closeAt])
	if err != nil {
		return nil, false, err
	}
	return path, true, nil
}

func parseTomlDottedKey(value string) ([]string, error) {
	path := []string{}
	for i := 0; ; {
		for i < len(value) && (value[i] == ' ' || value[i] == '\t') {
			i++
		}
		if i >= len(value) {
			if len(path) == 0 {
				return nil, errors.New("empty TOML table path")
			}
			return path, nil
		}
		var key string
		if value[i] == '"' {
			start := i
			i++
			escaped := false
			for i < len(value) {
				if escaped {
					escaped = false
					i++
					continue
				}
				if value[i] == '\\' {
					escaped = true
					i++
					continue
				}
				if value[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(value) || value[i-1] != '"' {
				return nil, errors.New("unterminated TOML quoted key")
			}
			decoded, err := strconv.Unquote(value[start:i])
			if err != nil {
				return nil, errors.New("invalid TOML quoted key")
			}
			key = decoded
		} else if value[i] == '\'' {
			i++
			start := i
			for i < len(value) && value[i] != '\'' {
				i++
			}
			if i >= len(value) {
				return nil, errors.New("unterminated TOML literal key")
			}
			key = value[start:i]
			i++
		} else {
			start := i
			for i < len(value) && value[i] != '.' && value[i] != ' ' && value[i] != '\t' {
				i++
			}
			if start == i {
				return nil, errors.New("invalid TOML bare key")
			}
			key = value[start:i]
		}
		path = append(path, key)
		for i < len(value) && (value[i] == ' ' || value[i] == '\t') {
			i++
		}
		if i >= len(value) {
			return path, nil
		}
		if value[i] != '.' {
			return nil, errors.New("invalid TOML dotted key")
		}
		i++
	}
}

func tomlPathHasPrefix(path []string, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func writeCodexConfigAtomically(configPath string, content string) error {
	var document map[string]any
	if err := toml.Unmarshal([]byte(content), &document); err != nil {
		return errors.New("refusing to write an invalid Codex TOML configuration")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("failed to create Codex config directory: %w", err)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(configPath); err == nil {
		mode = info.Mode()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect Codex config permissions: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".codex-config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary Codex config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to preserve Codex config permissions: %w", err)
	}
	if _, err := io.WriteString(tmp, content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temporary Codex config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync temporary Codex config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary Codex config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("failed to replace Codex config: %w", err)
	}
	return nil
}

func resolveCodexExecutable(commandPath string) (string, error) {
	commandPath = strings.TrimSpace(commandPath)
	if commandPath == "" {
		return "", errors.New("rstream command path is empty")
	}
	resolved, err := exec.LookPath(commandPath)
	if err != nil {
		return "", errors.New("rstream command does not exist or is not executable")
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", errors.New("failed to resolve rstream command path")
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		return "", errors.New("rstream command does not exist or is not executable")
	}
	return filepath.Clean(abs), nil
}

func validateCodexRuntimeOverrides(env map[string]string) error {
	if configPath := env["RSTREAM_CONFIG"]; configPath != "" {
		if err := readableRegularFile(configPath); err != nil {
			return errors.New("explicit RSTREAM_CONFIG does not exist or is not a readable file")
		}
	}
	return nil
}

func readableRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func sameResolvedExecutable(left string, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo) {
		return true
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func stringMapsEqual(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		if rightValue, ok := right[key]; !ok || rightValue != leftValue {
			return false
		}
	}
	return true
}

func writeCodexSetupResult(output io.Writer, format string, result codexSetupResult) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	case "yaml":
		data, err := yaml.Marshal(result)
		if err != nil {
			return err
		}
		_, err = output.Write(data)
		return err
	case "", "none":
		return writeCodexHumanResult(output, result)
	default:
		return validateOutputMode(format, "none", "json", "yaml")
	}
}

func writeCodexHumanResult(output io.Writer, result codexSetupResult) error {
	switch result.Status {
	case "installed":
		fmt.Fprintf(output, "Configured the rstream MCP server for Codex in %s.\n", result.Config)
	case "repaired":
		fmt.Fprintf(output, "Repaired the rstream MCP server configuration in %s.\n", result.Config)
	case "already_configured":
		fmt.Fprintf(output, "The rstream MCP server is already configured and usable in %s.\n", result.Config)
	case "verification_failed":
		fmt.Fprintf(output, "MCP verification failed for the rstream Codex configuration in %s.\n", result.Config)
	case "conflict":
		fmt.Fprintf(output, "The rstream MCP configuration in %s was not changed because it appears customized. Re-run with --force to replace its complete subtree.\n", result.Config)
	}
	if result.Command != "" {
		fmt.Fprintf(output, "Command: %s mcp serve\n", result.Command)
	}
	if result.Verification != nil && result.Verification.Status == "verified" {
		fmt.Fprintf(output, "MCP verification succeeded: protocol %s, rstream %s, %d tools.\n", result.Verification.ProtocolVersion, result.Verification.ServerVersion, result.Verification.ToolCount)
	}
	if result.ReloadRequired {
		fmt.Fprintln(output, "Open a new Codex task or reload Codex to load the rstream MCP server.")
	} else if result.Status == "already_configured" {
		fmt.Fprintln(output, "If the MCP server is not loaded in the current task, open a new Codex task or reload Codex.")
	}
	return nil
}

func verifyCodexMCP(ctx context.Context, command string, args []string, env map[string]string, timeout time.Duration) (codexMCPVerification, error) {
	verification := codexMCPVerification{Status: "failed", RequiredTools: append([]string(nil), codexEssentialTools...)}
	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	process := exec.CommandContext(verifyCtx, command, args...)
	process.Env = mergedEnvironment(os.Environ(), env)
	process.Stderr = io.Discard
	stdin, err := process.StdinPipe()
	if err != nil {
		return verification, errors.New("failed to open MCP server input")
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return verification, errors.New("failed to open MCP server output")
	}
	if err := process.Start(); err != nil {
		return verification, errors.New("failed to start the configured MCP server")
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	defer func() {
		_ = stdin.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = process.Process.Kill()
			<-done
		}
	}()
	reader := bufio.NewReader(stdout)

	initialize := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "rstream-codex-setup", "version": "1"},
		},
	}
	if err := writeCodexMCPJSON(stdin, initialize); err != nil {
		return verification, errors.New("failed to send MCP initialize")
	}
	initializeResponse, err := readCodexMCPResponse(verifyCtx, reader)
	if err != nil {
		return verification, safeMCPVerificationError("initialize", err)
	}
	if initializeResponse.Error != nil {
		return verification, errors.New("MCP initialize returned an error")
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(initializeResponse.Result, &initialized); err != nil {
		return verification, errors.New("MCP initialize returned an invalid result")
	}
	if initialized.ProtocolVersion != mcpProtocolVersion {
		return verification, errors.New("MCP initialize returned an unsupported protocol version")
	}
	if initialized.ServerInfo.Name != "rstream" {
		return verification, errors.New("configured MCP server did not identify itself as rstream")
	}

	if err := writeCodexMCPJSON(stdin, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return verification, errors.New("failed to send MCP initialized notification")
	}
	if err := writeCodexMCPJSON(stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return verification, errors.New("failed to send MCP tools/list")
	}
	toolsResponse, err := readCodexMCPResponse(verifyCtx, reader)
	if err != nil {
		return verification, safeMCPVerificationError("tools/list", err)
	}
	if toolsResponse.Error != nil {
		return verification, errors.New("MCP tools/list returned an error")
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsResponse.Result, &listed); err != nil {
		return verification, errors.New("MCP tools/list returned an invalid result")
	}
	present := map[string]bool{}
	for _, tool := range listed.Tools {
		present[tool.Name] = true
	}
	missing := []string{}
	for _, name := range codexEssentialTools {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return verification, fmt.Errorf("MCP tools/list is missing essential tools: %s", strings.Join(missing, ", "))
	}
	verification.Status = "verified"
	verification.ProtocolVersion = initialized.ProtocolVersion
	verification.ServerName = initialized.ServerInfo.Name
	verification.ServerVersion = initialized.ServerInfo.Version
	verification.ToolCount = len(listed.Tools)
	return verification, nil
}

type codexMCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

func writeCodexMCPJSON(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(payload, '\n'))
	return err
}

func readCodexMCPResponse(ctx context.Context, reader *bufio.Reader) (codexMCPResponse, error) {
	type readResult struct {
		response codexMCPResponse
		err      error
	}
	result := make(chan readResult, 1)
	go func() {
		response, err := readCodexMCPResponseBlocking(reader)
		result <- readResult{response: response, err: err}
	}()
	select {
	case <-ctx.Done():
		return codexMCPResponse{}, ctx.Err()
	case read := <-result:
		return read.response, read.err
	}
}

func readCodexMCPResponseBlocking(reader *bufio.Reader) (codexMCPResponse, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return codexMCPResponse{}, err
	}
	trimmed := strings.TrimSpace(line)
	var payload []byte
	if strings.HasPrefix(trimmed, "{") {
		payload = []byte(trimmed)
	} else {
		contentLength := -1
		if parsed, ok, err := parseMCPContentLengthHeader(strings.TrimRight(line, "\r\n")); err != nil {
			return codexMCPResponse{}, err
		} else if ok {
			contentLength = parsed
		}
		for {
			header, err := reader.ReadString('\n')
			if err != nil {
				return codexMCPResponse{}, err
			}
			header = strings.TrimRight(header, "\r\n")
			if header == "" {
				break
			}
			if parsed, ok, err := parseMCPContentLengthHeader(header); err != nil {
				return codexMCPResponse{}, err
			} else if ok {
				contentLength = parsed
			}
		}
		if contentLength < 0 || contentLength > mcpMaxMessageBytes {
			return codexMCPResponse{}, errors.New("invalid MCP response length")
		}
		payload = make([]byte, contentLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return codexMCPResponse{}, err
		}
	}
	var response codexMCPResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return codexMCPResponse{}, errors.New("invalid MCP JSON-RPC response")
	}
	return response, nil
}

func safeMCPVerificationError(stage string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("MCP %s timed out", stage)
	}
	return fmt.Errorf("MCP %s failed before a valid response was received", stage)
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	values := map[string]string{}
	order := []string{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = value
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = overrides[key]
	}
	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, key+"="+values[key])
	}
	return result
}

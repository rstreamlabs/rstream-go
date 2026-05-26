// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var codexCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "codex",
	Short:        "Configure Codex integrations",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var codexSetupCmd = &cobra.Command{
	Use:          "setup",
	Short:        "Configure Codex to use rstream MCP tools",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		if strings.TrimSpace(configPath) == "" {
			path, err := defaultCodexConfigPath()
			if err != nil {
				return err
			}
			configPath = path
		}
		commandPath, _ := cmd.Flags().GetString("command")
		if strings.TrimSpace(commandPath) == "" {
			path, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to resolve current executable: %w", err)
			}
			commandPath = path
		}
		block := codexRstreamMCPBlock(commandPath)
		printOnly, _ := cmd.Flags().GetBool("print")
		if printOnly {
			fmt.Fprint(cmd.OutOrStdout(), block)
			return nil
		}
		force, _ := cmd.Flags().GetBool("force")
		changed, err := writeCodexRstreamMCPConfig(configPath, block, force)
		if err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		return writeOptionalStructuredOutput(output, map[string]any{"config": configPath, "changed": changed})
	},
}

func init() {
	codexCmd.Flags().SortFlags = false
	codexCmd.PersistentFlags().SortFlags = false
	codexSetupCmd.Flags().SortFlags = false
	codexSetupCmd.Flags().String("config", "", "Codex config.toml path (defaults to $CODEX_HOME/config.toml or ~/.codex/config.toml)")
	codexSetupCmd.Flags().String("command", "", "rstream executable path (defaults to this executable)")
	codexSetupCmd.Flags().Bool("force", false, "replace an existing [mcp_servers.rstream] section")
	codexSetupCmd.Flags().Bool("print", false, "print the TOML section instead of editing config")
	codexSetupCmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	codexCmd.AddCommand(codexSetupCmd)
	rootCmd.AddCommand(codexCmd)
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

func codexRstreamMCPBlock(commandPath string) string {
	env := map[string]string{}
	if configPath := strings.TrimSpace(os.Getenv("RSTREAM_CONFIG")); configPath != "" {
		env["RSTREAM_CONFIG"] = configPath
	}
	return codexRstreamMCPBlockWithEnv(commandPath, env)
}

func codexRstreamMCPBlockWithEnv(commandPath string, env map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.rstream]\ncommand = %s\nargs = [\"mcp\", \"serve\"]\nstartup_timeout_sec = 30\n", tomlString(commandPath))
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
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func writeCodexRstreamMCPConfig(configPath string, block string, force bool) (bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to read Codex config: %w", err)
	}
	content := string(data)
	next, _, exists := upsertTomlSection(content, "[mcp_servers.rstream]", block, force)
	if exists && !force {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return false, fmt.Errorf("failed to create Codex config directory: %w", err)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(configPath); err == nil {
		mode = info.Mode()
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(next), mode); err != nil {
		return false, fmt.Errorf("failed to write temporary Codex config: %w", err)
	}
	if err := os.Rename(tmp, configPath); err != nil {
		return false, fmt.Errorf("failed to replace Codex config: %w", err)
	}
	return true, nil
}

func upsertTomlSection(content string, header string, block string, force bool) (string, bool, bool) {
	lines := strings.SplitAfter(content, "\n")
	start := -1
	end := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}
	if start == -1 {
		if strings.TrimSpace(content) == "" {
			return block, false, false
		}
		separator := "\n"
		if strings.HasSuffix(content, "\n") {
			separator = ""
		}
		return content + separator + "\n" + block, false, false
	}
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			end = i
			break
		}
	}
	if !force {
		return content, false, true
	}
	next := strings.Join(lines[:start], "") + block + strings.Join(lines[end:], "")
	return next, true, true
}

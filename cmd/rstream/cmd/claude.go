// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "claude",
	Short:        "Configure Claude Code integrations",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var claudeSetupCmd = &cobra.Command{
	Use:          "setup",
	Short:        "Configure Claude Code to use rstream MCP tools",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		if strings.TrimSpace(name) == "" {
			name = "rstream"
		}
		scope, _ := cmd.Flags().GetString("scope")
		scope = strings.TrimSpace(scope)
		if scope == "" {
			scope = "user"
		}
		if err := validateClaudeScope(scope); err != nil {
			return err
		}
		commandPath, _ := cmd.Flags().GetString("command")
		if strings.TrimSpace(commandPath) == "" {
			path, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to resolve current executable: %w", err)
			}
			commandPath = path
		}
		env := claudeRstreamMCPEnv()

		printOnly, _ := cmd.Flags().GetBool("print")
		if printOnly {
			fmt.Fprint(cmd.OutOrStdout(), claudeRstreamSetupPreview(name, scope, commandPath, env))
			return nil
		}

		// Claude Code stores user/local MCP servers in the stateful ~/.claude.json
		// file, so we delegate writes to `claude mcp add` rather than editing that
		// file by hand. This keeps the rstream side from drifting away from the
		// Claude Code config schema.
		claudePath, err := exec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("claude executable not found on PATH: install Claude Code, then rerun, or use --print to copy the configuration manually")
		}

		force, _ := cmd.Flags().GetBool("force")
		if force {
			remove := exec.CommandContext(cmd.Context(), claudePath, "mcp", "remove", name, "--scope", scope)
			remove.Stdout = cmd.OutOrStdout()
			remove.Stderr = cmd.ErrOrStderr()
			_ = remove.Run()
		}

		add := exec.CommandContext(cmd.Context(), claudePath, claudeMCPAddArgs(name, scope, commandPath, env)...)
		add.Stdout = cmd.OutOrStdout()
		add.Stderr = cmd.ErrOrStderr()
		if err := add.Run(); err != nil {
			return fmt.Errorf("failed to register rstream with Claude Code: %w", err)
		}

		output, _ := cmd.Flags().GetString("output")
		return writeOptionalStructuredOutput(output, map[string]any{"name": name, "scope": scope, "changed": true})
	},
}

func init() {
	claudeCmd.Flags().SortFlags = false
	claudeCmd.PersistentFlags().SortFlags = false
	claudeSetupCmd.Flags().SortFlags = false
	claudeSetupCmd.Flags().String("scope", "user", "Claude Code config scope (local, project, user)")
	claudeSetupCmd.Flags().String("name", "rstream", "MCP server name registered in Claude Code")
	claudeSetupCmd.Flags().String("command", "", "rstream executable path (defaults to this executable)")
	claudeSetupCmd.Flags().Bool("force", false, "replace an existing rstream MCP server entry")
	claudeSetupCmd.Flags().Bool("print", false, "print the configuration and command instead of editing Claude Code config")
	claudeSetupCmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	claudeCmd.AddCommand(claudeSetupCmd)
	rootCmd.AddCommand(claudeCmd)
}

func validateClaudeScope(scope string) error {
	switch scope {
	case "local", "project", "user":
		return nil
	default:
		return fmt.Errorf("invalid scope %q: expected local, project, or user", scope)
	}
}

func claudeRstreamMCPEnv() map[string]string {
	env := map[string]string{}
	if configPath := strings.TrimSpace(os.Getenv("RSTREAM_CONFIG")); configPath != "" {
		env["RSTREAM_CONFIG"] = configPath
	}
	return env
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key, value := range env {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func claudeMCPAddArgs(name, scope, commandPath string, env map[string]string) []string {
	args := []string{"mcp", "add", name, "--scope", scope}
	for _, key := range sortedEnvKeys(env) {
		args = append(args, "--env", key+"="+env[key])
	}
	args = append(args, "--", commandPath, "mcp", "serve")
	return args
}

func claudeRstreamMCPJSON(name, commandPath string, env map[string]string) string {
	entry := map[string]any{
		"type":    "stdio",
		"command": commandPath,
		"args":    []string{"mcp", "serve"},
	}
	keys := sortedEnvKeys(env)
	if len(keys) > 0 {
		filtered := map[string]string{}
		for _, key := range keys {
			filtered[key] = env[key]
		}
		entry["env"] = filtered
	}
	doc := map[string]any{
		"mcpServers": map[string]any{
			name: entry,
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ""
	}
	return string(data) + "\n"
}

func claudeRstreamSetupPreview(name, scope, commandPath string, env map[string]string) string {
	var b strings.Builder
	b.WriteString("# Project scope: add this to .mcp.json at the repository root.\n")
	b.WriteString(claudeRstreamMCPJSON(name, commandPath, env))
	b.WriteString("\n# Any scope: register with the Claude Code CLI.\n")
	b.WriteString("claude")
	for _, arg := range claudeMCPAddArgs(name, scope, commandPath, env) {
		b.WriteString(" ")
		b.WriteString(claudeShellArg(arg))
	}
	b.WriteString("\n")
	return b.String()
}

func claudeShellArg(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`*?|&;<>(){}[]#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

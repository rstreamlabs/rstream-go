// See LICENSE file in the project root for license information.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReadTokenInputOptional(t *testing.T) {
	command := tokenInputCommand()
	token, ok, err := readTokenInputOptional(command)
	if err != nil || ok || token != "" {
		t.Fatalf("empty input = %q, %v, %v", token, ok, err)
	}
	tokenPath := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(tokenPath, []byte(" file-token \n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	mustSetFlag(t, command, "token-file", tokenPath)
	token, ok, err = readTokenInputOptional(command)
	if err != nil || !ok || token != "file-token" {
		t.Fatalf("file token = %q, %v, %v", token, ok, err)
	}
}

func TestReadTokenFromFlags(t *testing.T) {
	command := tokenInputCommand()
	token, ok, err := readTokenFromFlags(command)
	if err != nil || ok || token != "" {
		t.Fatalf("empty flags = %q, %v, %v", token, ok, err)
	}
	mustSetFlag(t, command, "token", " flag-token ")
	token, ok, err = readTokenFromFlags(command)
	if err != nil || !ok || token != "flag-token" {
		t.Fatalf("flag token = %q, %v, %v", token, ok, err)
	}
	command = tokenInputCommand()
	mustSetFlag(t, command, "token-file", filepath.Join(t.TempDir(), "missing"))
	if _, _, err := readTokenFromFlags(command); err == nil || !strings.Contains(err.Error(), "failed to read token file") {
		t.Fatalf("expected missing token file error, got %v", err)
	}
}

func tokenInputCommand() *cobra.Command {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("token", "", "")
	command.Flags().Bool("stdin", false, "")
	command.Flags().Bool("token-stdin", false, "")
	command.Flags().String("token-file", "", "")
	return command
}

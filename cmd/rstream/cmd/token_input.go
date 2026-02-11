// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func readTokenInput(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0]), nil
	}
	stdin, _ := cmd.Flags().GetBool("stdin")
	file, _ := cmd.Flags().GetString("token-file")
	if stdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("failed to read token file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", errors.New("token is required")
}

func readTokenFromFlags(cmd *cobra.Command) (string, bool, error) {
	token, _ := cmd.Flags().GetString("token")
	if strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token), true, nil
	}
	stdin, _ := cmd.Flags().GetBool("token-stdin")
	file, _ := cmd.Flags().GetString("token-file")
	if stdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, err
		}
		return strings.TrimSpace(string(data)), true, nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", false, fmt.Errorf("failed to read token file: %w", err)
		}
		return strings.TrimSpace(string(data)), true, nil
	}
	return "", false, nil
}

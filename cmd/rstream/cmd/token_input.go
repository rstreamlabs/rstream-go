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

func readTokenInputOptional(cmd *cobra.Command) (string, bool, error) {
	stdin, _ := cmd.Flags().GetBool("stdin")
	tokenStdin, _ := cmd.Flags().GetBool("token-stdin")
	file, _ := cmd.Flags().GetString("token-file")
	if stdin || tokenStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, err
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", false, errors.New("token is empty")
		}
		return token, true, nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", false, fmt.Errorf("failed to read token file: %w", err)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", false, errors.New("token is empty")
		}
		return token, true, nil
	}
	return "", false, nil
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

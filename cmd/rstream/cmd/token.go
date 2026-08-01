// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "token",
	Short:        "Create short-lived rstream auth tokens",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var tokenCreateCmd = &cobra.Command{
	Use:          "create",
	Short:        "Create a control plane auth token",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		runtime, err := resolveControlPlane(cmd, true)
		if err != nil {
			return err
		}
		client := newRuntimeControlPlaneClient(runtime.Resolved)
		request, err := createTokenRequestFromFlags(cmd)
		if err != nil {
			return err
		}
		response, err := client.CreateToken(cmd.Context(), request)
		if err != nil {
			return mapControlPlaneError(err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "text":
			fmt.Fprintln(cmd.OutOrStdout(), response.Token)
			return nil
		case "json", "yaml":
			return writeStructuredOutput(output, response)
		default:
			return validateOutputMode(output, "text", "json", "yaml")
		}
	},
}

func init() {
	tokenCmd.Flags().SortFlags = false
	tokenCmd.PersistentFlags().SortFlags = false
	tokenCreateCmd.Flags().SortFlags = false
	tokenCreateCmd.Flags().StringArrayP("permission", "p", nil, "permission to include in the minted token (may be specified multiple times)")
	tokenCreateCmd.Flags().String("resources-json", "", "resource boundary JSON")
	tokenCreateCmd.Flags().String("resources-file", "", "read resource boundary JSON from file")
	tokenCreateCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	tokenCreateCmd.MarkFlagsMutuallyExclusive("resources-json", "resources-file")
	tokenCmd.AddCommand(tokenCreateCmd)
	rootCmd.AddCommand(tokenCmd)
}

func createTokenRequestFromFlags(cmd *cobra.Command) (controlplane.CreateTokenRequest, error) {
	var permissions *[]string
	if cmd.Flags().Changed("permission") {
		values, _ := cmd.Flags().GetStringArray("permission")
		normalized := normalizeTokenCreatePermissions(values)
		permissions = &normalized
	}
	resources, err := tokenCreateResourcesFromFlags(cmd)
	if err != nil {
		return controlplane.CreateTokenRequest{}, err
	}
	return controlplane.CreateTokenRequest{Permissions: permissions, Resources: resources}, nil
}

func normalizeTokenCreatePermissions(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func tokenCreateResourcesFromFlags(cmd *cobra.Command) (*json.RawMessage, error) {
	raw, _ := cmd.Flags().GetString("resources-json")
	file, _ := cmd.Flags().GetString("resources-file")
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read --resources-file: %w", err)
		}
		raw = string(data)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var parsed json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("invalid resource boundary JSON: %w", err)
	}
	if len(parsed) == 0 {
		return nil, errors.New("resource boundary JSON is empty")
	}
	return &parsed, nil
}

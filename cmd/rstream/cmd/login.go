// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"os"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

const loginAuthFlowOAuth = "oauth"
const loginAuthFlowLegacy = "legacy"

type loginResult struct {
	Authenticated bool                         `json:"authenticated"`
	APIURL        string                       `json:"apiUrl"`
	AuthFlow      string                       `json:"authFlow"`
	Identity      *controlplane.CallerIdentity `json:"identity,omitempty"`
}

var loginCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "login",
	Short:        "Login to rstream",
	Long:         "Authenticate using a browser-based login flow, or pass a token through stdin or a file.",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		token, tokenProvided, err := readTokenInputOptional(cmd)
		if err != nil {
			return err
		}
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		if tokenProvided {
			return completeLogin(cmd, path, cfg, apiURL, token, "token")
		}
		authFlow, err := cmd.Flags().GetString("auth-flow")
		if err != nil {
			return err
		}
		if authFlow == loginAuthFlowLegacy {
			return runLegacyDeviceLogin(cmd, path, cfg, apiURL)
		}
		if authFlow == loginAuthFlowOAuth {
			return runOAuthDeviceLogin(cmd, path, cfg, apiURL)
		}
		return &invalidLoginAuthFlowError{flow: authFlow}
	},
}

type invalidLoginAuthFlowError struct {
	flow string
}

func (e *invalidLoginAuthFlowError) Error() string {
	return "invalid auth flow: " + e.flow + " (expected oauth or legacy)"
}

func init() {
	loginCmd.Flags().SortFlags = false
	loginCmd.PersistentFlags().SortFlags = false
	loginCmd.Flags().Bool("token-stdin", false, "read token from stdin")
	loginCmd.Flags().String("token-file", "", "read token from file")
	loginCmd.Flags().String("token-storage", tokenStorageInline, "token storage backend: inline or macos-keychain")
	loginCmd.Flags().String("auth-flow", loginAuthFlowOAuth, "browser login flow: oauth or legacy")
	loginCmd.Flags().StringP("output", "o", "text", "output mode (text, json)")
	loginCmd.Flags().Bool("stdin", false, "read token from stdin (deprecated)")
	loginCmd.MarkFlagsMutuallyExclusive("token-stdin", "token-file", "stdin")
	loginCmd.MarkFlagFilename("token-file")
	loginCmd.Flags().MarkDeprecated("stdin", "use --token-stdin")
	rootCmd.AddCommand(loginCmd)
}

func completeLogin(cmd *cobra.Command, path string, cfg config.Config, apiURL, token, authFlow string) error {
	output, _ := cmd.Flags().GetString("output")
	if err := validateOutputMode(output, "text", "json"); err != nil {
		return err
	}
	identity, err := storeTokenFromFlags(cmd, path, cfg, apiURL, token)
	if err != nil {
		return err
	}
	return writeLoginResult(cmd, loginResult{Authenticated: true, APIURL: apiURL, AuthFlow: authFlow, Identity: &identity})
}

func writeLoginResult(cmd *cobra.Command, result loginResult) error {
	output, _ := cmd.Flags().GetString("output")
	switch output {
	case "text":
		fmt.Fprintln(os.Stdout, "Login successful.")
		if result.Identity != nil {
			owner := result.Identity.Owner
			if owner.Kind == "workspace" {
				fmt.Fprintf(os.Stdout, "Token owner: workspace %s\n", owner.WorkspaceID)
			} else {
				fmt.Fprintf(os.Stdout, "Token owner: user %s\n", owner.UserID)
			}
		}
		return nil
	case "json":
		return writeStructuredOutput("json", result)
	default:
		return validateOutputMode(output, "text", "json")
	}
}

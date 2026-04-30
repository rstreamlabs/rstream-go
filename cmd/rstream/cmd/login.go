// See LICENSE file in the project root for license information.

package cmd

import "github.com/spf13/cobra"

const loginAuthFlowOAuth = "oauth"
const loginAuthFlowLegacy = "legacy"

var loginCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "login [token]",
	Short:        "Login to rstream",
	Long:         "Authenticate using a token or complete a browser-based login flow.",
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		token, tokenProvided, err := readTokenInputOptional(cmd, args)
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
			return storeToken(cmd.Context(), path, cfg, apiURL, token)
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
	loginCmd.Flags().String("token", "", "authentication token")
	loginCmd.Flags().Bool("token-stdin", false, "read token from stdin")
	loginCmd.Flags().String("token-file", "", "read token from file")
	loginCmd.Flags().String("auth-flow", loginAuthFlowOAuth, "browser login flow: oauth or legacy")
	loginCmd.Flags().Bool("stdin", false, "read token from stdin (deprecated)")
	loginCmd.MarkFlagsMutuallyExclusive("token", "token-stdin", "token-file", "stdin")
	loginCmd.MarkFlagFilename("token-file")
	loginCmd.Flags().MarkDeprecated("stdin", "use --token-stdin")
	rootCmd.AddCommand(loginCmd)
}

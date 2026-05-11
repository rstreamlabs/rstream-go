// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "logout",
	Short:        "Logout of rstream",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		resolved, err := resolveLogoutTarget(cmd, cfg)
		if err != nil {
			return err
		}
		clearLogoutCredentials(&cfg, resolved)
		return config.WriteAtomic(path, cfg)
	},
}

func init() {
	logoutCmd.Flags().SortFlags = false
	logoutCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(logoutCmd)
}

func resolveLogoutTarget(cmd *cobra.Command, cfg config.Config) (config.Resolved, error) {
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagContext, _ := cmd.Flags().GetString("context")
	env := config.ReadEnv()
	return config.Resolve(config.ResolveInput{
		Config:      cfg,
		FlagAPIURL:  flagAPIURL,
		FlagContext: flagContext,
		EnvAPIURL:   env.APIURL,
		EnvContext:  env.Context,
	})
}

func clearLogoutCredentials(cfg *config.Config, resolved config.Resolved) {
	if env, _ := cfg.FindEnvironment(resolved.APIURL); env != nil {
		clearEnvironmentToken(env)
	}
	for i := range cfg.Contexts {
		if shouldClearContextAuth(cfg.Contexts[i], resolved) {
			cfg.Contexts[i].Auth = nil
		}
	}
}

func shouldClearContextAuth(ctx config.Context, resolved config.Resolved) bool {
	if ctx.APIURL != "" {
		return config.NormalizeAPIURL(ctx.APIURL) == config.NormalizeAPIURL(resolved.APIURL)
	}
	return resolved.Context != nil && resolved.Context.APIURL == "" && ctx.Name == resolved.ContextName
}

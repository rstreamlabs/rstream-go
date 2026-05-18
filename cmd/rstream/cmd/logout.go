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
		if err := clearLogoutCredentials(&cfg, resolved); err != nil {
			return err
		}
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

func clearLogoutCredentials(cfg *config.Config, resolved config.Resolved) error {
	if env, _ := cfg.FindEnvironment(resolved.APIURL); env != nil {
		if err := deleteAuthToken(env.Auth); err != nil {
			return err
		}
		clearEnvironmentToken(env)
	}
	for i := range cfg.Contexts {
		if shouldClearContextAuth(cfg.Contexts[i], resolved) {
			if err := deleteAuthToken(cfg.Contexts[i].Auth); err != nil {
				return err
			}
			cfg.Contexts[i].Auth = nil
		}
	}
	return nil
}

func shouldClearContextAuth(ctx config.Context, resolved config.Resolved) bool {
	if ctx.APIURL != "" {
		return config.NormalizeAPIURL(ctx.APIURL) == config.NormalizeAPIURL(resolved.APIURL)
	}
	return resolved.Context != nil && resolved.Context.APIURL == "" && ctx.Name == resolved.ContextName
}

func deleteAuthToken(auth *config.Auth) error {
	if auth == nil || auth.Token == nil || auth.Token.Storage == nil {
		return nil
	}
	return config.DeleteToken(*auth.Token.Storage)
}

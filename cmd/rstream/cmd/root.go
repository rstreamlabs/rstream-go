// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "rstream",
	Short:   "Powerful Tunnels for Modern Applications",
	Version: rstream.Version,
}

func init() {
	rootCmd.AddGroup(&cobra.Group{ID: "common", Title: "Common Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "management", Title: "Management Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "utils", Title: "Utility Commands:"})
	rootCmd.Flags().SortFlags = false
	rootCmd.PersistentFlags().SortFlags = false
	rootCmd.PersistentFlags().String("config", "", "path to rstream configuration file")
	rootCmd.PersistentFlags().StringP("log-level", "l", "info", "log level (debug, info, warn, error, fatal, panic)")
	rootCmd.PersistentFlags().Bool("version", false, "show version information and exit")
	rootCmd.PersistentFlags().String("token", "", "authentication token")
	rootCmd.PersistentFlags().Bool("no-token", false, "disable token-based authentication")
	rootCmd.MarkFlagsMutuallyExclusive("token", "no-token")
	rootCmd.PersistentFlags().String("engine", "", "engine URL (host:port)")
	rootCmd.PersistentFlags().String("tls-cert-file", "", "path to TLS cert PEM file (client)")
	rootCmd.PersistentFlags().String("tls-key-file", "", "path to TLS key PEM file (client)")
	rootCmd.MarkFlagsRequiredTogether("tls-cert-file", "tls-key-file")
	rootCmd.PersistentFlags().String("tls-cacert-file", "", "path to CA cert PEM file for verifying certificates (client)")
	rootCmd.PersistentFlags().String("local-addr", "", "use specific IP (e.g. 10.0.0.1)")
	rootCmd.PersistentFlags().String("network-interface", "", "use specific interface (e.g. eth0)")
	rootCmd.MarkFlagsMutuallyExclusive("local-addr", "network-interface")
	rootCmd.PersistentFlags().BoolP("force-ipv4", "4", false, "force using IPv4")
	rootCmd.PersistentFlags().BoolP("force-ipv6", "6", false, "force using IPv6")
	rootCmd.MarkFlagsMutuallyExclusive("force-ipv4", "force-ipv6")
	rootCmd.PersistentFlags().String("dns-override", "", "override DNS server (e.g. 8.8.8.8:53)")
	rootCmd.PersistentFlags().Bool("mptcp", false, "enable MPTCP support")
	rootCmd.PersistentFlags().String("proxy-http", "", "HTTP proxy (http[s]://host[:port]) for CONNECT")
	rootCmd.PersistentFlags().String("proxy-username", "", "proxy username")
	rootCmd.PersistentFlags().String("proxy-password", "", "proxy password")
	rootCmd.MarkFlagsRequiredTogether("proxy-username", "proxy-password")
	rootCmd.Flags().StringArray("proxy-http-headers", nil, "set proxy HTTP headers (key=value, might be specified multiple times)")
	rootCmd.PersistentFlags().String("proxy-tls-cert-file", "", "path to TLS cert PEM file (proxy)")
	rootCmd.PersistentFlags().String("proxy-tls-key-file", "", "path to TLS key PEM file (proxy)")
	rootCmd.MarkFlagsRequiredTogether("proxy-tls-cert-file", "proxy-tls-key-file")
	rootCmd.PersistentFlags().String("proxy-tls-cacert-file", "", "path to CA cert PEM file for verifying certificates (proxy)")
	origHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		origHelp(cmd, args)
		fmt.Fprintln(cmd.OutOrStdout(), "\nEnvironment Variables:")
		fmt.Fprintln(cmd.OutOrStdout(), "  RSTREAM_DEFAULT_CONFIG_PATH           path to the default rstream configuration folder")
		fmt.Fprintln(cmd.OutOrStdout(), "  RSTREAM_DEFAULT_AUTHENTICATION_TOKEN  default authentication token")
		fmt.Fprintln(cmd.OutOrStdout(), "  RSTREAM_DEFAULT_ENGINE                default engine URL")
		fmt.Fprintln(cmd.OutOrStdout(), "\nFurther information can be found in the rstream documentation (https://rstream.io/docs)")
	})
}

func ExecuteContext(ctx context.Context) {
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

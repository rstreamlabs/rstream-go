// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/cmd/logging"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var rootCmd = &cobra.Command{
	Use:     "rstream",
	Short:   "Powerful Tunnels for Modern Applications",
	Version: rstream.Version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initLogger(cmd)
	},
}

var (
	flagLogLevel  string // debug, info, warn, error, none
	flagLogFormat string // auto, json, text, pretty
)

func init() {
	rootCmd.AddGroup(&cobra.Group{ID: "common", Title: "Common Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "management", Title: "Management Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "utils", Title: "Utility Commands:"})
	rootCmd.Flags().SortFlags = false
	rootCmd.PersistentFlags().SortFlags = false
	rootCmd.PersistentFlags().String("config", "", "path to rstream configuration file")
	rootCmd.PersistentFlags().StringVarP(&flagLogLevel, "log-level", "l", "info", "log level (debug, info, warn, error, none)")
	rootCmd.PersistentFlags().StringVarP(&flagLogFormat, "log-format", "f", "auto", "log format (auto, json, text, pretty)")
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
}

func initLogger(cmd *cobra.Command) error {
	level, _ := cmd.Flags().GetString("log-level")
	format, _ := cmd.Flags().GetString("log-format")
	formatChanged := cmd.Flags().Changed("log-format")
	levelChanged := cmd.Flags().Changed("log-level")
	if format == "auto" {
		if term.IsTerminal(int(os.Stdout.Fd())) {
			format = "pretty"
		} else {
			format = "text"
		}
	}
	if format == "pretty" && !term.IsTerminal(int(os.Stdout.Fd())) && formatChanged {
		return fmt.Errorf("pretty log format requires a TTY on stdout")
	}
	logger := logging.New(logging.Config{
		Level:  level,
		Format: format,
		Output: os.Stdout,
	})
	slog.SetDefault(logger)
	_ = levelChanged
	return nil
}

func ExecuteContext(ctx context.Context) {
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

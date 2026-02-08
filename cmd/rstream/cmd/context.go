// See LICENSE file in the project root for license information.

package cmd

import "github.com/spf13/cobra"

var contextCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "context",
	Aliases:      []string{"ctx"},
	Short:        "Manage contexts",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var contextListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List contexts",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

var contextGetCmd = &cobra.Command{
	Use:          "get <name>",
	Short:        "Get context details",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

var contextUseCmd = &cobra.Command{
	Use:          "use <name>",
	Short:        "Set the default context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

var contextDeleteCmd = &cobra.Command{
	Use:          "delete <name>",
	Aliases:      []string{"rm"},
	Short:        "Delete a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

var contextCreateCmd = &cobra.Command{
	Use:          "create <name>",
	Short:        "Create a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

var contextUpdateCmd = &cobra.Command{
	Use:          "update <name>",
	Short:        "Update a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

func init() {
	contextCmd.Flags().SortFlags = false
	contextCmd.PersistentFlags().SortFlags = false
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextGetCmd)
	contextCmd.AddCommand(contextUseCmd)
	contextCmd.AddCommand(contextDeleteCmd)
	contextCmd.AddCommand(contextCreateCmd)
	contextCmd.AddCommand(contextUpdateCmd)
	contextListCmd.Flags().SortFlags = false
	contextListCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	contextGetCmd.Flags().SortFlags = false
	contextGetCmd.Flags().StringP("output", "o", "yaml", "output mode (yaml, json)")
	contextCreateCmd.Flags().SortFlags = false
	contextUpdateCmd.Flags().SortFlags = false
	addContextTransportFlags(contextCreateCmd)
	addContextTransportFlags(contextUpdateCmd)
	contextCreateCmd.Flags().Bool("default", false, "set created context as default (currentContext)")
	contextCreateCmd.Flags().String("project-endpoint", "", "associate a project endpoint with this context (optional)")
	contextUpdateCmd.Flags().String("project-endpoint", "", "associate a project endpoint with this context (optional)")
	rootCmd.AddCommand(contextCmd)
}

func addContextTransportFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("token", "t", "", "authentication token")
	cmd.Flags().Bool("no-token", false, "disable token-based authentication")
	cmd.MarkFlagsMutuallyExclusive("token", "no-token")
	cmd.Flags().String("engine", "", "engine URL (host:port)")
	cmd.Flags().String("tls-cert-file", "", "path to TLS cert PEM file (client)")
	cmd.Flags().String("tls-key-file", "", "path to TLS key PEM file (client)")
	cmd.MarkFlagsRequiredTogether("tls-cert-file", "tls-key-file")
	cmd.Flags().String("tls-cacert-file", "", "path to CA cert PEM file for verifying certificates (client)")
	cmd.Flags().String("local-addr", "", "use specific IP (e.g. 10.0.0.1)")
	cmd.Flags().String("network-interface", "", "use specific interface (e.g. eth0)")
	cmd.MarkFlagsMutuallyExclusive("local-addr", "network-interface")
	cmd.Flags().BoolP("force-ipv4", "4", false, "force using IPv4")
	cmd.Flags().BoolP("force-ipv6", "6", false, "force using IPv6")
	cmd.MarkFlagsMutuallyExclusive("force-ipv4", "force-ipv6")
	cmd.Flags().String("dns-override", "", "override DNS server (e.g. 8.8.8.8:53)")
	cmd.Flags().Bool("mptcp", false, "enable MPTCP support")
	cmd.Flags().String("proxy-http", "", "HTTP proxy (http[s]://host[:port]) for CONNECT")
	cmd.Flags().String("proxy-username", "", "proxy username")
	cmd.Flags().String("proxy-password", "", "proxy password")
	cmd.MarkFlagsRequiredTogether("proxy-username", "proxy-password")
	cmd.Flags().StringArray("proxy-http-headers", nil, "set proxy HTTP headers (key=value, might be specified multiple times)")
	cmd.Flags().String("proxy-tls-cert-file", "", "path to TLS cert PEM file (proxy)")
	cmd.Flags().String("proxy-tls-key-file", "", "path to TLS key PEM file (proxy)")
	cmd.MarkFlagsRequiredTogether("proxy-tls-cert-file", "proxy-tls-key-file")
	cmd.Flags().String("proxy-tls-cacert-file", "", "path to CA cert PEM file for verifying certificates (proxy)")
}

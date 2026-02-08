// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/config"
	"github.com/spf13/cobra"
)

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
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env, _ := cfg.FindEnvironment(apiURL)
		contexts := []config.Context{}
		if env != nil {
			contexts = append(contexts, env.Contexts...)
		}
		sort.SliceStable(contexts, func(i, j int) bool {
			return contexts[i].Name < contexts[j].Name
		})
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "table":
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tENGINE\tPROJECT ENDPOINT")
			for _, ctx := range contexts {
				engine := ctx.Engine
				project := ctx.ProjectEndpoint
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", ctx.Name, engine, project)
			}
			return w.Flush()
		case "json", "yaml":
			redacted := make([]config.Context, 0, len(contexts))
			for _, ctx := range contexts {
				redacted = append(redacted, redactContext(ctx))
			}
			return writeStructuredOutput(output, redacted)
		default:
			return validateOutputMode(output, "table", "json", "yaml")
		}
	},
}

var contextGetCmd = &cobra.Command{
	Use:          "get <name>",
	Short:        "Get context details",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env, _ := cfg.FindEnvironment(apiURL)
		if env == nil {
			return fmt.Errorf("no environment found for apiUrl %q", apiURL)
		}
		ctx, _ := env.FindContext(args[0])
		if ctx == nil {
			return fmt.Errorf("context %q not found", args[0])
		}
		output, _ := cmd.Flags().GetString("output")
		if output == "json" || output == "yaml" {
			return writeStructuredOutput(output, redactContext(*ctx))
		}
		return validateOutputMode(output, "json", "yaml")
	},
}

var contextUseCmd = &cobra.Command{
	Use:          "use <name>",
	Short:        "Set the default context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env, _ := cfg.FindEnvironment(apiURL)
		if env == nil {
			return fmt.Errorf("no environment found for apiUrl %q", apiURL)
		}
		ctx, _ := env.FindContext(args[0])
		if ctx == nil {
			return fmt.Errorf("context %q not found", args[0])
		}
		cfg.Defaults.Context = &config.DefaultContext{APIURL: apiURL, Name: ctx.Name}
		return config.WriteAtomic(path, cfg)
	},
}

var contextDeleteCmd = &cobra.Command{
	Use:          "delete <name>",
	Aliases:      []string{"rm"},
	Short:        "Delete a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env, _ := cfg.FindEnvironment(apiURL)
		if env == nil {
			return fmt.Errorf("no environment found for apiUrl %q", apiURL)
		}
		_, idx := env.FindContext(args[0])
		if idx < 0 {
			return fmt.Errorf("context %q not found", args[0])
		}
		env.Contexts = append(env.Contexts[:idx], env.Contexts[idx+1:]...)
		if cfg.Defaults.Context != nil && cfg.Defaults.Context.APIURL == apiURL && cfg.Defaults.Context.Name == args[0] {
			cfg.Defaults.Context = nil
		}
		return config.WriteAtomic(path, cfg)
	},
}

var contextCreateCmd = &cobra.Command{
	Use:          "create <name>",
	Short:        "Create a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env := cfg.EnsureEnvironment(apiURL)
		if existing, _ := env.FindContext(args[0]); existing != nil {
			return fmt.Errorf("context %q already exists", args[0])
		}
		engine, _ := cmd.Flags().GetString("engine")
		projectEndpoint, _ := cmd.Flags().GetString("project-endpoint")
		if engine == "" {
			if projectEndpoint != "" {
				return errors.New("project endpoint lookup is not implemented; use --engine")
			}
			return errors.New("--engine is required")
		}
		newCtx := config.Context{
			Name:            args[0],
			Engine:          engine,
			ProjectEndpoint: projectEndpoint,
		}
		transport, err := transportFromFlags(cmd)
		if err != nil {
			return err
		}
		if transport != nil {
			newCtx.Transport = transport
		}
		token, ok, err := readTokenFromFlags(cmd)
		if err != nil {
			return err
		}
		if ok {
			if strings.TrimSpace(token) == "" {
				return errors.New("token is empty")
			}
			setContextToken(&newCtx, token)
		}
		env.Contexts = append(env.Contexts, newCtx)
		if setDefault, _ := cmd.Flags().GetBool("default"); setDefault {
			cfg.Defaults.Context = &config.DefaultContext{APIURL: apiURL, Name: newCtx.Name}
		}
		return config.WriteAtomic(path, cfg)
	},
}

var contextUpdateCmd = &cobra.Command{
	Use:          "update <name>",
	Short:        "Update a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env, _ := cfg.FindEnvironment(apiURL)
		if env == nil {
			return fmt.Errorf("no environment found for apiUrl %q", apiURL)
		}
		ctx, _ := env.FindContext(args[0])
		if ctx == nil {
			return fmt.Errorf("context %q not found", args[0])
		}
		if cmd.Flags().Changed("engine") {
			engine, _ := cmd.Flags().GetString("engine")
			if engine != "" {
				ctx.Engine = engine
			}
		}
		if cmd.Flags().Changed("project-endpoint") {
			endpoint, _ := cmd.Flags().GetString("project-endpoint")
			if endpoint != "" {
				ctx.ProjectEndpoint = endpoint
			}
		}
		token, ok, err := readTokenFromFlags(cmd)
		if err != nil {
			return err
		}
		if ok {
			if strings.TrimSpace(token) == "" {
				return errors.New("token is empty")
			}
			setContextToken(ctx, token)
		}
		transport, err := transportFromFlags(cmd)
		if err != nil {
			return err
		}
		if transport != nil {
			ctx.Transport = config.MergeTransport(ctx.Transport, transport)
		}
		return config.WriteAtomic(path, cfg)
	},
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
	contextCreateCmd.Flags().Bool("default", false, "set created context as default")
	contextCreateCmd.Flags().String("project-endpoint", "", "associate a project endpoint with this context (optional)")
	contextUpdateCmd.Flags().String("project-endpoint", "", "associate a project endpoint with this context (optional)")
	rootCmd.AddCommand(contextCmd)
}

func addContextTransportFlags(cmd *cobra.Command) {
	cmd.Flags().String("token", "", "authentication token")
	cmd.Flags().Bool("token-stdin", false, "read token from stdin")
	cmd.Flags().String("token-file", "", "read token from file")
	cmd.MarkFlagsMutuallyExclusive("token", "token-stdin", "token-file")
	cmd.Flags().String("engine", "", "engine URL (host:port)")
	cmd.Flags().String("bind-address", "", "bind to a specific local address")
	cmd.Flags().String("bind-interface", "", "bind to a specific network interface")
	cmd.MarkFlagsMutuallyExclusive("bind-address", "bind-interface")
	cmd.Flags().String("ip-family", "", "force IP family (ipv4, ipv6)")
	cmd.Flags().String("dns-override", "", "override DNS server (e.g. 8.8.8.8:53)")
	cmd.Flags().Bool("mptcp", false, "enable MPTCP support")
	cmd.Flags().String("proxy-http", "", "HTTP proxy (http[s]://host[:port]) for CONNECT")
	cmd.Flags().String("proxy-username", "", "proxy username")
	cmd.Flags().String("proxy-password", "", "proxy password")
	cmd.MarkFlagsRequiredTogether("proxy-username", "proxy-password")
	cmd.Flags().StringArray("proxy-http-header", nil, "set proxy HTTP headers (key=value, might be specified multiple times)")
}

func transportFromFlags(cmd *cobra.Command) (*config.TransportConfig, error) {
	var cfg config.TransportConfig
	set := false
	bindAddress, _ := cmd.Flags().GetString("bind-address")
	bindInterface, _ := cmd.Flags().GetString("bind-interface")
	if bindAddress != "" || bindInterface != "" {
		cfg.Bind = &config.BindConfig{}
		switch {
		case bindInterface != "":
			cfg.Bind.Mode = "interface"
			cfg.Bind.Interface = bindInterface
		case bindAddress != "":
			cfg.Bind.Mode = "address"
			cfg.Bind.Address = bindAddress
		}
		set = true
	}
	ipFamily, _ := cmd.Flags().GetString("ip-family")
	if ipFamily != "" {
		cfg.IPFamily = ipFamily
		set = true
	}
	dnsOverride, _ := cmd.Flags().GetString("dns-override")
	if dnsOverride != "" {
		cfg.DNS = &config.DNSConfig{Override: dnsOverride}
		set = true
	}
	if cmd.Flags().Changed("mptcp") {
		val, _ := cmd.Flags().GetBool("mptcp")
		cfg.MPTCP = &val
		set = true
	}
	proxyHTTP, _ := cmd.Flags().GetString("proxy-http")
	proxyUsername, _ := cmd.Flags().GetString("proxy-username")
	proxyPassword, _ := cmd.Flags().GetString("proxy-password")
	proxyHeaders := getStringArrayMap(cmd, "proxy-http-header")
	if proxyHTTP != "" || proxyUsername != "" || proxyPassword != "" || len(proxyHeaders) > 0 {
		cfg.Proxy = &config.ProxyConfig{}
		cfg.Proxy.HTTP = proxyHTTP
		cfg.Proxy.Username = proxyUsername
		cfg.Proxy.Password = proxyPassword
		if len(proxyHeaders) > 0 {
			cfg.Proxy.Headers = proxyHeaders
		}
		set = true
	}
	if !set {
		return nil, nil
	}
	return &cfg, nil
}

func setContextToken(ctx *config.Context, token string) {
	if ctx.Auth == nil {
		ctx.Auth = &config.Auth{}
	}
	ctx.Auth.Token = &config.Token{Storage: &config.TokenStorage{Kind: config.TokenStorageInline, Value: token}}
}

func redactContext(ctx config.Context) config.Context {
	ctx.Auth = nil
	return ctx
}

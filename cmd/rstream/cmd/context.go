// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
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
		contexts := append([]config.Context(nil), cfg.Contexts...)
		sort.SliceStable(contexts, func(i, j int) bool {
			if contexts[i].Name == contexts[j].Name {
				return contexts[i].APIURL < contexts[j].APIURL
			}
			return contexts[i].Name < contexts[j].Name
		})
		defaultCtx := cfg.Defaults.Context
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "table":
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "DEFAULT\tNAME\tAPI URL\tENGINE\tPROJECT ENDPOINT\tREGION")
			for _, ctx := range contexts {
				engine := ctx.Engine
				project := ctx.ProjectEndpoint
				apiURL := ctx.APIURL
				if apiURL == "" {
					apiURL = "-"
				}
				isDefault := ""
				if defaultCtx != nil && defaultCtx.Name == ctx.Name {
					isDefault = "*"
				}
				region := ctx.Region
				if region == "" {
					region = "auto"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", isDefault, ctx.Name, apiURL, engine, project, region)
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
		ctx, _, err := selectContext(cmd, cfg, args[0])
		if err != nil {
			return err
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
		ctx, _, err := selectContext(cmd, cfg, args[0])
		if err != nil {
			return err
		}
		cfg.Defaults.Context = &config.DefaultContext{Name: ctx.Name}
		if err := config.WriteAtomic(path, cfg); err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		return writeOptionalStructuredOutput(output, map[string]any{
			"context": redactContext(*ctx),
			"default": true,
		})
	},
}

var contextDeleteCmd = &cobra.Command{
	Use:          "delete <name>",
	Aliases:      []string{"rm", "del"},
	Short:        "Delete a context",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		ctx, idx, err := selectContext(cmd, cfg, args[0])
		if err != nil {
			return err
		}
		deleted := *ctx
		if err := deleteAuthToken(deleted.Auth); err != nil {
			return err
		}
		cfg.Contexts = append(cfg.Contexts[:idx], cfg.Contexts[idx+1:]...)
		if cfg.Defaults.Context != nil && cfg.Defaults.Context.Name == deleted.Name {
			cfg.Defaults.Context = nil
		}
		if err := config.WriteAtomic(path, cfg); err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		return writeOptionalStructuredOutput(output, map[string]any{
			"deleted": true,
			"context": redactContext(deleted),
		})
	},
}

var contextCreateCmd = newContextCreateCommand()

func newContextCreateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "create <name>", Short: "Create a context", SilenceUsage: true, Args: cobra.ExactArgs(1), RunE: runContextCreate}
	cmd.Flags().SortFlags = false
	cmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	addContextTransportFlags(cmd)
	cmd.Flags().Bool("default", false, "set created context as default")
	cmd.Flags().String("project-endpoint", "", "associate a project endpoint with this context (optional)")
	cmd.Flags().Bool("no-api-url", false, "do not associate this context with a Control plane API URL")
	return cmd
}

func runContextCreate(cmd *cobra.Command, args []string) error {
	path, cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	selection, err := resolveContextSelection(cmd, cfg)
	if err != nil {
		return err
	}
	apiURL := ""
	if selection.useAPIURL {
		apiURL = selection.apiURL
	}
	if existing, _, err := cfg.FindContextByName(args[0]); err != nil {
		return err
	} else if existing != nil {
		return fmt.Errorf("context %q already exists", args[0])
	}
	engine, _ := cmd.Flags().GetString("engine")
	projectEndpoint, _ := cmd.Flags().GetString("project-endpoint")
	regionValue, _ := cmd.Flags().GetString("region")
	region, err := controlplane.NormalizeRegion(regionValue)
	if err != nil {
		return err
	}
	if engine != "" && region != "" {
		return errors.New("region selection cannot be combined with an explicit engine override")
	}
	token, tokenProvided, err := readTokenFromFlags(cmd)
	if err != nil {
		return err
	}
	if tokenProvided && strings.TrimSpace(token) == "" {
		return errors.New("token is empty")
	}
	newCtx := config.Context{Name: args[0], APIURL: apiURL, Engine: engine, ProjectEndpoint: projectEndpoint, Region: region}
	if engine == "" {
		if projectEndpoint == "" {
			return errors.New("--engine is required")
		}
		if selection.unlinked || apiURL == "" {
			return errors.New("project endpoint lookup requires --api-url and authentication")
		}
		lookupToken := token
		if !tokenProvided {
			lookupToken, err = resolveControlPlaneToken(cfg, apiURL)
			if err != nil {
				return err
			}
		}
		environment, _ := cfg.FindEnvironment(apiURL)
		headers, err := config.ResolveControlPlaneHeaders(environment, config.ReadEnv().ControlPlaneHeaders)
		if err != nil {
			return err
		}
		client := controlplane.NewClient(apiURL, lookupToken, controlplane.WithHeaders(headers))
		if err := client.RequireToken(); err != nil {
			return err
		}
		project, err := client.ResolveProjectByEndpoint(cmd.Context(), projectEndpoint)
		if err != nil {
			return mapControlPlaneError(err)
		}
		if _, err := project.EngineAddressForRegion(region); err != nil {
			return err
		}
		newCtx.ProjectEndpoint = project.Endpoint
		newCtx.Engine = project.EngineAddress()
		newCtx.TURNDomain = projectTURNDomain(project)
		newCtx.TURNRealm = strings.TrimSpace(project.TurnRealm)
		newCtx.TURNPort = project.TurnPort
		newCtx.TURNSPort = project.TurnsPort
	}
	transport, err := transportFromFlags(cmd)
	if err != nil {
		return err
	}
	if transport != nil {
		newCtx.Transport = transport
	}
	if tokenProvided {
		if err := setContextTokenFromFlags(cmd, &newCtx, token, newCtx.APIURL); err != nil {
			return err
		}
	} else if cmd.Flags().Changed("token-storage") {
		return errors.New("--token-storage requires --token, --token-stdin, or --token-file")
	}
	cfg.Contexts = append(cfg.Contexts, newCtx)
	if setDefault, _ := cmd.Flags().GetBool("default"); setDefault {
		cfg.Defaults.Context = &config.DefaultContext{Name: newCtx.Name}
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	return writeOptionalStructuredOutput(output, map[string]any{"context": redactContext(newCtx), "default": cfg.Defaults.Context != nil && cfg.Defaults.Context.Name == newCtx.Name})
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
		apiURLFlagSet := cmd.Flags().Changed("api-url")
		apiURLValue, _ := cmd.Flags().GetString("api-url")
		apiURLValue = config.NormalizeAPIURL(apiURLValue)
		noAPIURL, _ := cmd.Flags().GetBool("no-api-url")
		if apiURLFlagSet && noAPIURL {
			return errors.New("cannot use --api-url with --no-api-url")
		}
		applyAPIURL := false
		var ctx *config.Context
		var idx int
		switch {
		case apiURLFlagSet && apiURLValue != "":
			ctx, idx, err = cfg.FindContextByName(args[0])
			if err != nil {
				return err
			}
			if ctx == nil {
				return fmt.Errorf("context %q not found", args[0])
			}
			applyAPIURL = true
		case apiURLFlagSet && apiURLValue == "":
			ctx, idx, err = cfg.FindContextByName(args[0])
			if err != nil {
				return err
			}
			if ctx == nil {
				return fmt.Errorf("context %q not found", args[0])
			}
		case noAPIURL:
			ctx, idx, err = cfg.FindContextByName(args[0])
			if err != nil {
				return err
			}
			if ctx == nil {
				return fmt.Errorf("context %q not found", args[0])
			}
		default:
			ctx, idx, err = selectContext(cmd, cfg, args[0])
			if err != nil {
				return err
			}
		}
		_ = idx
		regionChanged := cmd.Flags().Changed("region")
		if regionChanged {
			value, _ := cmd.Flags().GetString("region")
			region, err := controlplane.NormalizeRegion(value)
			if err != nil {
				return err
			}
			ctx.Region = region
		}
		if cmd.Flags().Changed("engine") {
			engine, _ := cmd.Flags().GetString("engine")
			if engine != "" && ctx.Region != "" {
				return errors.New("region selection cannot be combined with an explicit engine override")
			}
			if engine != "" {
				ctx.Engine = engine
			}
		}
		projectEndpointChanged := cmd.Flags().Changed("project-endpoint")
		if projectEndpointChanged {
			endpoint, _ := cmd.Flags().GetString("project-endpoint")
			if endpoint != "" {
				ctx.ProjectEndpoint = endpoint
			}
		}
		tokenAPIURL := ctx.APIURL
		if noAPIURL || (apiURLFlagSet && apiURLValue == "") {
			tokenAPIURL = ""
		} else if applyAPIURL {
			tokenAPIURL = apiURLValue
		}
		token, ok, err := readTokenFromFlags(cmd)
		if err != nil {
			return err
		}
		if ok {
			if strings.TrimSpace(token) == "" {
				return errors.New("token is empty")
			}
			if err := setContextTokenFromFlags(cmd, ctx, token, tokenAPIURL); err != nil {
				return err
			}
		} else if cmd.Flags().Changed("token-storage") {
			return errors.New("--token-storage requires --token, --token-stdin, or --token-file")
		}
		if noAPIURL || (apiURLFlagSet && apiURLValue == "") {
			ctx.APIURL = ""
		} else if applyAPIURL {
			ctx.APIURL = apiURLValue
		}
		if regionChanged || projectEndpointChanged {
			if err := refreshContextProject(cmd, cfg, ctx); err != nil {
				return err
			}
		}
		transport, err := transportFromFlags(cmd)
		if err != nil {
			return err
		}
		if transport != nil {
			ctx.Transport = config.MergeTransport(ctx.Transport, transport)
		}
		if err := config.WriteAtomic(path, cfg); err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		return writeOptionalStructuredOutput(output, map[string]any{
			"context": redactContext(*ctx),
			"default": cfg.Defaults.Context != nil && cfg.Defaults.Context.Name == ctx.Name,
		})
	},
}

func refreshContextProject(cmd *cobra.Command, cfg config.Config, ctx *config.Context) error {
	if ctx.ProjectEndpoint == "" {
		if ctx.Region != "" {
			return errors.New("managed project endpoint is required for region selection")
		}
		return nil
	}
	apiURL := config.NormalizeAPIURL(ctx.APIURL)
	if apiURL == "" {
		return errors.New("project endpoint lookup requires an API URL")
	}
	token, err := resolveControlPlaneToken(cfg, apiURL)
	if err != nil {
		return err
	}
	environment, _ := cfg.FindEnvironment(apiURL)
	headers, err := config.ResolveControlPlaneHeaders(environment, config.ReadEnv().ControlPlaneHeaders)
	if err != nil {
		return err
	}
	client := controlplane.NewClient(apiURL, token, controlplane.WithHeaders(headers))
	if err := client.RequireToken(); err != nil {
		return err
	}
	project, err := client.ResolveProjectByEndpoint(cmd.Context(), ctx.ProjectEndpoint)
	if err != nil {
		return mapControlPlaneError(err)
	}
	if _, err := project.EngineAddressForRegion(ctx.Region); err != nil {
		return err
	}
	ctx.ProjectEndpoint = project.Endpoint
	ctx.Engine = project.EngineAddress()
	ctx.TURNDomain = projectTURNDomain(project)
	ctx.TURNRealm = strings.TrimSpace(project.TurnRealm)
	ctx.TURNPort = project.TurnPort
	ctx.TURNSPort = project.TurnsPort
	return nil
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
	contextUseCmd.Flags().SortFlags = false
	contextUseCmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	contextDeleteCmd.Flags().SortFlags = false
	contextDeleteCmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	contextUpdateCmd.Flags().SortFlags = false
	contextUpdateCmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	addContextTransportFlags(contextUpdateCmd)
	contextUpdateCmd.Flags().String("project-endpoint", "", "associate a project endpoint with this context (optional)")
	contextGetCmd.Flags().Bool("no-api-url", false, "select an unlinked context")
	contextUseCmd.Flags().Bool("no-api-url", false, "select an unlinked context")
	contextDeleteCmd.Flags().Bool("no-api-url", false, "select an unlinked context")
	contextUpdateCmd.Flags().Bool("no-api-url", false, "clear the context API URL association")
	rootCmd.AddCommand(contextCmd)
}

func addContextTransportFlags(cmd *cobra.Command) {
	cmd.Flags().String("token", "", "authentication token")
	cmd.Flags().Bool("token-stdin", false, "read token from stdin")
	cmd.Flags().String("token-file", "", "read token from file")
	cmd.Flags().String("token-storage", tokenStorageInline, "token storage backend: inline or macos-keychain")
	cmd.MarkFlagsMutuallyExclusive("token", "token-stdin", "token-file")
	cmd.Flags().String("engine", "", "engine URL (host:port)")
	cmd.Flags().String("bind-address", "", "bind to a specific local address")
	cmd.Flags().String("bind-interface", "", "bind to a specific network interface")
	cmd.MarkFlagsMutuallyExclusive("bind-address", "bind-interface")
	cmd.Flags().String("ip-family", "", "force IP family (ipv4, ipv6)")
	cmd.Flags().String("dns-override", "", "override DNS server (e.g. 8.8.8.8:53)")
	cmd.Flags().Bool("mptcp", false, "enable MPTCP support")
	cmd.Flags().String("engine-tls-ca-file", "", "PEM CA bundle used to verify Engine TLS")
	cmd.Flags().String("engine-tls-server-name", "", "TLS server name used when verifying Engine TLS")
	cmd.Flags().Bool("engine-tls-insecure-skip-verify", false, "skip Engine TLS verification")
	cmd.Flags().String("proxy-http", "", "HTTP proxy (http[s]://host[:port]) for CONNECT")
	cmd.Flags().String("proxy-socks5", "", "SOCKS5 proxy (socks5://host[:port])")
	cmd.Flags().String("proxy-username", "", "proxy username")
	cmd.Flags().String("proxy-password", "", "proxy password")
	cmd.MarkFlagsRequiredTogether("proxy-username", "proxy-password")
	cmd.MarkFlagsMutuallyExclusive("proxy-http", "proxy-socks5")
	cmd.Flags().StringArray("proxy-http-header", nil, "set proxy HTTP headers (key=value, might be specified multiple times)")
	cmd.Flags().String("proxy-tls-ca-file", "", "PEM CA bundle used to verify HTTPS or MASQUE proxy TLS")
	cmd.Flags().String("proxy-tls-server-name", "", "TLS server name used when verifying HTTPS or MASQUE proxy TLS")
	cmd.Flags().Bool("proxy-tls-insecure-skip-verify", false, "skip HTTPS or MASQUE proxy TLS verification")
	cmd.Flags().Bool("proxy-from-environment", false, "read HTTP_PROXY, HTTPS_PROXY, ALL_PROXY, and NO_PROXY when no proxy is configured")
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
	engineTLSCAFile, _ := cmd.Flags().GetString("engine-tls-ca-file")
	engineTLSServerName, _ := cmd.Flags().GetString("engine-tls-server-name")
	engineTLSInsecureChanged := cmd.Flags().Changed("engine-tls-insecure-skip-verify")
	engineTLSInsecure, _ := cmd.Flags().GetBool("engine-tls-insecure-skip-verify")
	engineTLSSet := engineTLSCAFile != "" || engineTLSServerName != "" || engineTLSInsecureChanged
	if engineTLSSet {
		cfg.TLS = &config.TLSConfig{
			CAFile:     engineTLSCAFile,
			ServerName: engineTLSServerName,
		}
		if engineTLSInsecureChanged {
			cfg.TLS.InsecureSkipVerify = rstream.BoolPtr(engineTLSInsecure)
		}
		set = true
	}
	proxyHTTP, _ := cmd.Flags().GetString("proxy-http")
	proxySOCKS5, _ := cmd.Flags().GetString("proxy-socks5")
	proxyUsername, _ := cmd.Flags().GetString("proxy-username")
	proxyPassword, _ := cmd.Flags().GetString("proxy-password")
	proxyFromEnvironmentChanged := cmd.Flags().Changed("proxy-from-environment")
	proxyFromEnvironment, _ := cmd.Flags().GetBool("proxy-from-environment")
	proxyHeaders := getStringArrayMap(cmd, "proxy-http-header")
	proxyTLSCAFile, _ := cmd.Flags().GetString("proxy-tls-ca-file")
	proxyTLSServerName, _ := cmd.Flags().GetString("proxy-tls-server-name")
	proxyTLSInsecureChanged := cmd.Flags().Changed("proxy-tls-insecure-skip-verify")
	proxyTLSInsecure, _ := cmd.Flags().GetBool("proxy-tls-insecure-skip-verify")
	proxyTLSSet := proxyTLSCAFile != "" || proxyTLSServerName != "" || proxyTLSInsecureChanged
	if proxySOCKS5 != "" && len(proxyHeaders) > 0 {
		return nil, fmt.Errorf("--proxy-http-header can only be used with --proxy-http")
	}
	if proxySOCKS5 != "" && proxyTLSSet {
		return nil, fmt.Errorf("--proxy-tls-* flags can only be used with --proxy-http or --proxy-from-environment")
	}
	if proxyTLSSet && proxyHTTP == "" && !proxyFromEnvironment {
		return nil, fmt.Errorf("--proxy-tls-* flags require --proxy-http or --proxy-from-environment=true")
	}
	if proxyHTTP != "" || proxySOCKS5 != "" || proxyUsername != "" || proxyPassword != "" || len(proxyHeaders) > 0 || proxyTLSSet || proxyFromEnvironmentChanged {
		cfg.Proxy = &config.ProxyConfig{}
		cfg.Proxy.HTTP = proxyHTTP
		cfg.Proxy.SOCKS5 = proxySOCKS5
		cfg.Proxy.Username = proxyUsername
		cfg.Proxy.Password = proxyPassword
		if proxyFromEnvironmentChanged {
			cfg.Proxy.FromEnvironment = rstream.BoolPtr(proxyFromEnvironment)
		}
		if len(proxyHeaders) > 0 {
			cfg.Proxy.Headers = proxyHeaders
		}
		if proxyTLSSet {
			cfg.Proxy.TLS = &config.ProxyTLSConfig{
				CAFile:     proxyTLSCAFile,
				ServerName: proxyTLSServerName,
			}
			if proxyTLSInsecureChanged {
				cfg.Proxy.TLS.InsecureSkipVerify = rstream.BoolPtr(proxyTLSInsecure)
			}
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

func setContextTokenStorage(ctx *config.Context, storage config.TokenStorage) {
	if ctx.Auth == nil {
		ctx.Auth = &config.Auth{}
	}
	ctx.Auth.Token = &config.Token{Storage: &storage}
}

func setContextTokenFromFlags(cmd *cobra.Command, ctx *config.Context, token, apiURL string) error {
	storage, err := contextTokenStorageFromFlags(cmd, ctx.Name, apiURL)
	if err != nil {
		return err
	}
	switch storage.Kind {
	case config.TokenStorageInline:
		setContextToken(ctx, token)
	case config.TokenStorageKeychain:
		if err := storeTokenInCredentialProvider(storage, token); err != nil {
			return err
		}
		setContextTokenStorage(ctx, storage)
	default:
		return fmt.Errorf("token storage kind %q is not supported", storage.Kind)
	}
	return nil
}

func contextTokenStorageFromFlags(cmd *cobra.Command, name, apiURL string) (config.TokenStorage, error) {
	storage, _ := cmd.Flags().GetString("token-storage")
	switch storage {
	case "", tokenStorageInline:
		return config.TokenStorage{Kind: config.TokenStorageInline}, nil
	case tokenStorageMacOSKeychain:
		ref := config.TokenStorage{
			Kind:     config.TokenStorageKeychain,
			Provider: config.CredentialProviderMacOS,
			Service:  config.DefaultMacOSKeychainTokenService,
			Account:  config.DefaultMacOSKeychainContextTokenAccount(name, apiURL),
		}
		return ref, nil
	default:
		return config.TokenStorage{}, fmt.Errorf("invalid --token-storage %q (valid: inline, macos-keychain)", storage)
	}
}

func redactContext(ctx config.Context) config.Context {
	ctx.Auth = nil
	if ctx.Transport != nil {
		ctx.Transport = config.MergeTransport(nil, ctx.Transport)
		if ctx.Transport.Proxy != nil {
			if ctx.Transport.Proxy.Password != "" {
				ctx.Transport.Proxy.Password = redactedValue
			}
			for key := range ctx.Transport.Proxy.Headers {
				if isSensitiveHeader(key) {
					ctx.Transport.Proxy.Headers[key] = redactedValue
				}
			}
		}
	}
	return ctx
}

const redactedValue = "<redacted>"

func isSensitiveHeader(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "x-auth-key", "x-auth-token", "x-csrf-token":
		return true
	default:
		return strings.Contains(key, "auth") ||
			strings.Contains(key, "credential") ||
			strings.Contains(key, "jwt") ||
			strings.Contains(key, "key") ||
			strings.Contains(key, "oidc") ||
			strings.Contains(key, "password") ||
			strings.Contains(key, "saml") ||
			strings.Contains(key, "secret") ||
			strings.Contains(key, "session") ||
			strings.Contains(key, "token")
	}
}

type contextSelection struct {
	apiURL    string
	useAPIURL bool
	unlinked  bool
}

func resolveContextSelection(cmd *cobra.Command, cfg config.Config) (contextSelection, error) {
	noAPIURL, _ := cmd.Flags().GetBool("no-api-url")
	if cmd.Flags().Changed("api-url") && noAPIURL {
		return contextSelection{}, errors.New("cannot use --api-url with --no-api-url")
	}
	if cmd.Flags().Changed("api-url") {
		value, _ := cmd.Flags().GetString("api-url")
		value = config.NormalizeAPIURL(value)
		if value == "" {
			return contextSelection{unlinked: true}, nil
		}
		return contextSelection{apiURL: value, useAPIURL: true}, nil
	}
	if noAPIURL {
		return contextSelection{unlinked: true}, nil
	}
	if env := config.ReadEnv().APIURL; env != "" {
		return contextSelection{apiURL: env, useAPIURL: true}, nil
	}
	_ = cfg
	return contextSelection{}, nil
}

func selectContext(cmd *cobra.Command, cfg config.Config, name string) (*config.Context, int, error) {
	selection, err := resolveContextSelection(cmd, cfg)
	if err != nil {
		return nil, -1, err
	}
	switch {
	case selection.useAPIURL:
		ctx, idx, err := cfg.FindContextByNameAndAPIURL(name, selection.apiURL)
		if err != nil {
			return nil, -1, err
		}
		if ctx == nil {
			return nil, -1, fmt.Errorf("context %q not found for API URL %q", name, selection.apiURL)
		}
		return ctx, idx, nil
	case selection.unlinked:
		ctx, idx, err := cfg.FindContextUnlinked(name)
		if err != nil {
			return nil, -1, err
		}
		if ctx == nil {
			return nil, -1, fmt.Errorf("context %q not found", name)
		}
		return ctx, idx, nil
	default:
		ctx, idx, err := cfg.FindContextByName(name)
		if err != nil {
			return nil, -1, err
		}
		if ctx == nil {
			return nil, -1, fmt.Errorf("context %q not found", name)
		}
		return ctx, idx, nil
	}
}

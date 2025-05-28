// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

type forwardOptions struct {
	Listener rstream.Listener
	Host     string
	Port     string
}

var forwardCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "forward [[host:]port]",
	Short:        "Forward traffic through rstream tunnel",
	Example:      `  rstream forward 8080`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := "localhost"
		port := "8080"
		if len(args) == 1 {
			hostPort := strings.SplitN(args[0], ":", 2)
			if len(hostPort) == 2 {
				host = hostPort[0]
				port = hostPort[1]
			} else {
				port = hostPort[0]
			}
		}
		opts, err := newForwardOptions(cmd, host, port)
		if err != nil {
			return err
		}
		return runForward(cmd.Context(), opts)
	},
}

func init() {
	forwardCmd.Flags().SortFlags = false
	forwardCmd.PersistentFlags().SortFlags = false
	forwardCmd.Flags().String("name", "", "tunnel name")
	forwardCmd.Flags().Bool("publish", false, "publish the tunnel")
	forwardCmd.Flags().Bool("no-publish", false, "do not publish the tunnel")
	forwardCmd.MarkFlagsMutuallyExclusive("publish", "no-publish")
	forwardCmd.Flags().Bool("http", false, "use HTTP protocol")
	forwardCmd.Flags().Bool("tls", false, "use TLS protocol")
	forwardCmd.MarkFlagsMutuallyExclusive("http", "tls")
	forwardCmd.Flags().StringArray("label", nil, "set tunnel labels (key=value, might be specified multiple times)")
	forwardCmd.Flags().String("geoip", "", "comma-separated allowed countries (ISO 3166-1 alpha-2)")
	forwardCmd.Flags().String("trusted-ips", "", "comma-separated allowed IP/CIDR ranges")
	forwardCmd.Flags().String("domain", "", "domain name for publishing")
	forwardCmd.Flags().String("tls-mode", "", "TLS mode (terminated, passthrough)")
	forwardCmd.Flags().String("tls-alpn", "", "comma-separated ALPN protocols")
	forwardCmd.Flags().String("tls-min-version", "", "minimum TLS version (tls1.2, tls1.3)")
	forwardCmd.Flags().String("tls-ciphers", "", "comma-separated TLS ciphers")
	forwardCmd.Flags().Bool("mtls", false, "enable mutual TLS authentication")
	forwardCmd.Flags().String("mtls-cacert-file", "", "path to CA cert PEM file for mTLS")
	forwardCmd.MarkFlagsRequiredTogether("mtls", "mtls-cacert-file")
	forwardCmd.Flags().String("http-version", "", "HTTP version (http/1.1, h2c)")
	forwardCmd.Flags().Bool("http-use-tls", false, "use TLS for upstream")
	forwardCmd.Flags().Bool("token-auth", false, "enable token-based HTTP authentication")
	forwardCmd.Flags().Bool("sso", false, "enable SSO authentication")
	forwardCmd.Flags().String("sso-provider", "", "comma-separated SSO providers (google, github, ...)")
	forwardCmd.MarkFlagsRequiredTogether("sso", "sso-provider")
	forwardCmd.Flags().String("email-whitelist", "", "comma-separated email list allowed for SSO")
	forwardCmd.Flags().String("email-blacklist", "", "comma-separated email list denied for SSO")
	forwardCmd.Flags().Bool("challenge", false, "enable challenge / captcha")
	forwardCmd.Flags().Bool("retry", false, "enable automatic reconnection on disconnect")
	forwardCmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	forwardCmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	forwardCmd.Flags().Int64("retry-interval", 0, "retry interval in ms")
	rootCmd.AddCommand(forwardCmd)
}

func newForwardOptions(cmd *cobra.Command, host, port string) (*forwardOptions, error) {
	client, err := newClientFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	tunnelProperties, err := newTunnelPropertiesFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	retryPtr := getBoolPtr(cmd, "retry")
	noRetryPtr := getBoolPtr(cmd, "no-retry")
	var autoReconnectPtr *bool
	switch {
	case retryPtr != nil && *retryPtr:
		autoReconnectPtr = rstream.BoolPtr(true)
	case noRetryPtr != nil && *noRetryPtr:
		autoReconnectPtr = rstream.BoolPtr(false)
	}
	retryIntervalMsPtr := getInt64Ptr(cmd, "retry-interval")
	var reconnectTimeoutPtr *time.Duration
	if retryIntervalMsPtr != nil {
		ri := time.Duration(*retryIntervalMsPtr) * time.Millisecond
		reconnectTimeoutPtr = &ri
	}
	opts := &forwardOptions{
		Listener: rstream.Listener{
			Client:           client,
			TunnelProperties: tunnelProperties,
			AutoReconnect:    autoReconnectPtr,
			ReconnectTimeout: reconnectTimeoutPtr,
		},
		Host: host,
		Port: port,
	}
	return opts, nil
}

func runForward(ctx context.Context, opts *forwardOptions) error {
	defer opts.Listener.Close()
	go func() {
		<-ctx.Done()
		_ = opts.Listener.Close()
	}()
	for {
		conn, err := opts.Listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return err
		}
		go func(inbound net.Conn) {
			defer inbound.Close()
			outbound, err := net.Dial("tcp", net.JoinHostPort(opts.Host, opts.Port))
			if err != nil {
				fmt.Printf("Dial error to %s:%s: %v\n", opts.Host, opts.Port, err)
			} else {
				defer outbound.Close()
				go io.Copy(outbound, inbound)
				io.Copy(inbound, outbound)
			}
		}(conn)
	}
}

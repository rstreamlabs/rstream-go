// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

type forwardOutputFormat string

const (
	forwardOutputFormatHuman       forwardOutputFormat = "human"
	forwardOutputFormatHumanPretty forwardOutputFormat = "human-pretty"
	forwardOutputFormatJSON        forwardOutputFormat = "json"
	forwardOutputFormatJSONPretty  forwardOutputFormat = "json-pretty"
)

type forwardStatus struct {
	Status     *string
	TunnelID   *string
	Forwarding *string
	Forwarded  *string
}

type forwardConnInfo struct {
	Active   bool
	Date     time.Time
	StreamID *string
	SourceIP net.IP
}

type forwardCtx struct {
	Client           *rstream.Client
	Props            *rstream.TunnelProperties
	Host             string
	Port             string
	AutoReconnect    *bool
	ReconnectTimeout *time.Duration
	Verbose          bool
	Format           forwardOutputFormat
	UI               forwardUI
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
		s, err := newForwardCtx(cmd, host, port)
		if err != nil {
			return err
		}
		if s.UI != nil {
			uidone := s.UI.Start(cmd.Context())
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			go func() { <-uidone; cancel() }()
			err = s.run(ctx)
		} else {
			err = s.run(cmd.Context())
		}
		if err != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

func init() {
	forwardCmd.Flags().SortFlags = false
	forwardCmd.PersistentFlags().SortFlags = false
	forwardCmd.Flags().BoolP("verbose", "v", false, "enable verbose mode")
	forwardCmd.Flags().StringP("format", "f", "", "output format (human, human-pretty, json, json-pretty)")
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
	forwardCmd.Flags().Bool("retry", true, "enable automatic reconnection on disconnect")
	forwardCmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	forwardCmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	forwardCmd.Flags().Int64("retry-interval", 5000, "retry interval in ms")
	rootCmd.AddCommand(forwardCmd)
}

func newForwardCtx(cmd *cobra.Command, host, port string) (*forwardCtx, error) {
	client, err := newClientFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	props, err := newTunnelPropertiesFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	retryPtr := getBoolPtr(cmd, "retry")
	noRetryPtr := getBoolPtr(cmd, "no-retry")
	var autoReconnect *bool
	switch {
	case retryPtr != nil && *retryPtr:
		autoReconnect = rstream.BoolPtr(true)
	case noRetryPtr != nil && *noRetryPtr:
		autoReconnect = rstream.BoolPtr(false)
	}
	if autoReconnect == nil {
		autoReconnect = rstream.BoolPtr(true)
	}
	var reconnectTimeout *time.Duration
	{
		v, _ := cmd.Flags().GetInt64("retry-interval")
		d := time.Duration(v) * time.Millisecond
		reconnectTimeout = &d
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	fstr, _ := cmd.Flags().GetString("format")
	var fmtOut forwardOutputFormat
	switch fstr {
	case "human", "human-pretty", "json", "json-pretty":
		fmtOut = forwardOutputFormat(fstr)
	case "":
		if stdoutIsTTY() && !verbose {
			fmtOut = forwardOutputFormatHumanPretty
		} else {
			fmtOut = forwardOutputFormatHuman
		}
	default:
		return nil, fmt.Errorf("invalid format: %s", fstr)
	}
	if fmtOut == forwardOutputFormatHumanPretty && verbose {
		return nil, fmt.Errorf("human-pretty cannot be used with verbose")
	}
	if fmtOut == forwardOutputFormatHumanPretty && !stdoutIsTTY() {
		return nil, fmt.Errorf("human-pretty requires a TTY")
	}
	var ui forwardUI
	if fmtOut == forwardOutputFormatHumanPretty {
		ui, err = newForwardUITCell()
		if err != nil {
			return nil, err
		}
	}
	return &forwardCtx{
		Client: client, Props: props, Host: host, Port: port,
		AutoReconnect: autoReconnect, ReconnectTimeout: reconnectTimeout,
		Verbose: verbose, Format: fmtOut, UI: ui,
	}, nil
}

func (s *forwardCtx) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.runOnce(ctx)
		s.setStatus(forwardStatus{
			Status: rstream.StringPtr("disconnected"),
		})
		if err == nil {
			return nil
		}
		if s.AutoReconnect != nil && !*s.AutoReconnect {
			return err
		}
		if s.AutoReconnect == nil {
			return err
		}
		select {
		case <-time.After(*s.ReconnectTimeout):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *forwardCtx) runOnce(ctx context.Context) error {
	ctrl, err := s.Client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	s.setStatus(forwardStatus{
		Status: rstream.StringPtr("connecting"),
	})
	tunnel, err := ctrl.CreateTunnel(ctx, *s.Props)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	listener, ok := tunnel.(interface{ net.Listener })
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	props, err := tunnel.Properties()
	if err != nil {
		return fmt.Errorf("failed to get tunnel properties: %w", err)
	}
	forwarding, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	forwarded, err := rstream.FormatForwardedHostPort(s.Host, s.Port, props)
	if err != nil {
		return fmt.Errorf("failed to format forwarded address: %w", err)
	}
	if s.Verbose {
		fmt.Printf("Forwarding %s to %s\n", forwarding, forwarded)
	}
	s.setStatus(forwardStatus{
		Status:     rstream.StringPtr("online"),
		TunnelID:   props.ID,
		Forwarding: &forwarding,
		Forwarded:  &forwarded,
	})
	errCh := make(chan error, 1)
	go func() { errCh <- s.serveTCP(listener) }()
	select {
	case <-ctx.Done():
		_ = listener.Close()
		<-errCh
		return context.Canceled
	case err := <-errCh:
		return err
	}
}

func (s *forwardCtx) serveTCP(l net.Listener) error {
	for {
		inbound, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		var streamID *string
		var sourceIP net.IP
		if laddr := inbound.LocalAddr().(*rstream.Addr); laddr != nil {
			streamID = &laddr.IdOrName
			sourceIP = laddr.SourceIP
		}
		idx := s.addConn(forwardConnInfo{Active: true, Date: time.Now(), StreamID: streamID, SourceIP: sourceIP})
		go func(inbound net.Conn, idx *int) {
			defer func() {
				if idx != nil {
					s.closeConn(*idx)
				}
				inbound.Close()
			}()
			outbound, err := net.Dial("tcp", net.JoinHostPort(s.Host, s.Port))
			if err != nil {
				if s.Verbose {
					fmt.Printf("Dial error to %s:%s: %v\n", s.Host, s.Port, err)
				}
				return
			}
			defer outbound.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(outbound, inbound); done <- struct{}{} }()
			go func() { _, _ = io.Copy(inbound, outbound); done <- struct{}{} }()
			<-done
		}(inbound, idx)
	}
}

func (s *forwardCtx) setStatus(status forwardStatus) {
	if s.UI != nil {
		s.UI.SetStatus(status)
	}
}

func (s *forwardCtx) addConn(ci forwardConnInfo) *int {
	if s.UI != nil {
		idx := s.UI.AddConn(ci)
		return &idx
	}
	return nil
}

func (s *forwardCtx) closeConn(idx int) {
	if s.UI != nil {
		s.UI.CloseConn(idx)
	}
}

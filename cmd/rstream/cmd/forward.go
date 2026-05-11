// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/cmd/logging"
	"github.com/spf13/cobra"
)

type forwardOutputFormat string

const (
	forwardOutputFormatText  forwardOutputFormat = "text"
	forwardOutputFormatJSON  forwardOutputFormat = "json"
	forwardOutputFormatXTerm forwardOutputFormat = "xterm"
	forwardOutputFormatNone  forwardOutputFormat = "none"
)

type forwardStatus struct {
	Version    *string `json:"version,omitempty"`
	Update     *string `json:"update,omitempty"`
	Plan       *string `json:"plan,omitempty"`
	Provider   *string `json:"provider,omitempty"`
	Region     *string `json:"region,omitempty"`
	Status     *string `json:"status,omitempty"`
	TunnelID   *string `json:"tunnel_id,omitempty"`
	Forwarding *string `json:"forwarding,omitempty"`
	Forwarded  *string `json:"forwarded,omitempty"`
}

type forwardConnInfo struct {
	Active   bool      `json:"active"`
	Date     time.Time `json:"date"`
	StreamID *string   `json:"stream_id,omitempty"`
	SourceIP *net.IP   `json:"source_ip,omitempty"`
}

type forwardCtx struct {
	Client           *rstream.Client
	Props            *rstream.TunnelProperties
	Host             string
	Port             string
	AutoReconnect    *bool
	ReconnectTimeout *time.Duration
	Logger           *slog.Logger
	OutputFormat     forwardOutputFormat
	Out              io.Writer
	UI               forwardUI
}

type statusReportedError struct {
	err error
}

func (e statusReportedError) Error() string {
	return e.err.Error()
}

func (e statusReportedError) Unwrap() error {
	return e.err
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
	forwardCmd.Flags().StringP("output", "o", "", "output mode (text, json, xterm, none)")
	forwardCmd.Flags().String("name", "", "tunnel name")
	forwardCmd.Flags().Bool("bytestream", false, "create a bytestream tunnel")
	forwardCmd.Flags().Bool("datagram", false, "create a datagram tunnel")
	forwardCmd.MarkFlagsMutuallyExclusive("bytestream", "datagram")
	forwardCmd.Flags().Bool("publish", false, "publish the tunnel")
	forwardCmd.Flags().Bool("no-publish", false, "do not publish the tunnel")
	forwardCmd.MarkFlagsMutuallyExclusive("publish", "no-publish")
	forwardCmd.Flags().Bool("tls", false, "use TLS protocol")
	forwardCmd.Flags().Bool("dtls", false, "use DTLS protocol")
	forwardCmd.Flags().Bool("quic", false, "use QUIC protocol")
	forwardCmd.Flags().Bool("http", false, "use HTTP protocol")
	forwardCmd.MarkFlagsMutuallyExclusive("tls", "dtls", "quic", "http")
	forwardCmd.Flags().StringArray("label", nil, "set tunnel labels (key=value, might be specified multiple times)")
	forwardCmd.Flags().String("geoip", "", "comma-separated allowed countries (ISO 3166-1 alpha-2)")
	forwardCmd.Flags().String("trusted-ips", "", "comma-separated allowed IP/CIDR ranges")
	forwardCmd.Flags().String("host", "", "Stable domain for publishing")
	forwardCmd.Flags().String("tls-mode", "", "TLS mode (terminated, passthrough)")
	forwardCmd.Flags().String("tls-alpn", "", "comma-separated ALPN protocols")
	forwardCmd.Flags().String("tls-min-version", "", "minimum TLS version (tls1.2, tls1.3)")
	forwardCmd.Flags().String("tls-ciphers", "", "comma-separated TLS ciphers")
	forwardCmd.Flags().Bool("mtls", false, "enable mutual TLS authentication")
	forwardCmd.Flags().String("mtls-cacert-file", "", "path to CA cert PEM file for mTLS")
	forwardCmd.MarkFlagsRequiredTogether("mtls", "mtls-cacert-file")
	forwardCmd.Flags().String("http-version", "", "HTTP version (http/1.1, h2c, h3)")
	forwardCmd.Flags().Bool("upstream-tls", false, "use TLS for the upstream side")
	forwardCmd.Flags().Bool("http-use-tls", false, "use TLS for HTTP upstream (deprecated; use --upstream-tls)")
	forwardCmd.Flags().Bool("token-auth", false, "enable token-based HTTP authentication")
	forwardCmd.Flags().Bool("rstream-auth", false, "require rstream account authentication (HTTP only)")
	forwardCmd.Flags().Bool("challenge-mode", false, "require an interactive challenge before access (HTTP only)")
	forwardCmd.Flags().Bool("retry", true, "enable automatic reconnection on disconnect")
	forwardCmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	forwardCmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	forwardCmd.Flags().Int64("retry-interval", 5000, "retry interval in ms")
	rootCmd.AddCommand(forwardCmd)
}

func newForwardCtx(cmd *cobra.Command, host, port string) (*forwardCtx, error) {
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return nil, err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return nil, err
	}
	props, err := newTunnelPropertiesFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	if err := rstream.MaybeSetGeneratedStableDomain(props, runtime.Resolved.Engine); err != nil {
		return nil, fmt.Errorf("failed to generate stable domain: %w", err)
	}
	retryPtr := getBoolPtr(cmd, "retry")
	noRetryPtr := getBoolPtr(cmd, "no-retry")
	var autoReconnect *bool
	switch {
	case retryPtr != nil:
		autoReconnect = rstream.BoolPtr(*retryPtr)
	case noRetryPtr != nil && *noRetryPtr:
		autoReconnect = rstream.BoolPtr(false)
	}
	if autoReconnect == nil {
		autoReconnect = rstream.BoolPtr(true)
	}
	var reconnectTimeout *time.Duration
	{
		v, _ := cmd.Flags().GetInt64("retry-interval")
		if v <= 0 {
			return nil, fmt.Errorf("--retry-interval must be greater than 0")
		}
		d := time.Duration(v) * time.Millisecond
		reconnectTimeout = &d
	}
	outStr, _ := cmd.Flags().GetString("output")
	var out forwardOutputFormat
	switch outStr {
	case "text":
		out = forwardOutputFormatText
	case "json":
		out = forwardOutputFormatJSON
	case "xterm":
		out = forwardOutputFormatXTerm
	case "none":
		out = forwardOutputFormatNone
	case "":
		if logging.IsTerminal(os.Stdout) && !flagVerbose {
			out = forwardOutputFormatXTerm
		} else {
			out = forwardOutputFormatText
		}
	default:
		return nil, fmt.Errorf("invalid output: %s (valid: text, json, xterm)", outStr)
	}
	if out == forwardOutputFormatXTerm {
		if logging.IsTerminal(os.Stdout) == false {
			return nil, fmt.Errorf("output mode 'xterm' requires a terminal")
		} else if flagVerbose == true {
			return nil, fmt.Errorf("output mode 'xterm' is not compatible with verbose mode")
		}
	}
	var ui forwardUI
	if out == forwardOutputFormatXTerm {
		ui, err = newForwardUITCell()
		if err != nil {
			return nil, err
		}
	}
	return &forwardCtx{
		Client:           client,
		Props:            props,
		Host:             host,
		Port:             port,
		AutoReconnect:    autoReconnect,
		ReconnectTimeout: reconnectTimeout,
		Logger:           slog.With("cmd", "forward"),
		OutputFormat:     out,
		Out:              os.Stdout,
		UI:               ui,
	}, nil
}

func formatVersion(version, channel string) string {
	ch := strings.TrimSpace(channel)
	if ch != "" && !strings.EqualFold(ch, "stable") {
		return fmt.Sprintf("%s (%s)", version, ch)
	}
	return version
}

func newForwardStatus(details *rstream.ServerDetails) forwardStatus {
	v := formatVersion(rstream.Version, rstream.Channel)
	status := forwardStatus{
		Version: &v,
	}
	if details != nil {
		status.Update = details.Update
		status.Plan = details.Plan
		status.Provider = details.Provider
		status.Region = details.Region
	}
	return status
}

func (s *forwardCtx) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.runOnce(ctx)
		var reported statusReportedError
		if err != nil && !errors.As(err, &reported) {
			status := newForwardStatus(nil)
			status.Status = rstream.StringPtr("disconnected")
			s.setStatus(status)
		}
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
	connectingStatus := newForwardStatus(nil)
	connectingStatus.Status = rstream.StringPtr("connecting")
	s.setStatus(connectingStatus)
	ctrl, err := s.Client.Connect(ctx, nil)
	if err != nil {
		status := newForwardStatus(nil)
		status.Status = rstream.StringPtr(formatStatusError("connection failed", err))
		s.setStatus(status)
		return statusReportedError{err: fmt.Errorf("failed to connect to rstream engine server: %w", err)}
	}
	defer ctrl.Close()
	baseStatus := newForwardStatus(ctrl.ServerDetails())
	connectedStatus := baseStatus
	connectedStatus.Status = rstream.StringPtr("connected")
	s.setStatus(connectedStatus)
	tunnel, err := ctrl.CreateTunnel(ctx, *s.Props)
	if err != nil {
		status := baseStatus
		status.Status = rstream.StringPtr(formatStatusError("tunnel creation failed", err))
		s.setStatus(status)
		return statusReportedError{err: fmt.Errorf("failed to create tunnel: %w", err)}
	}
	defer tunnel.Close()
	props, err := tunnel.Properties()
	if err != nil {
		status := baseStatus
		status.Status = rstream.StringPtr(formatStatusError("tunnel creation failed", err))
		s.setStatus(status)
		return statusReportedError{err: fmt.Errorf("failed to get tunnel properties: %w", err)}
	}
	forwarding, err := tunnel.ForwardingAddress()
	if err != nil {
		status := baseStatus
		status.Status = rstream.StringPtr(formatStatusError("tunnel creation failed", err))
		s.setStatus(status)
		return statusReportedError{err: fmt.Errorf("failed to get forwarding address: %w", err)}
	}
	forwarded, err := rstream.FormatForwardedHostPort(s.Host, s.Port, props)
	if err != nil {
		status := baseStatus
		status.Status = rstream.StringPtr(formatStatusError("tunnel creation failed", err))
		s.setStatus(status)
		return statusReportedError{err: fmt.Errorf("failed to format forwarded address: %w", err)}
	}
	onlineStatus := baseStatus
	onlineStatus.Status = rstream.StringPtr("online")
	onlineStatus.TunnelID = props.ID
	onlineStatus.Forwarding = &forwarding
	onlineStatus.Forwarded = &forwarded
	s.setStatus(onlineStatus)
	if l, ok := tunnel.(interface{ net.Listener }); ok {
		return s.serveWithCtx(ctx, l.Close, func() error { return s.serveTCP(l) })
	}
	if pl, ok := tunnel.(rstream.PacketListener); ok {
		return s.serveWithCtx(ctx, pl.Close, func() error { return s.serveUDP(pl) })
	}
	return fmt.Errorf("tunnel does not implement net.Listener or rstream.PacketListener")
}

func formatStatusError(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return prefix
	}
	return fmt.Sprintf("%s (%s)", prefix, msg)
}

func (s *forwardCtx) serveWithCtx(ctx context.Context, closeFn func() error, fn func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()
	select {
	case <-ctx.Done():
		_ = closeFn()
		<-errCh
		return context.Canceled
	case err := <-errCh:
		return err
	}
}

func (s *forwardCtx) withTrackedConn(addr net.Addr, run func()) {
	var streamID *string
	var sourceIP *net.IP
	if ra, ok := addr.(*rstream.Addr); ok && ra != nil {
		streamID = &ra.IdOrName
		sourceIP = &ra.SourceIP
	}
	idx := s.addConn(forwardConnInfo{Active: true, Date: time.Now(), StreamID: streamID, SourceIP: sourceIP})
	go func() {
		defer func() {
			if idx != nil {
				s.closeConn(*idx)
			}
		}()
		run()
	}()
}

func (s *forwardCtx) proxyTCP(inbound net.Conn) {
	s.withTrackedConn(inbound.LocalAddr(), func() {
		defer inbound.Close()
		outbound, err := net.Dial("tcp", net.JoinHostPort(s.Host, s.Port))
		if err != nil {
			s.Logger.Error("Dial error", slog.String("host", s.Host), slog.String("port", s.Port), slog.String("error", err.Error()))
		} else {
			defer outbound.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(outbound, inbound); done <- struct{}{} }()
			go func() { _, _ = io.Copy(inbound, outbound); done <- struct{}{} }()
			<-done
		}
	})
}

func (s *forwardCtx) proxyUDP(inbound net.PacketConn, remote net.Addr) {
	s.withTrackedConn(inbound.LocalAddr(), func() {
		defer inbound.Close()
		udpRaddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(s.Host, s.Port))
		if err != nil {
			s.Logger.Error("ResolveUDPAddr error", slog.String("host", s.Host), slog.String("port", s.Port), slog.String("error", err.Error()))
			return
		}
		outbound, err := net.DialUDP("udp", nil, udpRaddr)
		if err != nil {
			s.Logger.Error("DialUDP error", slog.String("host", s.Host), slog.String("port", s.Port), slog.String("error", err.Error()))
			return
		}
		defer outbound.Close()
		done := make(chan struct{}, 2)
		go func() {
			buf := make([]byte, 65535)
			for {
				n, _, err := inbound.ReadFrom(buf)
				if err != nil {
					break
				}
				if _, err := outbound.Write(buf[:n]); err != nil {
					break
				}
			}
			done <- struct{}{}
		}()
		go func() {
			buf := make([]byte, 65535)
			for {
				n, err := outbound.Read(buf)
				if err != nil {
					break
				}
				if _, err := inbound.WriteTo(buf[:n], remote); err != nil {
					break
				}
			}
			done <- struct{}{}
		}()
		<-done
	})
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
		s.proxyTCP(inbound)
	}
}

func (s *forwardCtx) serveUDP(l rstream.PacketListener) error {
	for {
		inbound, raddr, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		s.proxyUDP(inbound, raddr)
	}
}

func (s *forwardCtx) setStatus(status forwardStatus) {
	{
		args := []any{}
		if status.Status != nil {
			args = append(args, slog.String("status", *status.Status))
		}
		if status.TunnelID != nil {
			args = append(args, slog.String("tunnel_id", *status.TunnelID))
		}
		if status.Forwarding != nil {
			args = append(args, slog.String("forwarding", *status.Forwarding))
		}
		if status.Forwarded != nil {
			args = append(args, slog.String("forwarded", *status.Forwarded))
		}
		s.Logger.Debug("Status update", args...)
	}
	switch s.OutputFormat {
	case forwardOutputFormatText:
		s.renderStatusText(status)
	case forwardOutputFormatJSON:
		s.writeJSON(status)
	case forwardOutputFormatXTerm:
		if s.UI != nil {
			s.UI.SetStatus(status)
		}
	case forwardOutputFormatNone:
	}
}

func (s *forwardCtx) addConn(ci forwardConnInfo) *int {
	{
		args := []any{
			slog.Bool("active", ci.Active),
			slog.String("date", ci.Date.Format(time.RFC3339)),
		}
		if ci.StreamID != nil {
			args = append(args, slog.String("stream_id", *ci.StreamID))
		}
		if ci.SourceIP != nil {
			args = append(args, slog.String("source_ip", ci.SourceIP.String()))
		}
		s.Logger.Debug("New connection", args...)
	}
	switch s.OutputFormat {
	case forwardOutputFormatText:
		streamID := "-"
		if ci.StreamID != nil {
			streamID = *ci.StreamID
		}
		sourceIP := "-"
		if ci.SourceIP != nil {
			sourceIP = ci.SourceIP.String()
		}
		s.writef("incoming connection: date=%s stream_id=%s source_ip=%s active=%t\n",
			ci.Date.UTC().Format("2006-01-02 15:04:05.000 UTC"), streamID, sourceIP, ci.Active)
	case forwardOutputFormatJSON:
		s.writeJSON(ci)
	case forwardOutputFormatXTerm:
		if s.UI != nil {
			idx := s.UI.AddConn(ci)
			return &idx
		}
	case forwardOutputFormatNone:
	}
	return nil
}

func (s *forwardCtx) closeConn(idx int) {
	s.Logger.Debug("Connection closed", slog.Int("idx", idx))
	switch s.OutputFormat {
	case forwardOutputFormatText:
		s.writef("connection closed: idx=%d\n", idx)
	case forwardOutputFormatJSON:
		s.writeJSON(map[string]any{
			"event": "connection_closed",
			"idx":   idx,
		})
	case forwardOutputFormatXTerm:
		if s.UI != nil {
			s.UI.CloseConn(idx)
		}
	case forwardOutputFormatNone:
	}
}

func (s *forwardCtx) writeLine(a ...any) {
	if s.Out == nil {
		return
	}
	fmt.Fprintln(s.Out, a...)
}

func (s *forwardCtx) writef(format string, a ...any) {
	if s.Out == nil {
		return
	}
	fmt.Fprintf(s.Out, format, a...)
}

func (s *forwardCtx) writeJSON(v any) {
	if s.Out == nil {
		return
	}
	enc := json.NewEncoder(s.Out)
	_ = enc.Encode(v)
}

func (s *forwardCtx) renderStatusText(st forwardStatus) {
	type kv struct{ k, v string }
	val := func(p *string) string {
		if p == nil || strings.TrimSpace(*p) == "" {
			return "-"
		}
		return *p
	}
	lines := []kv{
		{"version", val(st.Version)},
		{"update", val(st.Update)},
		{"plan", val(st.Plan)},
		{"provider", val(st.Provider)},
		{"region", val(st.Region)},
		{"status", val(st.Status)},
		{"tunnel ID", val(st.TunnelID)},
		{"forwarding", val(st.Forwarding)},
		{"forwarded", val(st.Forwarded)},
	}
	maxw := 0
	for _, kv := range lines {
		if len(kv.k) > maxw {
			maxw = len(kv.k)
		}
	}
	s.writeLine("tunnel status")
	for _, kv := range lines {
		s.writef("  %-*s : %s\n", maxw, kv.k, kv.v)
	}
}

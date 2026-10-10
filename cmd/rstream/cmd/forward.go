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
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/cmd/logging"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/netretry"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/sessiongroup"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/streamrelay"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/fileserver"
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
	Files      *fileserver.Info `json:"files,omitempty"`
	Version    *string          `json:"version,omitempty"`
	Update     *string          `json:"update,omitempty"`
	Plan       *string          `json:"plan,omitempty"`
	Provider   *string          `json:"provider,omitempty"`
	Region     *string          `json:"region,omitempty"`
	Status     *string          `json:"status,omitempty"`
	TunnelID   *string          `json:"tunnel_id,omitempty"`
	Forwarding *string          `json:"forwarding,omitempty"`
	Forwarded  *string          `json:"forwarded,omitempty"`
}

type forwardConnInfo struct {
	Active   bool      `json:"active"`
	Date     time.Time `json:"date"`
	StreamID *string   `json:"stream_id,omitempty"`
	SourceIP *net.IP   `json:"source_ip,omitempty"`
}

type filesActivityEvent struct {
	Event      string    `json:"event"`
	Date       time.Time `json:"date"`
	Backend    string    `json:"backend"`
	Operation  string    `json:"operation"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	Bytes      int64     `json:"bytes"`
	DurationMS int64     `json:"duration_ms"`
	Outcome    string    `json:"outcome"`
}

type forwardCtx struct {
	LocalHTTP        *localHTTPService
	Client           *rstream.Client
	Props            *rstream.TunnelProperties
	Host             string
	Port             string
	AutoReconnect    *bool
	ReconnectTimeout *time.Duration
	Logger           *slog.Logger
	OutputFormat     forwardOutputFormat
	Out              io.Writer
	outMu            sync.Mutex
	UI               forwardUI
	clientCloser     *ownedRstreamClient
	resolveProject   func(context.Context) (controlplane.Project, error)
}

type forwardSessionGroup = sessiongroup.Group

func newForwardSessionGroup(ctx context.Context) *forwardSessionGroup {
	return sessiongroup.New(ctx)
}

func (s *forwardCtx) Close() error {
	if s == nil {
		return nil
	}
	return s.clientCloser.Close()
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
			target, err := runmodel.ParseForwardTarget(args[0], host)
			if err != nil {
				return err
			}
			host, port = target.Host, target.Port
		}
		s, err := newForwardCtx(cmd, host, port)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := s.Close(); closeErr != nil {
				s.Logger.Warn("failed to close forward client", "error", closeErr)
			}
		}()
		err = runForwardWithUI(cmd.Context(), s.UI, s.run)
		if err != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

func runForwardWithUI(ctx context.Context, ui forwardUI, run func(context.Context) error) (err error) {
	if ui == nil {
		return run(ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		if stopErr := ui.Stop(); stopErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to stop forward UI: %w", stopErr))
		}
	}()
	uidone := ui.Start(runCtx)
	go func() {
		select {
		case <-uidone:
			cancel()
		case <-runCtx.Done():
		}
	}()
	return run(runCtx)
}

func init() {
	addForwardFlags(forwardCmd)
	rootCmd.AddCommand(forwardCmd)
}

func addForwardFlags(cmd *cobra.Command) {
	cmd.Flags().SortFlags = false
	cmd.PersistentFlags().SortFlags = false
	cmd.Flags().StringP("output", "o", "", "output mode (text, json, xterm, none)")
	cmd.Flags().String("name", "", "tunnel name")
	cmd.Flags().Bool("bytestream", false, "create a bytestream tunnel")
	cmd.Flags().Bool("datagram", false, "create a raw private datagram tunnel")
	cmd.MarkFlagsMutuallyExclusive("bytestream", "datagram")
	cmd.Flags().Bool("publish", false, "publish the tunnel")
	cmd.Flags().Bool("no-publish", false, "do not publish the tunnel")
	cmd.MarkFlagsMutuallyExclusive("publish", "no-publish")
	cmd.Flags().Bool("tls", false, "use TLS protocol")
	cmd.Flags().Bool("tcp", false, "publish a TCP tunnel")
	cmd.Flags().Bool("dtls", false, "use DTLS protocol")
	cmd.Flags().Bool("quic", false, "use QUIC protocol")
	cmd.Flags().Bool("http", false, "use HTTP protocol")
	cmd.MarkFlagsMutuallyExclusive("tls", "tcp", "dtls", "quic", "http")
	cmd.Flags().Uint32("tcp-port", 0, "use a reserved published TCP port")
	cmd.Flags().Bool("allow-cross-region-routing", false, "allow cross-region routing when ingress and tunnel owner are in different regions")
	cmd.Flags().StringArray("label", nil, "set tunnel labels (key=value, might be specified multiple times)")
	cmd.Flags().String("geoip", "", "comma-separated allowed countries (ISO 3166-1 alpha-2)")
	cmd.Flags().String("trusted-ips", "", "comma-separated allowed IP/CIDR ranges")
	cmd.Flags().String("host", "", "Stable domain for publishing")
	cmd.Flags().String("tls-mode", "", "TLS mode (terminated, passthrough)")
	cmd.Flags().String("tls-alpn", "", "comma-separated ALPN protocols")
	cmd.Flags().String("tls-min-version", "", "minimum TLS version (tls1.2, tls1.3)")
	cmd.Flags().String("tls-ciphers", "", "comma-separated TLS ciphers")
	cmd.Flags().Bool("mtls", false, "enable mTLS Tunnel access")
	cmd.Flags().String("http-version", "", "HTTP version (http/1.1, h2c, h3)")
	cmd.Flags().Bool("upstream-tls", false, "use TLS for the upstream side")
	cmd.Flags().Bool("datagram-guaranteed-delivery", false, "require reliable delivery for datagram tunnels")
	cmd.Flags().Bool("token-auth", false, "enable token-based HTTP authentication")
	cmd.Flags().Bool("rstream-auth", false, "require rstream account authentication (HTTP only)")
	cmd.Flags().Bool("challenge-mode", false, "require an interactive challenge before access (HTTP only)")
	cmd.Flags().Bool("retry", true, "enable automatic reconnection on disconnect")
	cmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	cmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	cmd.Flags().Int64("retry-interval", 5000, "retry interval in ms")
}

func newForwardCtx(cmd *cobra.Command, host, port string) (*forwardCtx, error) {
	props, err := newTunnelPropertiesFromFlags(cmd)
	if err != nil {
		return nil, err
	}
	return newForwardCtxWithProperties(cmd, host, port, props)
}

func newForwardCtxWithProperties(cmd *cobra.Command, host, port string, props *rstream.TunnelProperties) (result *forwardCtx, err error) {
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return nil, err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return nil, err
	}
	clientCloser := ownRstreamClient(client)
	defer func() {
		if err != nil {
			err = errors.Join(err, clientCloser.Close())
		}
	}()
	if err := rstream.MaybeSetGeneratedStableDomain(props, runtime.Resolved.StableDomainEndpoint()); err != nil {
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
		if !logging.IsTerminal(os.Stdout) {
			return nil, fmt.Errorf("output mode 'xterm' requires a terminal")
		} else if flagVerbose {
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
	result = &forwardCtx{
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
		clientCloser:     clientCloser,
	}
	if runtime.Resolved.Context != nil && runtime.Resolved.Context.ProjectEndpoint != "" && runtime.Resolved.APIURL != "" && runtime.Resolved.Token != "" {
		controlClient := newRuntimeControlPlaneClient(runtime.Resolved)
		endpoint := runtime.Resolved.Context.ProjectEndpoint
		result.resolveProject = func(ctx context.Context) (controlplane.Project, error) {
			return controlClient.ResolveProjectByEndpoint(ctx, endpoint)
		}
	}
	return result, nil
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
	sessions := newForwardSessionGroup(ctx)
	defer sessions.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.runOnce(ctx, sessions)
		var reported statusReportedError
		if err != nil && !errors.As(err, &reported) {
			status := newForwardStatus(nil)
			status.Status = rstream.StringPtr("disconnected")
			s.setStatus(status)
		}
		if err == nil {
			return nil
		}
		if !forwardRetryableError(err) {
			return err
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

func forwardRetryableError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var engineErr *rstream.EngineError
	if errors.As(err, &engineErr) {
		return engineErr.Retryable()
	}
	return true
}

func (s *forwardCtx) runOnce(ctx context.Context, sessions *forwardSessionGroup) error {
	connectingStatus := newForwardStatus(nil)
	connectingStatus.Status = rstream.StringPtr("connecting")
	s.setStatus(connectingStatus)
	ctrl, err := s.Client.Connect(ctx, nil)
	if err != nil {
		err = s.diagnoseProjectAvailability(ctx, err)
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
	if s.LocalHTTP != nil {
		return s.serveLocalHTTP(ctx, tunnel, props, forwarding, baseStatus)
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
		return s.serveWithCtx(ctx, l.Close, func() error { return s.serveTCP(ctx, l, sessions) })
	}
	if pl, ok := tunnel.(rstream.PacketListener); ok {
		return s.serveWithCtx(ctx, pl.Close, func() error { return s.serveUDP(ctx, pl, sessions) })
	}
	return fmt.Errorf("tunnel does not implement net.Listener or rstream.PacketListener")
}

func (s *forwardCtx) diagnoseProjectAvailability(ctx context.Context, transportErr error) error {
	if s.resolveProject == nil || ctx.Err() != nil {
		return transportErr
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	project, err := s.resolveProject(checkCtx)
	if err != nil || project.Status == "active" {
		return transportErr
	}
	message := fmt.Sprintf("project %q (%s) is %s and cannot serve traffic", project.Name, project.Endpoint, project.Status)
	if project.Issue != nil && project.Issue.Message != "" {
		message += ": " + project.Issue.Message
	}
	return &rstream.EngineError{Code: rstream.EngineErrorCodeInvalidRequest, Message: message}
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

func (s *forwardCtx) withTrackedConn(sessions *forwardSessionGroup, closer io.Closer, addr net.Addr, run func(context.Context)) {
	var streamID *string
	var sourceIP *net.IP
	if ra, ok := addr.(*rstream.Addr); ok && ra != nil {
		streamID = &ra.IdOrName
		sourceIP = &ra.SourceIP
	}
	idx := s.addConn(forwardConnInfo{Active: true, Date: time.Now(), StreamID: streamID, SourceIP: sourceIP})
	sessions.Start(closer, func(ctx context.Context) {
		defer func() {
			if idx != nil {
				s.closeConn(*idx)
			}
		}()
		run(ctx)
	})
}

func (s *forwardCtx) proxyTCP(sessions *forwardSessionGroup, inbound net.Conn) {
	s.withTrackedConn(sessions, inbound, inbound.LocalAddr(), func(ctx context.Context) {
		defer inbound.Close()
		dialer := &net.Dialer{}
		outbound, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
		if err != nil {
			s.Logger.Error("Dial error", slog.String("host", s.Host), slog.String("port", s.Port), slog.String("error", err.Error()))
		} else {
			defer outbound.Close()
			streamrelay.Bidirectional(inbound, outbound)
		}
	})
}

func (s *forwardCtx) proxyUDP(sessions *forwardSessionGroup, inbound net.PacketConn, remote net.Addr) {
	s.withTrackedConn(sessions, inbound, inbound.LocalAddr(), func(ctx context.Context) {
		defer inbound.Close()
		dialer := &net.Dialer{}
		outbound, err := dialer.DialContext(ctx, "udp", net.JoinHostPort(s.Host, s.Port))
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
		_ = inbound.Close()
		_ = outbound.Close()
		<-done
	})
}

func (s *forwardCtx) serveTCP(ctx context.Context, l net.Listener, sessions *forwardSessionGroup) error {
	var acceptRetryDelay time.Duration
	for {
		inbound, err := l.Accept()
		if err != nil {
			delay, retry := netretry.NextAcceptDelay(err, acceptRetryDelay, netcatAcceptRetryMaxDelay)
			if retry && netretry.Wait(ctx, delay) {
				acceptRetryDelay = delay
				continue
			}
			return err
		}
		acceptRetryDelay = 0
		s.proxyTCP(sessions, inbound)
	}
}

func (s *forwardCtx) serveUDP(ctx context.Context, l rstream.PacketListener, sessions *forwardSessionGroup) error {
	var acceptRetryDelay time.Duration
	for {
		inbound, raddr, err := l.Accept()
		if err != nil {
			delay, retry := netretry.NextAcceptDelay(err, acceptRetryDelay, netcatAcceptRetryMaxDelay)
			if retry && netretry.Wait(ctx, delay) {
				acceptRetryDelay = delay
				continue
			}
			return err
		}
		acceptRetryDelay = 0
		s.proxyUDP(sessions, inbound, raddr)
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

func (s *forwardCtx) addFileActivity(activity fileserver.Activity) {
	event := filesActivityEvent{
		Event:      "file_request",
		Date:       activity.Date,
		Backend:    activity.Backend,
		Operation:  activity.Operation,
		Method:     activity.Method,
		Path:       activity.Path,
		Status:     activity.Status,
		Bytes:      activity.Bytes,
		DurationMS: activity.Duration.Milliseconds(),
		Outcome:    activity.Outcome,
	}
	s.Logger.Debug("File request", "backend", event.Backend, "operation", event.Operation, "method", event.Method, "path", event.Path, "status", event.Status, "bytes", event.Bytes, "duration_ms", event.DurationMS, "outcome", event.Outcome)
	switch s.OutputFormat {
	case forwardOutputFormatText:
		s.writef("file request: date=%s backend=%s operation=%s method=%s path=%q status=%d bytes=%d duration_ms=%d outcome=%s\n", event.Date.UTC().Format("2006-01-02 15:04:05.000 UTC"), event.Backend, event.Operation, event.Method, event.Path, event.Status, event.Bytes, event.DurationMS, event.Outcome)
	case forwardOutputFormatJSON:
		s.writeJSON(event)
	case forwardOutputFormatXTerm:
		if ui, ok := s.UI.(interface{ AddFileActivity(filesActivityEvent) }); ok {
			ui.AddFileActivity(event)
		}
	case forwardOutputFormatNone:
	}
}

func (s *forwardCtx) writeLine(a ...any) {
	if s.Out == nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintln(s.Out, a...)
}

func (s *forwardCtx) writef(format string, a ...any) {
	if s.Out == nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintf(s.Out, format, a...)
}

func (s *forwardCtx) writeJSON(v any) {
	if s.Out == nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
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
	files := s.LocalHTTP != nil || st.Files != nil
	lines := []kv{{"version", val(st.Version)}, {"update", val(st.Update)}}
	if files {
		lines = append(lines, kv{"status", val(st.Status)}, kv{"forwarding", val(st.Forwarding)}, kv{"forwarded", val(st.Forwarded)})
	} else {
		lines = append(lines, kv{"plan", val(st.Plan)}, kv{"provider", val(st.Provider)}, kv{"region", val(st.Region)}, kv{"status", val(st.Status)}, kv{"tunnel ID", val(st.TunnelID)}, kv{"forwarding", val(st.Forwarding)}, kv{"forwarded", val(st.Forwarded)})
	}
	if files && st.Files != nil {
		lines = append(lines, kv{"backend", st.Files.Backend}, kv{"access", st.Files.Access})
		if st.Files.Username != "" {
			lines = append(lines, kv{"username", st.Files.Username})
		}
		lines = append(lines, kv{"mode", filesMode(st.Files)})
	}
	maxw := 0
	for _, kv := range lines {
		if len(kv.k) > maxw {
			maxw = len(kv.k)
		}
	}
	if files {
		s.writeLine("file server status")
	} else {
		s.writeLine("tunnel status")
	}
	for _, kv := range lines {
		s.writef("  %-*s : %s\n", maxw, kv.k, kv.v)
	}
}

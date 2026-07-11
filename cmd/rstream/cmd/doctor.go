// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

type doctorStatus string

const (
	doctorStatusPass doctorStatus = "pass"
	doctorStatusWarn doctorStatus = "warn"
	doctorStatusFail doctorStatus = "fail"
	doctorStatusSkip doctorStatus = "skip"
)

type doctorCheck struct {
	Name    string            `json:"name"`
	Status  doctorStatus      `json:"status"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

type doctorSummary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

type doctorReport struct {
	Version         string        `json:"version"`
	Channel         string        `json:"channel,omitempty"`
	ConfigPath      string        `json:"configPath,omitempty"`
	APIURL          string        `json:"apiUrl,omitempty"`
	ContextName     string        `json:"contextName,omitempty"`
	Engine          string        `json:"engine,omitempty"`
	ProjectEndpoint string        `json:"projectEndpoint,omitempty"`
	Summary         doctorSummary `json:"summary"`
	Checks          []doctorCheck `json:"checks"`
	GeneratedAt     time.Time     `json:"generatedAt"`
}

type doctorTokenInfo struct {
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	Permissions  []string   `json:"permissions,omitempty"`
	Scopes       []string   `json:"scopes,omitempty"`
	HasResources bool       `json:"hasResources"`
}

var doctorCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "doctor",
	Short:        "Diagnose rstream CLI, project, token, and engine readiness",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		report := runDoctor(cmd)
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "json":
			return writeStructuredOutput("json", report)
		case "table":
			return printDoctorTable(os.Stdout, report)
		default:
			return validateOutputMode(output, "table", "json")
		}
	},
}

func init() {
	doctorCmd.Flags().SortFlags = false
	doctorCmd.PersistentFlags().SortFlags = false
	doctorCmd.Flags().StringP("output", "o", "table", "output mode (table, json)")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command) doctorReport {
	report := doctorReport{Version: rstream.Version, Channel: rstream.Channel, GeneratedAt: time.Now().UTC()}
	path, cfg, err := loadConfig(cmd)
	if err != nil {
		report.add("config", doctorStatusFail, err.Error(), nil)
		report.finalize()
		return report
	}
	report.ConfigPath = path
	report.add("config", doctorStatusPass, "configuration loaded", map[string]string{"path": path})
	resolved, err := resolveDoctorRuntime(cmd, cfg)
	if err != nil {
		report.add("context", doctorStatusFail, err.Error(), nil)
		report.finalize()
		return report
	}
	report.APIURL = resolved.APIURL
	report.ContextName = resolved.ContextName
	report.Engine = resolved.Engine
	if resolved.Context != nil {
		report.ProjectEndpoint = resolved.Context.ProjectEndpoint
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	checkDoctorContext(&report, resolved)
	checkDoctorToken(&report, resolved.Token)
	if doctorUsesControlPlane(resolved) {
		checkDoctorControlPlane(ctx, &report, resolved)
		checkDoctorProject(ctx, &report, resolved)
	} else {
		report.add("control_plane_auth", doctorStatusSkip, "context is not linked to a hosted API URL", nil)
		report.add("project", doctorStatusSkip, "context is engine-only", nil)
	}
	checkDoctorNetwork(ctx, &report, resolved)
	checkDoctorEngine(ctx, &report, resolved)
	report.finalize()
	return report
}

func resolveDoctorRuntime(cmd *cobra.Command, cfg config.Config) (config.Resolved, error) {
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagContext, _ := cmd.Flags().GetString("context")
	flagTunnelTransport, _ := cmd.Flags().GetString("tunnel-transport")
	env := config.ReadEnv()
	resolved, err := config.Resolve(config.ResolveInput{
		Config:              cfg,
		FlagAPIURL:          flagAPIURL,
		FlagContext:         flagContext,
		EnvAPIURL:           env.APIURL,
		EnvContext:          env.Context,
		EnvEngine:           env.Engine,
		EnvToken:            env.Token,
		FlagTunnelTransport: flagTunnelTransport,
		EnvTunnelTransport:  env.TunnelTransport,
		EnvUseQUIC:          env.UseQUIC,
		ResolveToken:        true,
	})
	if err != nil {
		return config.Resolved{}, err
	}
	return resolved, nil
}

func checkDoctorContext(report *doctorReport, resolved config.Resolved) {
	if resolved.Context == nil {
		report.add("context", doctorStatusWarn, "no context selected", map[string]string{"apiUrl": resolved.APIURL})
		return
	}
	details := map[string]string{"name": resolved.Context.Name, "apiUrl": resolved.APIURL}
	if resolved.Context.ProjectEndpoint != "" {
		details["projectEndpoint"] = resolved.Context.ProjectEndpoint
	}
	if resolved.Engine != "" {
		details["engine"] = resolved.Engine
	}
	report.add("context", doctorStatusPass, "context selected", details)
}

func doctorUsesControlPlane(resolved config.Resolved) bool {
	return resolved.Context == nil || resolved.Context.APIURL != ""
}

func checkDoctorToken(report *doctorReport, token string) {
	if strings.TrimSpace(token) == "" {
		report.add("token", doctorStatusFail, "token is not configured", nil)
		return
	}
	info, err := parseDoctorTokenInfo(token)
	if err != nil {
		report.add("token", doctorStatusWarn, "token is present but claims could not be parsed", map[string]string{"error": err.Error()})
		return
	}
	details := map[string]string{"present": "true"}
	if info.ExpiresAt != nil {
		details["expiresAt"] = info.ExpiresAt.Format(time.RFC3339)
		if time.Now().After(*info.ExpiresAt) {
			report.add("token", doctorStatusFail, "token has expired", details)
			return
		}
	}
	if len(info.Permissions) > 0 {
		details["permissions"] = strings.Join(info.Permissions, " ")
	}
	if len(info.Scopes) > 0 {
		details["scopes"] = strings.Join(info.Scopes, " ")
	}
	if info.HasResources {
		details["resources"] = "present"
	}
	report.add("token", doctorStatusPass, "token is configured", details)
}

func checkDoctorControlPlane(ctx context.Context, report *doctorReport, resolved config.Resolved) {
	if resolved.Token == "" {
		report.add("control_plane_auth", doctorStatusSkip, "token is required", nil)
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := controlplane.NewClient(resolved.APIURL, resolved.Token)
	whoami, err := client.Whoami(runCtx)
	if err != nil {
		report.add("control_plane_auth", doctorStatusFail, mapControlPlaneError(err).Error(), nil)
		return
	}
	details := map[string]string{"userId": whoami.ID, "role": whoami.Role, "permissions": strconv.Itoa(len(whoami.Permissions))}
	if whoami.Email != "" {
		details["email"] = whoami.Email
	}
	report.add("control_plane_auth", doctorStatusPass, "Control plane API token accepted", details)
}

func checkDoctorProject(ctx context.Context, report *doctorReport, resolved config.Resolved) {
	if resolved.Context == nil || resolved.Context.ProjectEndpoint == "" {
		report.add("project", doctorStatusSkip, "project endpoint is not configured", nil)
		return
	}
	if resolved.Token == "" {
		report.add("project", doctorStatusSkip, "token is required", nil)
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := controlplane.NewClient(resolved.APIURL, resolved.Token)
	project, err := client.ResolveProjectByEndpoint(runCtx, resolved.Context.ProjectEndpoint)
	if err != nil {
		report.add("project", doctorStatusFail, mapControlPlaneError(err).Error(), map[string]string{"projectEndpoint": resolved.Context.ProjectEndpoint})
		return
	}
	details := map[string]string{"id": project.ID, "name": project.Name, "endpoint": project.Endpoint, "status": project.Status, "plan": project.Plan, "engine": project.EngineAddress()}
	if project.Region != "" {
		details["region"] = project.Region
	}
	report.add("project", doctorStatusPass, "project resolved", details)
}

func checkDoctorNetwork(ctx context.Context, report *doctorReport, resolved config.Resolved) {
	host, address, err := doctorEngineHostPort(resolved.Engine)
	if err != nil {
		report.add("engine_address", doctorStatusFail, err.Error(), nil)
		return
	}
	if host == "" {
		report.add("engine_address", doctorStatusSkip, "engine is not configured", nil)
		return
	}
	report.add("engine_address", doctorStatusPass, "engine address configured", map[string]string{"address": address, "host": host})
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupHost(lookupCtx, host)
	if err != nil {
		report.add("dns", doctorStatusFail, err.Error(), map[string]string{"host": host})
	} else {
		report.add("dns", doctorStatusPass, "engine host resolves", map[string]string{"host": host, "addresses": strings.Join(ips, ",")})
	}
	mode := doctorConfiguredTransportMode(resolved.Transport)
	tlsResultCh := make(chan doctorTransportProbe, 1)
	quicResultCh := make(chan doctorTransportProbe, 1)
	go func() { tlsResultCh <- probeDoctorTLS(ctx, resolved, host, address) }()
	go func() { quicResultCh <- probeDoctorQUIC(ctx, resolved, host, address) }()
	tlsResult := <-tlsResultCh
	quicResult := <-quicResultCh
	tlsStatus, quicStatus := doctorTransportProbeStatuses(mode, tlsResult.OK, quicResult.OK)
	report.add("tls", tlsStatus, tlsResult.Message, tlsResult.Details)
	report.add("quic_transport", quicStatus, quicResult.Message, quicResult.Details)

	selected := "none"
	selectionStatus := doctorStatusFail
	selectionMessage := "no tunnel transport is reachable"
	switch mode {
	case rstream.TunnelTransportModeTLS:
		selected = "tls"
		if tlsResult.OK {
			selectionStatus = doctorStatusPass
			selectionMessage = "TLS tunnel transport is reachable"
		}
	case rstream.TunnelTransportModeQUIC:
		selected = "quic"
		if quicResult.OK {
			selectionStatus = doctorStatusPass
			selectionMessage = "QUIC tunnel transport is reachable"
		}
	default:
		if quicResult.OK {
			selected = "quic"
			selectionStatus = doctorStatusPass
			selectionMessage = "auto transport will prefer QUIC"
		} else if tlsResult.OK {
			selected = "tls"
			selectionStatus = doctorStatusWarn
			selectionMessage = "auto transport will fall back to TLS"
		}
	}
	report.add("tunnel_transport", selectionStatus, selectionMessage, map[string]string{"configuredMode": string(mode), "selectedMode": selected})
}

func checkDoctorEngine(ctx context.Context, report *doctorReport, resolved config.Resolved) {
	if resolved.Engine == "" {
		report.add("engine_inventory", doctorStatusSkip, "engine is not configured", nil)
		return
	}
	if resolved.Token == "" {
		report.add("engine_inventory", doctorStatusSkip, "token is required", nil)
		return
	}
	client, err := newClientFromResolved(resolved)
	if err != nil {
		report.add("engine_inventory", doctorStatusFail, err.Error(), nil)
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	clients, err := client.ListClients(runCtx, nil)
	if err != nil {
		report.add("engine_clients", doctorStatusFail, err.Error(), nil)
	} else {
		report.add("engine_clients", doctorStatusPass, "engine clients listed", map[string]string{"count": strconv.Itoa(len(*clients))})
	}
	tunnels, err := client.ListTunnels(runCtx, nil)
	if err != nil {
		report.add("engine_tunnels", doctorStatusFail, err.Error(), nil)
		return
	}
	report.add("engine_tunnels", doctorStatusPass, "engine tunnels listed", map[string]string{"total": strconv.Itoa(len(*tunnels)), "online": strconv.Itoa(countDoctorOnlineTunnels(*tunnels))})
}

type doctorTransportProbe struct {
	OK      bool
	Message string
	Details map[string]string
}

func probeDoctorTLS(ctx context.Context, resolved config.Resolved, host, address string) doctorTransportProbe {
	transport, ok := doctorTLSTransport(resolved.Transport)
	if !ok {
		return doctorTransportProbe{Message: "TLS probe is unavailable for the configured custom transport"}
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tlsCfg := doctorTLSConfig(resolved.TLSClientConfig, host)
	conn, err := transport.Dial(runCtx, address, tlsCfg)
	if err != nil {
		return doctorTransportProbe{Message: "TLS connection failed", Details: map[string]string{"address": address, "error": err.Error()}}
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		_ = conn.Close()
		return doctorTransportProbe{Message: "TLS probe returned an unexpected connection type", Details: map[string]string{"address": address, "type": fmt.Sprintf("%T", conn)}}
	}
	state := tlsConn.ConnectionState()
	_ = conn.Close()
	return doctorTransportProbe{OK: true, Message: "TLS handshake succeeded", Details: map[string]string{"address": address, "serverName": tlsCfg.ServerName, "version": tlsVersionName(state.Version)}}
}

func probeDoctorQUIC(ctx context.Context, resolved config.Resolved, host, address string) doctorTransportProbe {
	transport, ok := doctorQUICTransport(resolved.Transport)
	if !ok {
		return doctorTransportProbe{Message: "QUIC probe is unavailable for the configured custom transport"}
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tlsCfg := doctorTLSConfig(resolved.TLSClientConfig, host)
	conn, err := transport.Dial(runCtx, address, tlsCfg)
	if err != nil {
		return doctorTransportProbe{Message: "QUIC connection failed; UDP may be blocked on this network", Details: map[string]string{"address": address, "error": err.Error()}}
	}
	_ = conn.Close()
	_ = transport.Close()
	return doctorTransportProbe{OK: true, Message: "QUIC connection succeeded", Details: map[string]string{"address": address, "host": host}}
}

func doctorQUICTransport(transport rstream.Dialer) (*rstream.QUICTransport, bool) {
	if auto, ok := transport.(*rstream.AutoTransport); ok && auto != nil {
		transport = auto.QUIC
	}
	quicTransport, ok := transport.(*rstream.QUICTransport)
	if !ok {
		return nil, false
	}
	if quicTransport == nil {
		quicTransport = &rstream.QUICTransport{}
	}
	return &rstream.QUICTransport{
		LocalAddr:            quicTransport.LocalAddr,
		NetworkInterface:     quicTransport.NetworkInterface,
		ForceIPv4:            quicTransport.ForceIPv4,
		ForceIPv6:            quicTransport.ForceIPv6,
		DNSOverride:          quicTransport.DNSOverride,
		DNSOverTLS:           quicTransport.DNSOverTLS,
		DNSServerName:        quicTransport.DNSServerName,
		DNSSECEnabled:        quicTransport.DNSSECEnabled,
		ProxyHTTP:            quicTransport.ProxyHTTP,
		ProxySOCKS5:          quicTransport.ProxySOCKS5,
		ProxyUsername:        quicTransport.ProxyUsername,
		ProxyPassword:        quicTransport.ProxyPassword,
		ProxyHTTPHeaders:     cloneDoctorHeaders(quicTransport.ProxyHTTPHeaders),
		TLSProxyConfig:       cloneDoctorTLSConfig(quicTransport.TLSProxyConfig),
		ProxyFromEnvironment: quicTransport.ProxyFromEnvironment,
	}, true
}

func doctorTLSTransport(transport rstream.Dialer) (*rstream.Transport, bool) {
	if auto, ok := transport.(*rstream.AutoTransport); ok && auto != nil {
		transport = auto.TLS
	}
	switch current := transport.(type) {
	case nil:
		return &rstream.Transport{}, true
	case *rstream.Transport:
		if current == nil {
			return &rstream.Transport{}, true
		}
		out := *current
		out.ProxyHTTPHeaders = cloneDoctorHeaders(current.ProxyHTTPHeaders)
		out.TLSProxyConfig = cloneDoctorTLSConfig(current.TLSProxyConfig)
		return &out, true
	case *rstream.QUICTransport:
		if current == nil {
			return &rstream.Transport{}, true
		}
		return &rstream.Transport{LocalAddr: current.LocalAddr, NetworkInterface: current.NetworkInterface, ForceIPv4: current.ForceIPv4, ForceIPv6: current.ForceIPv6, DNSOverride: current.DNSOverride, DNSOverTLS: current.DNSOverTLS, DNSServerName: current.DNSServerName, DNSSECEnabled: current.DNSSECEnabled, ProxyHTTP: current.ProxyHTTP, ProxySOCKS5: current.ProxySOCKS5, ProxyUsername: current.ProxyUsername, ProxyPassword: current.ProxyPassword, ProxyHTTPHeaders: cloneDoctorHeaders(current.ProxyHTTPHeaders), TLSProxyConfig: cloneDoctorTLSConfig(current.TLSProxyConfig), ProxyFromEnvironment: current.ProxyFromEnvironment}, true
	default:
		return nil, false
	}
}

func doctorConfiguredTransportMode(transport rstream.Dialer) rstream.TunnelTransportMode {
	switch transport.(type) {
	case *rstream.Transport:
		return rstream.TunnelTransportModeTLS
	case *rstream.QUICTransport:
		return rstream.TunnelTransportModeQUIC
	default:
		return rstream.TunnelTransportModeAuto
	}
}

func doctorTransportProbeStatuses(mode rstream.TunnelTransportMode, tlsOK, quicOK bool) (doctorStatus, doctorStatus) {
	tlsStatus := doctorStatusPass
	if !tlsOK {
		tlsStatus = doctorStatusFail
	}
	quicStatus := doctorStatusPass
	if !quicOK {
		quicStatus = doctorStatusFail
	}
	if mode == rstream.TunnelTransportModeAuto {
		if tlsOK && !quicOK {
			quicStatus = doctorStatusWarn
		}
		if quicOK && !tlsOK {
			tlsStatus = doctorStatusWarn
		}
	} else if mode == rstream.TunnelTransportModeTLS && !quicOK {
		quicStatus = doctorStatusWarn
	} else if mode == rstream.TunnelTransportModeQUIC && !tlsOK {
		tlsStatus = doctorStatusWarn
	}
	return tlsStatus, quicStatus
}

func doctorTLSConfig(base *tls.Config, host string) *tls.Config {
	var cfg *tls.Config
	if base == nil {
		cfg = &tls.Config{}
	} else {
		cfg = base.Clone()
	}
	if cfg.ServerName == "" {
		cfg.ServerName = host
	}
	cfg.NextProtos = []string{"rstrm/1"}
	return cfg
}

func cloneDoctorHeaders(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneDoctorTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	return cfg.Clone()
}

func (r *doctorReport) add(name string, status doctorStatus, message string, details map[string]string) {
	r.Checks = append(r.Checks, doctorCheck{Name: name, Status: status, Message: message, Details: details})
}

func (r *doctorReport) finalize() {
	for _, check := range r.Checks {
		switch check.Status {
		case doctorStatusPass:
			r.Summary.Pass++
		case doctorStatusWarn:
			r.Summary.Warn++
		case doctorStatusFail:
			r.Summary.Fail++
		case doctorStatusSkip:
			r.Summary.Skip++
		}
	}
}

func printDoctorTable(w *os.File, report doctorReport) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "Version\t%s\n", formatVersion(report.Version, report.Channel))
	if report.ConfigPath != "" {
		fmt.Fprintf(tw, "Config\t%s\n", report.ConfigPath)
	}
	if report.ContextName != "" {
		fmt.Fprintf(tw, "Context\t%s\n", report.ContextName)
	}
	if report.ProjectEndpoint != "" {
		fmt.Fprintf(tw, "Project\t%s\n", report.ProjectEndpoint)
	}
	if report.Engine != "" {
		fmt.Fprintf(tw, "Engine\t%s\n", report.Engine)
	}
	fmt.Fprintf(tw, "Summary\tpass=%d warn=%d fail=%d skip=%d\n\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Skip)
	fmt.Fprintln(tw, "CHECK\tSTATUS\tMESSAGE")
	for _, check := range report.Checks {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", check.Name, check.Status, check.Message)
	}
	return tw.Flush()
}

func doctorEngineHostPort(engine string) (string, string, error) {
	value := strings.TrimSpace(engine)
	if value == "" {
		return "", "", nil
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", "", err
		}
		value = parsed.Host
	}
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		return host, net.JoinHostPort(host, port), nil
	}
	if strings.Contains(value, ":") {
		return "", "", fmt.Errorf("invalid engine address %q: %w", engine, err)
	}
	return value, net.JoinHostPort(value, "443"), nil
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%x", version)
	}
}

func countDoctorOnlineTunnels(tunnels []rstream.TunnelInventory) int {
	count := 0
	for _, tunnel := range tunnels {
		if tunnel.Status == "online" {
			count++
		}
	}
	return count
}

func parseDoctorTokenInfo(token string) (doctorTokenInfo, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return doctorTokenInfo{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return doctorTokenInfo{}, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return doctorTokenInfo{}, err
	}
	info := doctorTokenInfo{Permissions: doctorStringSliceClaim(claims["permissions"]), Scopes: doctorScopesClaim(claims)}
	if exp, ok := doctorNumberClaim(claims["exp"]); ok {
		expiresAt := time.Unix(int64(exp), 0).UTC()
		info.ExpiresAt = &expiresAt
	}
	if resources, ok := claims["resources"].(map[string]any); ok {
		if _, ok := resources["tunnels"]; ok {
			info.HasResources = true
		}
	}
	sort.Strings(info.Permissions)
	sort.Strings(info.Scopes)
	return info, nil
}

func doctorScopesClaim(claims map[string]any) []string {
	if values := doctorStringSliceClaim(claims["scp"]); len(values) > 0 {
		return values
	}
	if raw, ok := claims["scope"].(string); ok {
		return strings.Fields(raw)
	}
	return nil
}

func doctorStringSliceClaim(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func doctorNumberClaim(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

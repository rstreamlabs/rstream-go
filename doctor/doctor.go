// See LICENSE file in the project root for license information.

// Package doctor runs rstream diagnostics independently of the CLI.
package doctor

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

// Status is the outcome of one diagnostic check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Check describes the outcome and structured details of a diagnostic.
type Check struct {
	Name    string            `json:"name"`
	Status  Status            `json:"status"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Summary counts check outcomes. Warnings do not make a report fail.
type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// Report contains the structured diagnostics also rendered by the CLI.
type Report struct {
	Version         string    `json:"version"`
	Channel         string    `json:"channel,omitempty"`
	ConfigPath      string    `json:"configPath,omitempty"`
	APIURL          string    `json:"apiUrl,omitempty"`
	ContextName     string    `json:"contextName,omitempty"`
	Engine          string    `json:"engine,omitempty"`
	ProjectEndpoint string    `json:"projectEndpoint,omitempty"`
	Summary         Summary   `json:"summary"`
	Checks          []Check   `json:"checks"`
	GeneratedAt     time.Time `json:"generatedAt"`
}

type doctorTokenInfo struct {
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	Permissions  []string   `json:"permissions,omitempty"`
	Scopes       []string   `json:"scopes,omitempty"`
	HasResources bool       `json:"hasResources"`
}

type doctorTunnelClient interface {
	Connect(context.Context, *rstream.Config) (rstream.ControlChannel, error)
}

type doctorTunnelProbe struct {
	mode       string
	properties rstream.TunnelProperties
}

func checkDoctorContext(report *Report, resolved config.Resolved) {
	if resolved.Context == nil {
		report.add("context", StatusWarn, "no context selected", map[string]string{"apiUrl": resolved.APIURL})
		return
	}
	details := map[string]string{"name": resolved.Context.Name, "apiUrl": resolved.APIURL}
	if resolved.Context.ProjectEndpoint != "" {
		details["projectEndpoint"] = resolved.Context.ProjectEndpoint
	}
	if resolved.Engine != "" {
		details["engine"] = resolved.Engine
	}
	report.add("context", StatusPass, "context selected", details)
}

func doctorUsesControlPlane(resolved config.Resolved) bool {
	return resolved.Context == nil || resolved.Context.APIURL != ""
}

func checkDoctorToken(report *Report, token string) {
	if strings.TrimSpace(token) == "" {
		report.add("token", StatusFail, "token is not configured", nil)
		return
	}
	info, err := parseDoctorTokenInfo(token)
	if err != nil {
		report.add("token", StatusWarn, "token is present but claims could not be parsed", map[string]string{"error": err.Error()})
		return
	}
	details := map[string]string{"present": "true"}
	if info.ExpiresAt != nil {
		details["expiresAt"] = info.ExpiresAt.Format(time.RFC3339)
		if time.Now().After(*info.ExpiresAt) {
			report.add("token", StatusFail, "token has expired", details)
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
	report.add("token", StatusPass, "token is configured", details)
}

func checkDoctorAuthentication(ctx context.Context, report *Report, resolved config.Resolved) {
	if !resolved.HasMTLS() {
		checkDoctorToken(report, resolved.Token)
		return
	}
	report.add("token", StatusSkip, "agent uses mTLS authentication", nil)
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	certificate, err := resolved.CheckExternalMTLS(probeCtx)
	if err != nil {
		report.add("mtls", StatusFail, "external mTLS identity/signature check failed", map[string]string{"error": err.Error()})
		return
	}
	if certificate == nil {
		report.add("mtls", StatusPass, "client certificate is configured", nil)
		return
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	report.add("mtls", StatusPass, "external certificate and signing capabilities verified", map[string]string{"certificateSHA256": fmt.Sprintf("%x", fingerprint), "expiresAt": certificate.NotAfter.UTC().Format(time.RFC3339)})
}

func checkDoctorControlPlane(ctx context.Context, report *Report, resolved config.Resolved) {
	if resolved.Token == "" {
		report.add("control_plane_auth", StatusSkip, "token is required", nil)
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := controlplane.NewClient(resolved.APIURL, resolved.Token, controlplane.WithHeaders(resolved.ControlPlaneHeaders))
	defer client.CloseIdleConnections()
	whoami, err := client.Whoami(runCtx)
	if err != nil {
		report.add("control_plane_auth", StatusFail, mapControlPlaneError(err).Error(), nil)
		return
	}
	details := map[string]string{"userId": whoami.ID, "role": whoami.Role, "permissions": strconv.Itoa(len(whoami.Permissions))}
	if whoami.Email != "" {
		details["email"] = whoami.Email
	}
	report.add("control_plane_auth", StatusPass, "Control plane API token accepted", details)
}

func checkDoctorProject(ctx context.Context, report *Report, resolved config.Resolved) bool {
	if resolved.Context == nil || resolved.Context.ProjectEndpoint == "" {
		report.add("project", StatusSkip, "project endpoint is not configured", nil)
		return true
	}
	if resolved.Token == "" {
		report.add("project", StatusSkip, "token is required", nil)
		return true
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := controlplane.NewClient(resolved.APIURL, resolved.Token, controlplane.WithHeaders(resolved.ControlPlaneHeaders))
	defer client.CloseIdleConnections()
	project, err := client.ResolveProjectByEndpoint(runCtx, resolved.Context.ProjectEndpoint)
	if err != nil {
		report.add("project", StatusFail, mapControlPlaneError(err).Error(), map[string]string{"projectEndpoint": resolved.Context.ProjectEndpoint})
		// A control plane lookup failure does not prove that a prepared data plane
		// context is unavailable. Keep probing transports in that case.
		return true
	}
	details := map[string]string{"id": project.ID, "name": project.Name, "endpoint": project.Endpoint, "status": project.Status, "plan": project.Plan, "engine": project.EngineAddress()}
	if project.Region != "" {
		details["region"] = project.Region
	}
	if project.Status != "active" {
		message := fmt.Sprintf("project is %s and cannot serve traffic", project.Status)
		if project.Issue != nil {
			if project.Issue.Category != "" {
				details["issueCategory"] = project.Issue.Category
			}
			if project.Issue.Code != "" {
				details["issueCode"] = project.Issue.Code
			}
			if project.Issue.OccurredAt != "" {
				details["issueOccurredAt"] = project.Issue.OccurredAt
			}
			if project.Issue.Message != "" {
				message += ": " + project.Issue.Message
			}
		}
		report.add("project", StatusFail, message, details)
		return false
	}
	report.add("project", StatusPass, "project resolved", details)
	return true
}

func checkDoctorNetwork(ctx context.Context, report *Report, resolved config.Resolved) {
	host, address, err := doctorEngineHostPort(resolved.Engine)
	if err != nil {
		report.add("engine_address", StatusFail, err.Error(), nil)
		return
	}
	if host == "" {
		report.add("engine_address", StatusSkip, "engine is not configured", nil)
		return
	}
	report.add("engine_address", StatusPass, "engine address configured", map[string]string{"address": address, "host": host})
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupHost(lookupCtx, host)
	if err != nil {
		report.add("dns", StatusFail, err.Error(), map[string]string{"host": host})
	} else {
		report.add("dns", StatusPass, "engine host resolves", map[string]string{"host": host, "addresses": strings.Join(ips, ",")})
	}
	if ctx.Err() != nil {
		return
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
	selectionStatus := StatusFail
	selectionMessage := "no tunnel transport is reachable"
	switch mode {
	case rstream.TunnelTransportModeTLS:
		selected = "tls"
		if tlsResult.OK {
			selectionStatus = StatusPass
			selectionMessage = "TLS tunnel transport is reachable"
		}
	case rstream.TunnelTransportModeQUIC:
		selected = "quic"
		if quicResult.OK {
			selectionStatus = StatusPass
			selectionMessage = "QUIC tunnel transport is reachable"
		}
	default:
		if quicResult.OK {
			selected = "quic"
			selectionStatus = StatusPass
			selectionMessage = "auto transport will prefer QUIC"
		} else if tlsResult.OK {
			selected = "tls"
			selectionStatus = StatusWarn
			selectionMessage = "auto transport will fall back to TLS"
		}
	}
	report.add("tunnel_transport", selectionStatus, selectionMessage, map[string]string{"configuredMode": string(mode), "selectedMode": selected})
}

func checkDoctorEngine(ctx context.Context, report *Report, resolved config.Resolved) {
	if resolved.Engine == "" {
		report.add("engine", StatusSkip, "engine is not configured", nil)
		return
	}
	if resolved.Token == "" && !resolved.HasMTLS() {
		report.add("engine", StatusSkip, "token is required", nil)
		return
	}
	client, err := config.NewClientFromResolved(resolved)
	if err != nil {
		report.add("engine", StatusFail, err.Error(), nil)
		return
	}
	defer closeClient(report, client)
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if resolved.HasMTLS() {
		ctrl, err := client.Connect(runCtx, nil)
		if err != nil {
			report.add("engine", StatusFail, "mTLS control-channel admission failed", map[string]string{"error": err.Error()})
			return
		}
		if err := ctrl.Close(); err != nil {
			report.add("engine", StatusFail, "failed to close mTLS control-channel probe", map[string]string{"error": err.Error()})
			return
		}
		report.add("engine", StatusPass, "mTLS control-channel admission succeeded", nil)
		return
	}
	health, err := client.CheckHealth(runCtx)
	if err != nil {
		report.add("engine", StatusFail, "engine health check failed", map[string]string{"error": err.Error()})
		return
	}
	if !health.Ready {
		report.add("engine", StatusFail, "engine is running but unavailable", nil)
		return
	}
	clients, err := client.ListClients(runCtx, nil)
	if err != nil {
		report.add("engine", StatusFail, "engine API is unavailable", map[string]string{"error": err.Error()})
		return
	}
	tunnels, err := client.ListTunnels(runCtx, nil)
	if err != nil {
		report.add("engine", StatusFail, "engine API is unavailable", map[string]string{"error": err.Error()})
		return
	}
	report.add("engine", StatusPass, "engine is ready", map[string]string{"clients": strconv.Itoa(len(*clients)), "tunnels": strconv.Itoa(len(*tunnels)), "onlineTunnels": strconv.Itoa(countDoctorOnlineTunnels(*tunnels))})
}

func checkDoctorTunnelCreation(ctx context.Context, report *Report, resolved config.Resolved) {
	if resolved.Engine == "" {
		report.add("tunnel_creation", StatusSkip, "engine is not configured", nil)
		return
	}
	if resolved.Token == "" && !resolved.HasMTLS() {
		report.add("tunnel_creation", StatusSkip, "token is required", nil)
		return
	}
	client, err := config.NewClientFromResolved(resolved)
	if err != nil {
		report.add("tunnel_creation", StatusFail, "failed to configure the tunnel client", map[string]string{"error": err.Error()})
		return
	}
	defer closeClient(report, client)
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	details, err := probeDoctorTunnelCreation(runCtx, client)
	if err != nil {
		report.add("tunnel_creation", StatusFail, "tunnel lifecycle failed", map[string]string{"error": err.Error()})
		return
	}
	report.add("tunnel_creation", StatusPass, "tunnel lifecycle succeeded", details)
}

func probeDoctorTunnelCreation(ctx context.Context, client doctorTunnelClient) (map[string]string, error) {
	details, err := probeDoctorTunnelLifecycle(ctx, client, doctorPrivateTunnelProbe())
	if err == nil {
		return details, nil
	}
	var engineErr *rstream.EngineError
	if !errors.As(err, &engineErr) || engineErr.Code != rstream.EngineErrorCodeFeatureNotAvailable {
		return nil, err
	}
	details, fallbackErr := probeDoctorTunnelLifecycle(ctx, client, doctorPublishedHTTPTunnelProbe())
	if fallbackErr != nil {
		return nil, fmt.Errorf("private tunnel is unavailable and published HTTP fallback failed: %w", fallbackErr)
	}
	details["fallbackReason"] = "private_feature_unavailable"
	return details, nil
}

func probeDoctorTunnelLifecycle(ctx context.Context, client doctorTunnelClient, probe doctorTunnelProbe) (map[string]string, error) {
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("opening control channel: %w", err)
	}
	controlClosed := false
	defer func() {
		if !controlClosed {
			_ = control.Close()
		}
	}()
	tunnel, err := control.CreateTunnel(ctx, probe.properties)
	if err != nil {
		return nil, fmt.Errorf("creating %s tunnel: %w", probe.mode, err)
	}
	props, err := tunnel.Properties()
	if err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("reading tunnel properties: %w", err)
	}
	if err := tunnel.Close(); err != nil {
		return nil, fmt.Errorf("closing %s tunnel: %w", probe.mode, err)
	}
	if err := control.Close(); err != nil {
		return nil, fmt.Errorf("closing control channel: %w", err)
	}
	controlClosed = true
	details := map[string]string{"mode": probe.mode}
	if props.ID != nil {
		details["tunnelId"] = *props.ID
	}
	return details, nil
}

func doctorPrivateTunnelProbe() doctorTunnelProbe {
	return doctorTunnelProbe{mode: "private", properties: rstream.TunnelProperties{Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream), Publish: rstream.BoolPtr(false)}}
}

func doctorPublishedHTTPTunnelProbe() doctorTunnelProbe {
	return doctorTunnelProbe{mode: "published_http", properties: rstream.TunnelProperties{Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream), Publish: rstream.BoolPtr(true), Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP), HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1)}}
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
	defer transport.Close()
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tlsCfg := doctorTLSConfig(resolved.TLSClientConfig, host)
	conn, err := transport.Dial(runCtx, address, tlsCfg)
	if err != nil {
		return doctorTransportProbe{Message: "QUIC connection failed; UDP may be blocked on this network", Details: map[string]string{"address": address, "error": err.Error()}}
	}
	_ = conn.Close()
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

func doctorTransportProbeStatuses(mode rstream.TunnelTransportMode, tlsOK, quicOK bool) (Status, Status) {
	tlsStatus := StatusPass
	if !tlsOK {
		tlsStatus = StatusFail
	}
	quicStatus := StatusPass
	if !quicOK {
		quicStatus = StatusFail
	}
	if mode == rstream.TunnelTransportModeAuto {
		if tlsOK && !quicOK {
			quicStatus = StatusWarn
		}
		if quicOK && !tlsOK {
			tlsStatus = StatusWarn
		}
	} else if mode == rstream.TunnelTransportModeTLS && !quicOK {
		quicStatus = StatusWarn
	} else if mode == rstream.TunnelTransportModeQUIC && !tlsOK {
		tlsStatus = StatusWarn
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

func (r *Report) add(name string, status Status, message string, details map[string]string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Message: message, Details: details})
}

func (r *Report) finalize() {
	r.Summary = Summary{}
	for _, check := range r.Checks {
		switch check.Status {
		case StatusPass:
			r.Summary.Pass++
		case StatusWarn:
			r.Summary.Warn++
		case StatusFail:
			r.Summary.Fail++
		case StatusSkip:
			r.Summary.Skip++
		}
	}
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

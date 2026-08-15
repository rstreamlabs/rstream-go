// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

type mcpLocalTunnelSession struct {
	ID                         string    `json:"id"`
	Name                       string    `json:"name"`
	TunnelID                   string    `json:"tunnel_id"`
	URL                        string    `json:"url"`
	Forwarded                  string    `json:"forwarded"`
	Host                       string    `json:"host"`
	Port                       string    `json:"port"`
	PID                        int       `json:"pid"`
	Publish                    bool      `json:"publish"`
	Protocol                   string    `json:"protocol,omitempty"`
	HTTPVersion                string    `json:"http_version,omitempty"`
	TokenAuth                  bool      `json:"token_auth"`
	RstreamAuth                bool      `json:"rstream_auth"`
	ChallengeMode              bool      `json:"challenge_mode"`
	MTLSAuth                   bool      `json:"mtls_auth"`
	UpstreamTLS                bool      `json:"upstream_tls"`
	DatagramGuaranteedDelivery bool      `json:"datagram_guaranteed_delivery"`
	CleanupTool                string    `json:"cleanup_tool"`
	CleanupID                  string    `json:"cleanup_id"`
	ConfigPath                 string    `json:"config_path,omitempty"`
	LogPath                    string    `json:"log_path,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
}

type mcpLocalTunnelRegistryFile struct {
	Sessions []mcpLocalTunnelSession `json:"local_tunnels"`
}

func mcpLocalTunnelExpose(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, err := resolveMCPRuntimeForArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	host, port, props, err := mcpLocalTunnelArgs(args, runtime.Resolved.StableDomainEndpoint())
	if err != nil {
		return nil, err
	}
	path, err := mcpLocalTunnelRegistryPath(runtime.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := mcpLocalTunnelPruneRegistry(path); err != nil {
		return nil, err
	}
	cmd, logPath, err := startMCPLocalTunnelProcess(runtime.ConfigPath, host, port, props)
	if err != nil {
		return nil, err
	}
	session, err := waitMCPLocalTunnelProcessOnline(ctx, cmd, logPath, props, host, port, runtime.ConfigPath)
	if err != nil {
		if cmd.Process != nil {
			_ = terminateMCPLocalTunnelProcess(cmd.Process.Pid)
		}
		return nil, err
	}
	if err := mcpLocalTunnelAddSession(path, *session); err != nil {
		_ = terminateMCPLocalTunnelProcess(session.PID)
		return nil, err
	}
	return mcpJSONResourceLinkResult(session, false, session.URL, session.Name, "Public rstream local tunnel URL", "text/html")
}

func mcpLocalTunnelList() (map[string]any, error) {
	path, err := mcpLocalTunnelRegistryPathFromConfig()
	if err != nil {
		return nil, err
	}
	sessions, err := mcpLocalTunnelListFromPath(path)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"local_tunnels": sessions}, false)
}

func mcpLocalTunnelStop(args map[string]json.RawMessage) (map[string]any, error) {
	id, err := mcpRequiredStringArg(args, "id")
	if err != nil {
		return nil, err
	}
	path, err := mcpLocalTunnelRegistryPathFromConfig()
	if err != nil {
		return nil, err
	}
	result, err := mcpLocalTunnelStopFromPath(path, id)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(result, false)
}

func mcpLocalTunnelExposeToolDescription() string {
	return "Expose a local service through a persistent rstream forward process managed by the local MCP tunnel registry. Supports the same tunnel option families as rstream forward for local services, including HTTP versions, token auth, rstream Auth, challenge mode, TLS/mTLS, Geo/IP filters, labels, stable domains, and publish/private mode. If the local runtime has no ready context, pass project, project_name, project_endpoint, or project_id instead of minting a token manually. Use the returned cleanup_tool and cleanup_id with the matching MCP stop tool to clean up MCP-managed local tunnels."
}

func mcpLocalTunnelExposeToolProperties() map[string]any {
	return map[string]any{
		"port":                         mcpStringSchema("Local port to expose, such as 3000."),
		"host":                         mcpStringSchema("Optional local host, defaults to localhost."),
		"name":                         mcpStringSchema("Optional tunnel name."),
		"publish":                      map[string]any{"type": "boolean", "description": "Publish the tunnel. Defaults to true. Set false for a private rstrm:// target."},
		"protocol":                     mcpStringEnumSchema("Optional protocol: http, http/1.1, h2c, h3, tls, dtls, quic, tcp, bytestream, udp, or datagram. Raw udp/datagram tunnels require publish=false; use dtls, quic, or h3 for published datagrams. Defaults to http.", []string{"http", "http/1.1", "h2c", "h3", "tls", "dtls", "quic", "tcp", "bytestream", "udp", "datagram"}),
		"tunnel_type":                  mcpStringEnumSchema("Optional raw tunnel type override. Use bytestream for TCP-like services or datagram for UDP-like services.", []string{"bytestream", "datagram"}),
		"stable_domain":                mcpStringSchema("Optional stable published host. Requires publish=true."),
		"tcp_port":                     map[string]any{"type": "number", "description": "Optional reserved public TCP port. Requires protocol=tcp and publish=true."},
		"token_auth":                   map[string]any{"type": "boolean", "description": "Require rstream token authentication at the HTTP edge. Requires an HTTP published tunnel."},
		"rstream_auth":                 map[string]any{"type": "boolean", "description": "Require rstream account authentication at the HTTP edge. Requires an HTTP published tunnel."},
		"challenge_mode":               map[string]any{"type": "boolean", "description": "Require an interactive browser challenge before HTTP edge access. Requires an HTTP published tunnel."},
		"mtls":                         map[string]any{"type": "boolean", "description": "Enable mTLS Tunnel access for published clients when supported by the selected project plan."},
		"http_version":                 mcpStringEnumSchema("Optional HTTP upstream/public mode when protocol is HTTP: http/1.1, h2c, or h3.", []string{"http/1.1", "h2c", "h3"}),
		"upstream_tls":                 map[string]any{"type": "boolean", "description": "Use TLS for the upstream side. For HTTP this also sets HTTP upstream TLS."},
		"datagram_guaranteed_delivery": map[string]any{"type": "boolean", "description": "Require reliable delivery for datagram tunnels."},
		"tls_mode":                     mcpStringEnumSchema("Optional TLS mode for TLS tunnels: terminated or passthrough.", []string{"terminated", "passthrough"}),
		"tls_alpns":                    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional TLS ALPN protocols."},
		"tls_min_version": mcpStringEnumSchema(
			"Optional minimum TLS version.",
			[]string{"tls1.2", "tls1.3"},
		),
		"tls_ciphers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional TLS cipher IDs."},
		"geoip":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional allowed country codes for published edge access."},
		"trusted_ips": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional allowed IP or CIDR ranges for published edge access."},
		"labels":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional rstream labels as key=value entries."},
	}
}

func mcpStringEnumSchema(description string, values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

func mcpLocalTunnelArgs(args map[string]json.RawMessage, engine string) (string, string, *rstream.TunnelProperties, error) {
	port, err := mcpRequiredStringArg(args, "port")
	if err != nil {
		return "", "", nil, err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", "", nil, fmt.Errorf("port must be numeric")
	}
	host, err := mcpOptionalStringArg(args, "host", "localhost")
	if err != nil {
		return "", "", nil, err
	}
	name, err := mcpOptionalStringArg(args, "name", "")
	if err != nil {
		return "", "", nil, err
	}
	if name == "" {
		name = fmt.Sprintf("codex-local-tunnel-%d", time.Now().Unix())
	}
	stableDomain, err := mcpOptionalStringArg(args, "stable_domain", "")
	if err != nil {
		return "", "", nil, err
	}
	publish, err := mcpOptionalBoolArg(args, "publish", true)
	if err != nil {
		return "", "", nil, err
	}
	props, err := mcpLocalTunnelProperties(args, name, stableDomain, port, publish)
	if err != nil {
		return "", "", nil, err
	}
	if err := rstream.MaybeSetGeneratedStableDomain(props, engine); err != nil {
		return "", "", nil, fmt.Errorf("failed to generate stable domain: %w", err)
	}
	return host, port, props, nil
}

func mcpLocalTunnelProperties(args map[string]json.RawMessage, name string, stableDomain string, port string, publish bool) (*rstream.TunnelProperties, error) {
	labels, err := mcpLocalTunnelLabels(args, port)
	if err != nil {
		return nil, err
	}
	props := &rstream.TunnelProperties{Name: &name, Publish: &publish, Labels: labels}
	if stableDomain != "" {
		props.Hostname = &stableDomain
	}
	if err := mcpLocalTunnelApplyProtocol(args, props); err != nil {
		return nil, err
	}
	if err := mcpLocalTunnelApplyTCPPort(args, props); err != nil {
		return nil, err
	}
	if err := mcpLocalTunnelApplySecurityOptions(args, props); err != nil {
		return nil, err
	}
	if err := mcpLocalTunnelValidateTunnelProperties(props); err != nil {
		return nil, err
	}
	return props, nil
}

func mcpLocalTunnelApplyTCPPort(args map[string]json.RawMessage, props *rstream.TunnelProperties) error {
	port, err := mcpOptionalIntArg(args, "tcp_port")
	if err != nil {
		return err
	}
	if port == nil {
		return nil
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("tcp_port must be between 1 and 65535")
	}
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolTCP || props.Publish == nil || !*props.Publish {
		return fmt.Errorf("tcp_port requires protocol=tcp and publish=true")
	}
	value := uint32(*port)
	props.Port = &value
	return nil
}

func mcpLocalTunnelLabels(args map[string]json.RawMessage, port string) (map[string]string, error) {
	labels, err := mcpOptionalLabelMap(args)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(labels)+3)
	for key, value := range labels {
		out[key] = value
	}
	out["application-protocol"] = "rstream.local_tunnel"
	out["rstream.local_tunnel.kind"] = "codex"
	out["rstream.local_tunnel.port"] = port
	return out, nil
}

func mcpLocalTunnelApplyProtocol(args map[string]json.RawMessage, props *rstream.TunnelProperties) error {
	rawProtocol, err := mcpOptionalStringArg(args, "protocol", "http")
	if err != nil {
		return err
	}
	tunnelType, err := mcpOptionalStringArg(args, "tunnel_type", "")
	if err != nil {
		return err
	}
	if tunnelType != "" {
		val, err := mcpLocalTunnelParseTunnelType(tunnelType)
		if err != nil {
			return err
		}
		props.Type = &val
	}
	if err := mcpLocalTunnelApplyProtocolValue(props, rawProtocol); err != nil {
		return err
	}
	httpVersion, err := mcpOptionalStringArg(args, "http_version", "")
	if err != nil {
		return err
	}
	if httpVersion != "" {
		val, err := parseForwardHTTPVersion(httpVersion)
		if err != nil {
			return err
		}
		props.HTTPVersion = &val
	}
	return mcpLocalTunnelValidateProtocolType(props)
}

func mcpLocalTunnelApplyProtocolValue(props *rstream.TunnelProperties, value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "http", "http/1.1":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP1_1)
	case "h2c", "http/2":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP2)
	case "h3", "http/3":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
	case "tls":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolTLS)
	case "dtls":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolDTLS)
	case "quic":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolQUIC)
	case "tcp":
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolTCP)
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
	case "bytestream":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
	case "udp", "datagram":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
	default:
		return fmt.Errorf("invalid protocol %q", value)
	}
	return nil
}

func mcpLocalTunnelParseTunnelType(value string) (rstream.TunnelType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(rstream.TunnelTypeBytestream):
		return rstream.TunnelTypeBytestream, nil
	case string(rstream.TunnelTypeDatagram):
		return rstream.TunnelTypeDatagram, nil
	default:
		return "", fmt.Errorf("invalid tunnel_type %q", value)
	}
}

func mcpLocalTunnelApplySecurityOptions(args map[string]json.RawMessage, props *rstream.TunnelProperties) error {
	tokenAuth, err := mcpOptionalBoolPtrArg(args, "token_auth")
	if err != nil {
		return err
	}
	rstreamAuth, err := mcpOptionalBoolPtrArg(args, "rstream_auth")
	if err != nil {
		return err
	}
	challengeMode, err := mcpOptionalBoolPtrArg(args, "challenge_mode")
	if err != nil {
		return err
	}
	mtlsAuth, err := mcpOptionalBoolPtrArg(args, "mtls")
	if err != nil {
		return err
	}
	upstreamTLS, err := mcpOptionalBoolPtrArg(args, "upstream_tls")
	if err != nil {
		return err
	}
	datagramGuaranteedDelivery, err := mcpOptionalBoolPtrArg(args, "datagram_guaranteed_delivery")
	if err != nil {
		return err
	}
	tlsMode, err := mcpOptionalStringArg(args, "tls_mode", "")
	if err != nil {
		return err
	}
	if tlsMode != "" {
		val, err := parseForwardTLSMode(tlsMode)
		if err != nil {
			return err
		}
		props.TLSMode = &val
	}
	props.TLSALPNs, err = mcpOptionalStringSliceArg(args, "tls_alpns")
	if err != nil {
		return err
	}
	props.TLSMinVersion, err = mcpOptionalStringPtrArg(args, "tls_min_version")
	if err != nil {
		return err
	}
	if props.TLSMinVersion != nil && *props.TLSMinVersion != "tls1.2" && *props.TLSMinVersion != "tls1.3" {
		return fmt.Errorf("invalid tls_min_version %q (valid: tls1.2, tls1.3)", *props.TLSMinVersion)
	}
	props.TLSCiphers, err = mcpOptionalStringSliceArg(args, "tls_ciphers")
	if err != nil {
		return err
	}
	props.GeoIP, err = mcpOptionalStringSliceArg(args, "geoip")
	if err != nil {
		return err
	}
	props.TrustedIPs, err = mcpOptionalStringSliceArg(args, "trusted_ips")
	if err != nil {
		return err
	}
	props.TokenAuth = tokenAuth
	props.RstreamAuth = rstreamAuth
	props.ChallengeMode = challengeMode
	props.MTLSAuth = mtlsAuth
	props.UpstreamTLS = upstreamTLS
	props.DatagramGuaranteedDelivery = datagramGuaranteedDelivery
	if upstreamTLS != nil && props.Protocol != nil && *props.Protocol == rstream.ProtocolHTTP {
		props.HTTPUseTLS = upstreamTLS
	}
	return nil
}

func mcpLocalTunnelValidateTunnelProperties(props *rstream.TunnelProperties) error {
	if props.Protocol != nil && *props.Protocol == rstream.ProtocolTCP {
		if props.Publish == nil || !*props.Publish {
			return fmt.Errorf("protocol=tcp requires publish=true")
		}
		if props.Hostname != nil || props.TLSMode != nil || len(props.TLSALPNs) > 0 || props.TLSMinVersion != nil || len(props.TLSCiphers) > 0 || props.MTLSAuth != nil || props.HTTPVersion != nil || props.HTTPUseTLS != nil || props.UpstreamTLS != nil || props.TokenAuth != nil || props.RstreamAuth != nil || props.ChallengeMode != nil || props.DatagramGuaranteedDelivery != nil {
			return fmt.Errorf("protocol=tcp does not accept stable_domain, HTTP, TLS, edge authentication, or datagram delivery options")
		}
	}
	if props.Publish != nil && !*props.Publish {
		if props.Hostname != nil && strings.TrimSpace(*props.Hostname) != "" {
			return fmt.Errorf("stable_domain requires publish=true")
		}
		if boolPtrValue(props.TokenAuth) || boolPtrValue(props.RstreamAuth) || boolPtrValue(props.ChallengeMode) || boolPtrValue(props.MTLSAuth) || len(props.GeoIP) > 0 || len(props.TrustedIPs) > 0 {
			return fmt.Errorf("edge authentication and access policy options require publish=true")
		}
	}
	if mcpLocalTunnelHasHTTPOptions(props) && (props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP) {
		return fmt.Errorf("HTTP options require protocol=http")
	}
	if props.TLSMode != nil && (props.Protocol == nil || *props.Protocol != rstream.ProtocolTLS) {
		return fmt.Errorf("tls_mode requires protocol=tls")
	}
	if boolPtrValue(props.DatagramGuaranteedDelivery) && !mcpLocalTunnelUsesDatagram(props) {
		return fmt.Errorf("datagram_guaranteed_delivery requires a datagram tunnel")
	}
	if props.Publish != nil && *props.Publish && props.Type != nil && *props.Type == rstream.TunnelTypeDatagram && props.Protocol == nil {
		return fmt.Errorf("protocol=udp/datagram requires publish=false or protocol=dtls, quic, or h3")
	}
	return nil
}

func mcpLocalTunnelHasHTTPOptions(props *rstream.TunnelProperties) bool {
	return props.HTTPVersion != nil || props.HTTPUseTLS != nil || boolPtrValue(props.TokenAuth) || boolPtrValue(props.RstreamAuth) || boolPtrValue(props.ChallengeMode)
}

func mcpLocalTunnelValidateProtocolType(props *rstream.TunnelProperties) error {
	if props.Type == nil || props.Protocol == nil {
		return nil
	}
	switch *props.Protocol {
	case rstream.ProtocolTLS:
		if *props.Type != rstream.TunnelTypeBytestream {
			return fmt.Errorf("protocol=tls requires tunnel_type=bytestream")
		}
	case rstream.ProtocolDTLS, rstream.ProtocolQUIC:
		if *props.Type != rstream.TunnelTypeDatagram {
			return fmt.Errorf("protocol=%s requires tunnel_type=datagram", *props.Protocol)
		}
	case rstream.ProtocolHTTP:
		if props.HTTPVersion != nil && *props.HTTPVersion == rstream.HTTP3 && *props.Type != rstream.TunnelTypeDatagram {
			return fmt.Errorf("http_version=h3 requires tunnel_type=datagram when tunnel_type is specified")
		}
		if props.HTTPVersion == nil || *props.HTTPVersion != rstream.HTTP3 {
			if *props.Type != rstream.TunnelTypeBytestream {
				return fmt.Errorf("HTTP/1.1 and h2c require tunnel_type=bytestream when tunnel_type is specified")
			}
		}
	}
	return nil
}

func mcpLocalTunnelUsesDatagram(props *rstream.TunnelProperties) bool {
	if props.Type != nil {
		return *props.Type == rstream.TunnelTypeDatagram
	}
	if props.Protocol == nil {
		return false
	}
	switch *props.Protocol {
	case rstream.ProtocolDTLS, rstream.ProtocolQUIC:
		return true
	case rstream.ProtocolHTTP:
		return props.HTTPVersion != nil && *props.HTTPVersion == rstream.HTTP3
	default:
		return false
	}
}

func boolPtrValue(value *bool) bool {
	return value != nil && *value
}

func startMCPLocalTunnelProcess(configPath string, host string, port string, props *rstream.TunnelProperties) (*exec.Cmd, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	registryPath, err := mcpLocalTunnelRegistryPath(configPath)
	if err != nil {
		return nil, "", err
	}
	logDir := filepath.Join(filepath.Dir(registryPath), "mcp-local-tunnels")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%d.log", safeMCPLocalTunnelFilePart(statusString(props.Name)), time.Now().UnixNano()))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer logFile.Close()
	cmd := exec.Command(executable, mcpLocalTunnelForwardArgs(host, port, props)...)
	cmd.Env = append(os.Environ(), "RSTREAM_CONFIG="+configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureMCPLocalTunnelProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	return cmd, logPath, nil
}

func mcpLocalTunnelForwardArgs(host string, port string, props *rstream.TunnelProperties) []string {
	args := []string{"forward", net.JoinHostPort(host, port), "--output", "json", "--name", statusString(props.Name)}
	args = append(args, forwardArgsFromTunnelProperties(props)...)
	keys := make([]string, 0, len(props.Labels))
	for key := range props.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+props.Labels[key])
	}
	return args
}

func forwardArgsFromTunnelProperties(props *rstream.TunnelProperties) []string {
	args := []string{}
	if props.Publish != nil && !*props.Publish {
		args = append(args, "--no-publish")
		return append(args, privateForwardArgsFromTunnelProperties(props)...)
	} else {
		args = append(args, "--publish")
	}
	switch {
	case props.Protocol != nil && *props.Protocol == rstream.ProtocolTLS:
		args = append(args, "--tls")
	case props.Protocol != nil && *props.Protocol == rstream.ProtocolTCP:
		args = append(args, "--tcp")
		if props.Port != nil {
			args = append(args, "--tcp-port", strconv.FormatUint(uint64(*props.Port), 10))
		}
	case props.Protocol != nil && *props.Protocol == rstream.ProtocolDTLS:
		args = append(args, "--dtls")
	case props.Protocol != nil && *props.Protocol == rstream.ProtocolQUIC:
		args = append(args, "--quic")
	case props.Protocol != nil && *props.Protocol == rstream.ProtocolHTTP:
		args = append(args, "--http")
		if props.HTTPVersion != nil {
			args = append(args, "--http-version", string(*props.HTTPVersion))
		}
	case props.Type != nil && *props.Type == rstream.TunnelTypeDatagram:
		args = append(args, "--datagram")
	default:
		args = append(args, "--bytestream")
	}
	if props.Hostname != nil && strings.TrimSpace(*props.Hostname) != "" {
		args = append(args, "--host", *props.Hostname)
	}
	if props.TLSMode != nil {
		args = append(args, "--tls-mode", string(*props.TLSMode))
	}
	if len(props.TLSALPNs) > 0 {
		args = append(args, "--tls-alpn", strings.Join(props.TLSALPNs, ","))
	}
	if props.TLSMinVersion != nil && strings.TrimSpace(*props.TLSMinVersion) != "" {
		args = append(args, "--tls-min-version", *props.TLSMinVersion)
	}
	if len(props.TLSCiphers) > 0 {
		args = append(args, "--tls-ciphers", strings.Join(props.TLSCiphers, ","))
	}
	if boolPtrValue(props.MTLSAuth) {
		args = append(args, "--mtls")
	}
	if boolPtrValue(props.UpstreamTLS) {
		args = append(args, "--upstream-tls")
	}
	if boolPtrValue(props.DatagramGuaranteedDelivery) {
		args = append(args, "--datagram-guaranteed-delivery")
	}
	if boolPtrValue(props.TokenAuth) {
		args = append(args, "--token-auth")
	}
	if boolPtrValue(props.RstreamAuth) {
		args = append(args, "--rstream-auth")
	}
	if boolPtrValue(props.ChallengeMode) {
		args = append(args, "--challenge-mode")
	}
	if len(props.GeoIP) > 0 {
		args = append(args, "--geoip", strings.Join(props.GeoIP, ","))
	}
	if len(props.TrustedIPs) > 0 {
		args = append(args, "--trusted-ips", strings.Join(props.TrustedIPs, ","))
	}
	return args
}

func privateForwardArgsFromTunnelProperties(props *rstream.TunnelProperties) []string {
	args := []string{}
	if props.Type != nil && *props.Type == rstream.TunnelTypeDatagram {
		args = append(args, "--datagram")
		if boolPtrValue(props.DatagramGuaranteedDelivery) {
			args = append(args, "--datagram-guaranteed-delivery")
		}
		return args
	}
	if props.Protocol != nil {
		switch *props.Protocol {
		case rstream.ProtocolDTLS, rstream.ProtocolQUIC:
			args = append(args, "--datagram")
			if boolPtrValue(props.DatagramGuaranteedDelivery) {
				args = append(args, "--datagram-guaranteed-delivery")
			}
			return args
		case rstream.ProtocolHTTP:
			if props.HTTPVersion != nil && *props.HTTPVersion == rstream.HTTP3 {
				args = append(args, "--datagram")
				if boolPtrValue(props.DatagramGuaranteedDelivery) {
					args = append(args, "--datagram-guaranteed-delivery")
				}
				return args
			}
		}
	}
	return []string{"--bytestream"}
}

func waitMCPLocalTunnelProcessOnline(ctx context.Context, cmd *exec.Cmd, logPath string, props *rstream.TunnelProperties, host string, port string, configPath string) (*mcpLocalTunnelSession, error) {
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		session, statusErr := readMCPLocalTunnelOnlineStatus(logPath, props, host, port, configPath, cmd.Process.Pid)
		if session != nil || statusErr != nil {
			return session, statusErr
		}
		select {
		case err := <-exitCh:
			if err == nil {
				return nil, fmt.Errorf("local tunnel stopped before becoming online")
			}
			return nil, fmt.Errorf("local tunnel failed before becoming online: %w", err)
		case <-time.After(200 * time.Millisecond):
		case <-timeout.C:
			return nil, fmt.Errorf("timed out waiting for local tunnel")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func readMCPLocalTunnelOnlineStatus(logPath string, props *rstream.TunnelProperties, host string, port string, configPath string, pid int) (*mcpLocalTunnelSession, error) {
	file, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var status forwardStatus
		if err := json.Unmarshal(scanner.Bytes(), &status); err != nil {
			continue
		}
		if status.Status != nil && strings.Contains(*status.Status, "failed") {
			return nil, fmt.Errorf("%s", *status.Status)
		}
		if status.Status != nil && *status.Status == "online" && status.Forwarding != nil && status.TunnelID != nil {
			return mcpLocalTunnelSessionFromStatus(*status.TunnelID, *status.Forwarding, statusString(status.Forwarded), props, host, port, configPath, logPath, pid), nil
		}
	}
	return nil, scanner.Err()
}

func mcpLocalTunnelSessionFromStatus(tunnelID string, forwarding string, forwarded string, props *rstream.TunnelProperties, host string, port string, configPath string, logPath string, pid int) *mcpLocalTunnelSession {
	return &mcpLocalTunnelSession{ID: tunnelID, Name: statusString(props.Name), TunnelID: tunnelID, URL: forwarding, Forwarded: forwarded, Host: host, Port: port, PID: pid, Publish: props.Publish == nil || *props.Publish, Protocol: localTunnelProtocolValue(props), HTTPVersion: localTunnelHTTPVersionValue(props), TokenAuth: boolPtrValue(props.TokenAuth), RstreamAuth: boolPtrValue(props.RstreamAuth), ChallengeMode: boolPtrValue(props.ChallengeMode), MTLSAuth: boolPtrValue(props.MTLSAuth), UpstreamTLS: boolPtrValue(props.UpstreamTLS), DatagramGuaranteedDelivery: boolPtrValue(props.DatagramGuaranteedDelivery), CleanupTool: "rstream_local_tunnel_stop", CleanupID: tunnelID, ConfigPath: configPath, LogPath: logPath, CreatedAt: time.Now().UTC()}
}

func localTunnelProtocolValue(props *rstream.TunnelProperties) string {
	if props.Protocol != nil {
		return string(*props.Protocol)
	}
	if props.Type != nil {
		return string(*props.Type)
	}
	return ""
}

func localTunnelHTTPVersionValue(props *rstream.TunnelProperties) string {
	if props.HTTPVersion == nil {
		return ""
	}
	return string(*props.HTTPVersion)
}

func mcpLocalTunnelRegistryPathFromConfig() (string, error) {
	path, _, err := mcpLoadConfig()
	if err != nil {
		return "", err
	}
	return mcpLocalTunnelRegistryPath(path)
}

func mcpLocalTunnelRegistryPath(configPath string) (string, error) {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.DefaultConfigPath()
		if err != nil {
			return "", err
		}
		configPath = path
	}
	return filepath.Join(filepath.Dir(configPath), "mcp-local-tunnels.json"), nil
}

func mcpLocalTunnelListFromPath(path string) ([]mcpLocalTunnelSession, error) {
	if err := mcpLocalTunnelPruneRegistry(path); err != nil {
		return nil, err
	}
	return readMCPLocalTunnelRegistry(path)
}

func mcpLocalTunnelStopFromPath(path string, id string) (map[string]any, error) {
	var matched *mcpLocalTunnelSession
	if err := updateMCPLocalTunnelRegistry(path, func(sessions []mcpLocalTunnelSession) ([]mcpLocalTunnelSession, error) {
		next := make([]mcpLocalTunnelSession, 0, len(sessions))
		for _, session := range sessions {
			if session.ID == id || session.TunnelID == id {
				copy := session
				matched = &copy
				continue
			}
			next = append(next, session)
		}
		return next, nil
	}); err != nil {
		return nil, err
	}
	if matched == nil {
		return nil, fmt.Errorf("local tunnel %q not found", id)
	}
	stopped := false
	if mcpLocalTunnelProcessRunning(matched.PID) {
		if err := terminateMCPLocalTunnelProcess(matched.PID); err != nil {
			return nil, err
		}
		stopped = true
	}
	return map[string]any{"stopped": stopped, "id": matched.ID, "tunnel_id": matched.TunnelID, "pid": matched.PID}, nil
}

func mcpLocalTunnelAddSession(path string, session mcpLocalTunnelSession) error {
	return updateMCPLocalTunnelRegistry(path, func(sessions []mcpLocalTunnelSession) ([]mcpLocalTunnelSession, error) {
		next := make([]mcpLocalTunnelSession, 0, len(sessions)+1)
		for _, existing := range sessions {
			if existing.ID != session.ID && existing.TunnelID != session.TunnelID && mcpLocalTunnelProcessRunning(existing.PID) {
				next = append(next, existing)
			}
		}
		next = append(next, session)
		return next, nil
	})
}

func mcpLocalTunnelPruneRegistry(path string) error {
	return updateMCPLocalTunnelRegistry(path, func(sessions []mcpLocalTunnelSession) ([]mcpLocalTunnelSession, error) {
		next := make([]mcpLocalTunnelSession, 0, len(sessions))
		for _, session := range sessions {
			if mcpLocalTunnelProcessRunning(session.PID) {
				next = append(next, session)
			}
		}
		return next, nil
	})
}

func readMCPLocalTunnelRegistry(path string) ([]mcpLocalTunnelSession, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []mcpLocalTunnelSession{}, nil
		}
		return nil, err
	}
	defer file.Close()
	var data mcpLocalTunnelRegistryFile
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, err
	}
	return data.Sessions, nil
}

func updateMCPLocalTunnelRegistry(path string, update func([]mcpLocalTunnelSession) ([]mcpLocalTunnelSession, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	sessions, err := readMCPLocalTunnelRegistry(path)
	if err != nil {
		return err
	}
	next, err := update(sessions)
	if err != nil {
		return err
	}
	return writeMCPLocalTunnelRegistry(path, next)
}

func writeMCPLocalTunnelRegistry(path string, sessions []mcpLocalTunnelSession) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-local-tunnels-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(mcpLocalTunnelRegistryFile{Sessions: sessions}); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func statusString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func safeMCPLocalTunnelFilePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "local-tunnel"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "local-tunnel"
	}
	return b.String()
}

// See LICENSE file in the project root for license information.

package rundocker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

type ContainerInfo struct {
	ID       string
	Name     string
	Labels   map[string]string
	Networks map[string]string
}

func (c ContainerInfo) FirstIP() string {
	if len(c.Networks) == 0 {
		return ""
	}
	keys := make([]string, 0, len(c.Networks))
	for k := range c.Networks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ip := strings.TrimSpace(c.Networks[k]); ip != "" {
			return ip
		}
	}
	return ""
}

func ParseDesiredTunnels(info ContainerInfo, network string, ctx runmodel.ResolvedContext) ([]runmodel.DesiredTunnel, error) {
	labelGroups := map[string]*labelSpec{}
	for key, val := range info.Labels {
		if !strings.HasPrefix(key, "rstream.tunnel.") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(key, "rstream.tunnel."), ".", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		spec := labelGroups[name]
		if spec == nil {
			spec = &labelSpec{}
			labelGroups[name] = spec
		}
		if err := spec.apply(parts[1], val); err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", name, err)
		}
	}
	if len(labelGroups) == 0 {
		return nil, nil
	}
	out := make([]runmodel.DesiredTunnel, 0, len(labelGroups))
	for name, spec := range labelGroups {
		if strings.TrimSpace(spec.forward) == "" {
			return nil, fmt.Errorf("tunnel %q missing forward label", name)
		}
		props := spec.props
		if props.Publish == nil {
			props.Publish = rstream.BoolPtr(true)
		}
		if props.Protocol == nil {
			proto := rstream.ProtocolHTTP
			props.Protocol = &proto
		}
		forward, err := resolveForward(spec.forward, info, network)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q forward: %w", name, err)
		}
		fullName := runmodel.SanitizeName(fmt.Sprintf("%s-%s", info.Name, name))
		if fullName == "" {
			fullName = runmodel.SanitizeName(fmt.Sprintf("%s-%s", info.ID, name))
		}
		props.Name = &fullName
		runmodel.ApplyManagedLabels(&props, "docker")
		out = append(out, runmodel.DesiredTunnel{
			Name:    fullName,
			Forward: forward,
			Context: ctx,
			Props:   props,
			Source:  "docker",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type labelSpec struct {
	forward string
	props   rstream.TunnelProperties
	seen    map[string]bool
}

func (l *labelSpec) apply(key, value string) error {
	if l.seen == nil {
		l.seen = make(map[string]bool)
	}
	switch {
	case key == "forward":
		l.forward = strings.TrimSpace(value)
		return nil
	case key == "publish":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.Publish = &v
		return nil
	case key == "protocol":
		proto, err := parseProtocol(value)
		if err != nil {
			return err
		}
		l.props.Protocol = &proto
		return nil
	case key == "type":
		val, err := parseTunnelType(value)
		if err != nil {
			return err
		}
		l.props.Type = &val
		return nil
	case key == "host":
		v := strings.TrimSpace(value)
		if v != "" {
			l.props.Host = &v
		}
		return nil
	case key == "trusted-ips":
		l.props.TrustedIPs = splitCSV(value)
		return nil
	case key == "geoip":
		l.props.GeoIP = splitCSV(value)
		return nil
	case strings.HasPrefix(key, "label."):
		labelKey := strings.TrimPrefix(key, "label.")
		if strings.TrimSpace(labelKey) == "" {
			return fmt.Errorf("label key is empty")
		}
		if l.props.Labels == nil {
			l.props.Labels = make(map[string]string)
		}
		l.props.Labels[labelKey] = value
		return nil
	case strings.HasPrefix(key, "http."):
		return l.applyHTTP(strings.TrimPrefix(key, "http."), value)
	case strings.HasPrefix(key, "tls."):
		return l.applyTLS(strings.TrimPrefix(key, "tls."), value)
	case strings.HasPrefix(key, "auth."):
		return l.applyAuth(strings.TrimPrefix(key, "auth."), value)
	default:
		return fmt.Errorf("unknown label %q", key)
	}
}

func (l *labelSpec) applyHTTP(key, value string) error {
	switch key {
	case "version":
		val, err := parseHTTPVersion(value)
		if err != nil {
			return err
		}
		l.props.HTTPVersion = &val
		return nil
	case "upstreamTLS":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.HTTPUseTLS = &v
		return nil
	case "tokenAuth":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		return l.setTokenAuth(v)
	default:
		return fmt.Errorf("unknown http label %q", key)
	}
}

func (l *labelSpec) applyTLS(key, value string) error {
	switch key {
	case "mode":
		val, err := parseTLSMode(value)
		if err != nil {
			return err
		}
		l.props.TLSMode = &val
		return nil
	case "minVersion":
		val, err := parseTLSMinVersion(value)
		if err != nil {
			return err
		}
		l.props.TLSMinVersion = &val
		return nil
	case "alpns":
		l.props.TLSALPNs = splitCSV(value)
		return nil
	case "mtls":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.MTLS = &v
		return nil
	case "mtlsCACertFile":
		pem, err := os.ReadFile(filepath.Clean(value))
		if err != nil {
			return fmt.Errorf("read mtlsCACertFile: %w", err)
		}
		val := string(pem)
		l.props.MTLSCACertPEM = &val
		return nil
	default:
		return fmt.Errorf("unknown tls label %q", key)
	}
}

func (l *labelSpec) applyAuth(key, value string) error {
	switch key {
	case "token":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		return l.setTokenAuth(v)
	case "rstream":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.RstreamAuth = &v
		return nil
	case "challenge":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.ChallengeMode = &v
		return nil
	default:
		return fmt.Errorf("unknown auth label %q", key)
	}
}

func (l *labelSpec) setTokenAuth(v bool) error {
	if l.seen["tokenAuth"] {
		if l.props.TokenAuth != nil && *l.props.TokenAuth != v {
			return fmt.Errorf("conflicting token auth labels")
		}
		return nil
	}
	l.seen["tokenAuth"] = true
	l.props.TokenAuth = &v
	return nil
}

func resolveForward(raw string, info ContainerInfo, network string) (runmodel.ForwardTarget, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return runmodel.ForwardTarget{}, fmt.Errorf("forward is empty")
	}
	if isBarePort(value) {
		host := resolveContainerHost(info, network)
		if host == "" {
			return runmodel.ForwardTarget{}, fmt.Errorf("unable to resolve container host")
		}
		return runmodel.ParseForwardTarget(fmt.Sprintf("%s:%s", host, value), host)
	}
	return runmodel.ParseForwardTarget(value, "")
}

func resolveContainerHost(info ContainerInfo, network string) string {
	if network != "" {
		if ip := strings.TrimSpace(info.Networks[network]); ip != "" {
			return ip
		}
	}
	if network == "" {
		if name := strings.TrimSpace(info.Name); name != "" {
			return name
		}
	}
	if ip := info.FirstIP(); ip != "" {
		return ip
	}
	if name := strings.TrimSpace(info.Name); name != "" {
		return name
	}
	return ""
}

func isBarePort(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func splitCSV(val string) []string {
	parts := strings.Split(val, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseBool(val string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "t", "true", "yes", "y":
		return true, nil
	case "0", "f", "false", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", val)
	}
}

func parseProtocol(val string) (rstream.Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.ProtocolHTTP):
		return rstream.ProtocolHTTP, nil
	case string(rstream.ProtocolTLS):
		return rstream.ProtocolTLS, nil
	case string(rstream.ProtocolDTLS):
		return rstream.ProtocolDTLS, nil
	case string(rstream.ProtocolQUIC):
		return rstream.ProtocolQUIC, nil
	default:
		return "", fmt.Errorf("invalid protocol %q", val)
	}
}

func parseTunnelType(val string) (rstream.TunnelType, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.TunnelTypeBytestream):
		return rstream.TunnelTypeBytestream, nil
	case string(rstream.TunnelTypeDatagram):
		return rstream.TunnelTypeDatagram, nil
	default:
		return "", fmt.Errorf("invalid tunnel type %q", val)
	}
}

func parseHTTPVersion(val string) (rstream.HTTPVersion, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.HTTP1_1):
		return rstream.HTTP1_1, nil
	case string(rstream.HTTP2):
		return rstream.HTTP2, nil
	case string(rstream.HTTP3):
		return rstream.HTTP3, nil
	default:
		return "", fmt.Errorf("invalid http version %q", val)
	}
}

func parseTLSMode(val string) (rstream.TLSMode, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.TLSModeTerminated):
		return rstream.TLSModeTerminated, nil
	case string(rstream.TLSModePassthrough):
		return rstream.TLSModePassthrough, nil
	default:
		return "", fmt.Errorf("invalid tls mode %q", val)
	}
}

func parseTLSMinVersion(val string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "tls1.2", "tls1.3":
		return strings.ToLower(strings.TrimSpace(val)), nil
	default:
		return "", fmt.Errorf("invalid tls min version %q", val)
	}
}

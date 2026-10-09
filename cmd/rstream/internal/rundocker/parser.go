// See LICENSE file in the project root for license information.

package rundocker

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/tunnelconfig"
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
		props, err := tunnelconfig.Resolve(props)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", name, err)
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
}

func (l *labelSpec) apply(key, value string) error {
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
		proto, err := tunnelconfig.ParseProtocol(value)
		if err != nil {
			return err
		}
		l.props.Protocol = &proto
		return nil
	case key == "type":
		val, err := tunnelconfig.ParseTunnelType(value)
		if err != nil {
			return err
		}
		l.props.Type = &val
		return nil
	case key == "host":
		v := strings.TrimSpace(value)
		l.props.Hostname = &v
		return nil
	case key == "port":
		port, err := parsePort(value)
		if err != nil {
			return err
		}
		l.props.Port = &port
		return nil
	case key == "allow-cross-region-routing":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.AllowCrossRegionRouting = &v
		return nil
	case key == "upstream-tls":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.UpstreamTLS = &v
		return nil
	case key == "datagram-guaranteed-delivery":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.DatagramGuaranteedDelivery = &v
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
	default:
		return fmt.Errorf("unknown label %q", key)
	}
}

func (l *labelSpec) applyHTTP(key, value string) error {
	switch {
	case key == "version":
		val, err := tunnelconfig.ParseHTTPVersion(value)
		if err != nil {
			return err
		}
		l.props.HTTPVersion = &val
		return nil
	case strings.HasPrefix(key, "auth."):
		return l.applyHTTPAuth(strings.TrimPrefix(key, "auth."), value)
	case strings.HasPrefix(key, "gate."):
		return l.applyHTTPGate(strings.TrimPrefix(key, "gate."), value)
	default:
		return fmt.Errorf("unknown http label %q", key)
	}
}

func (l *labelSpec) applyTLS(key, value string) error {
	switch key {
	case "mode":
		val, err := tunnelconfig.ParseTLSMode(value)
		if err != nil {
			return err
		}
		l.props.TLSMode = &val
		return nil
	case "minVersion":
		val, err := tunnelconfig.ParseTLSMinVersion(value)
		if err != nil {
			return err
		}
		l.props.TLSMinVersion = &val
		return nil
	case "ciphers":
		l.props.TLSCiphers = splitCSV(value)
		return nil
	case "alpns":
		l.props.TLSALPNs = splitCSV(value)
		return nil
	case "mtls":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.MTLSAuth = &v
		return nil
	default:
		return fmt.Errorf("unknown tls label %q", key)
	}
}

func (l *labelSpec) applyHTTPAuth(key, value string) error {
	switch key {
	case "token":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.TokenAuth = &v
		return nil
	case "rstream":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.RstreamAuth = &v
		return nil
	default:
		return fmt.Errorf("unknown http.auth label %q", key)
	}
}

func (l *labelSpec) applyHTTPGate(key, value string) error {
	switch key {
	case "challenge":
		v, err := parseBool(value)
		if err != nil {
			return err
		}
		l.props.ChallengeMode = &v
		return nil
	default:
		return fmt.Errorf("unknown http.gate label %q", key)
	}
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
		return runmodel.ParseForwardTarget(value, host)
	}
	target, err := runmodel.ParseForwardTarget(value, "")
	if err != nil {
		return runmodel.ForwardTarget{}, err
	}
	if !dockerForwardHostAllowed(target.Host, info, network) {
		return runmodel.ForwardTarget{}, fmt.Errorf("host %q is outside the discovered container network; use a bare port label or --apply for host/internal targets", target.Host)
	}
	return target, nil
}

func resolveContainerHost(info ContainerInfo, network string) string {
	if network != "" {
		if ip := strings.TrimSpace(info.Networks[network]); ip != "" {
			return ip
		}
	}
	return info.FirstIP()
}

func dockerForwardHostAllowed(host string, info ContainerInfo, network string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return false
	}
	allowed := map[string]struct{}{}
	addAllowed := func(value string) {
		value = strings.Trim(strings.TrimSpace(value), "[]")
		if value != "" {
			allowed[value] = struct{}{}
		}
	}
	addAllowed(resolveContainerHost(info, network))
	for _, ip := range info.Networks {
		addAllowed(ip)
	}
	_, ok := allowed[host]
	return ok
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

func parsePort(value string) (uint32, error) {
	port, err := strconv.ParseUint(strings.TrimSpace(value), 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid TCP port %q", value)
	}
	return uint32(port), nil
}

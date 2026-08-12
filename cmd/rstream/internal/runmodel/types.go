// See LICENSE file in the project root for license information.

package runmodel

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"unicode"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const (
	ManagedByLabel = "rstream.managed-by"
	SourceLabel    = "rstream.source"
)

type ForwardTarget struct {
	Host string
	Port string
}

func (f ForwardTarget) String() string {
	return net.JoinHostPort(f.Host, f.Port)
}

func ParseForwardTarget(raw, defaultHost string) (ForwardTarget, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ForwardTarget{}, fmt.Errorf("forward target is empty")
	}
	host := defaultHost
	port := clean
	if strings.Contains(clean, ":") {
		parts := strings.SplitN(clean, ":", 2)
		if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return ForwardTarget{}, fmt.Errorf("invalid forward target %q", raw)
		}
		host = strings.TrimSpace(parts[0])
		port = strings.TrimSpace(parts[1])
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return ForwardTarget{}, fmt.Errorf("invalid forward target %q", raw)
	}
	return ForwardTarget{Host: host, Port: port}, nil
}

type ResolvedContext struct {
	Name               string
	Engine             string
	StableDomainEngine string
	Token              string
	Transport          rstream.Dialer
	TransportConfig    *config.TransportConfig
}

func (r ResolvedContext) StableDomainEndpoint() string {
	if value := strings.TrimSpace(r.StableDomainEngine); value != "" {
		return value
	}
	return r.Engine
}

type DesiredTunnel struct {
	Name    string
	Forward ForwardTarget
	Context ResolvedContext
	Props   rstream.TunnelProperties
	Source  string
}

func EqualDesired(a, b DesiredTunnel) bool {
	a.Context.Transport = nil
	b.Context.Transport = nil
	return reflect.DeepEqual(a, b)
}

func CloneDesired(d DesiredTunnel) DesiredTunnel {
	d.Context.TransportConfig = config.MergeTransport(d.Context.TransportConfig, nil)
	d.Props = cloneTunnelProperties(d.Props)
	return d
}

func cloneTunnelProperties(props rstream.TunnelProperties) rstream.TunnelProperties {
	clone := props
	clone.ID = clonePtr(props.ID)
	clone.CreationDate = clonePtr(props.CreationDate)
	clone.Name = clonePtr(props.Name)
	clone.Type = clonePtr(props.Type)
	clone.Publish = clonePtr(props.Publish)
	clone.Protocol = clonePtr(props.Protocol)
	clone.Labels = cloneMap(props.Labels)
	clone.GeoIP = cloneSlice(props.GeoIP)
	clone.TrustedIPs = cloneSlice(props.TrustedIPs)
	clone.Host = clonePtr(props.Host)
	clone.Hostname = clonePtr(props.Hostname)
	clone.Port = clonePtr(props.Port)
	clone.TLSMode = clonePtr(props.TLSMode)
	clone.TLSALPNs = cloneSlice(props.TLSALPNs)
	clone.TLSMinVersion = clonePtr(props.TLSMinVersion)
	clone.TLSCiphers = cloneSlice(props.TLSCiphers)
	clone.MTLSAuth = clonePtr(props.MTLSAuth)
	clone.HTTPVersion = clonePtr(props.HTTPVersion)
	clone.HTTPUseTLS = clonePtr(props.HTTPUseTLS)
	clone.UpstreamTLS = clonePtr(props.UpstreamTLS)
	clone.DatagramGuaranteedDelivery = clonePtr(props.DatagramGuaranteedDelivery)
	clone.AllowCrossRegionRouting = clonePtr(props.AllowCrossRegionRouting)
	clone.TokenAuth = clonePtr(props.TokenAuth)
	clone.RstreamAuth = clonePtr(props.RstreamAuth)
	clone.ChallengeMode = clonePtr(props.ChallengeMode)
	return clone
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneMap[K comparable, V any](value map[K]V) map[K]V {
	if value == nil {
		return nil
	}
	clone := make(map[K]V, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func cloneSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append(make([]T, 0, len(value)), value...)
}

type Handle interface {
	Stop() error
}

func ApplyManagedLabels(props *rstream.TunnelProperties, source string) {
	if props == nil {
		return
	}
	if props.Labels == nil {
		props.Labels = make(map[string]string)
	}
	props.Labels[ManagedByLabel] = "run"
	if strings.TrimSpace(source) != "" {
		props.Labels[SourceLabel] = source
	}
}

func SanitizeName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(clean))
	for _, r := range clean {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func Summary(d DesiredTunnel) map[string]any {
	summary := map[string]any{
		"name":    d.Name,
		"forward": d.Forward.String(),
		"engine":  d.Context.Engine,
	}
	if d.Props.Protocol != nil {
		summary["protocol"] = *d.Props.Protocol
	}
	if d.Props.Type != nil {
		summary["type"] = *d.Props.Type
	}
	if d.Props.Publish != nil {
		summary["publish"] = *d.Props.Publish
	}
	return summary
}

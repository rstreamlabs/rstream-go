// See LICENSE file in the project root for license information.

package runmodel

import (
	"fmt"
	"net"
	"strings"
	"unicode"

	"github.com/rstreamlabs/rstream-go"
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
	Name      string
	Engine    string
	Token     string
	Transport *rstream.Transport
}

type DesiredTunnel struct {
	Name    string
	Forward ForwardTarget
	Context ResolvedContext
	Props   rstream.TunnelProperties
	Source  string
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

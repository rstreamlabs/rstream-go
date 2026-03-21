// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	rstream "github.com/rstreamlabs/rstream-go"
)

type netcatEndpointKind string

const (
	netcatEndpointTCP     netcatEndpointKind = "tcp"
	netcatEndpointRstream netcatEndpointKind = "rstream"
)

type netcatDialTarget struct {
	Kind    netcatEndpointKind
	Address string
}

type netcatListenTarget struct {
	Kind    netcatEndpointKind
	Address string
	Name    *string
}

func (t netcatDialTarget) String() string {
	if t.Kind == netcatEndpointRstream {
		return "rstrm://" + t.Address
	}
	return t.Address
}

func (t netcatListenTarget) String() string {
	if t.Kind == netcatEndpointRstream {
		if t.Name == nil {
			return "rstrm://"
		}
		return "rstrm://" + *t.Name
	}
	return t.Address
}

func parseNetcatDialTarget(raw string) (netcatDialTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netcatDialTarget{}, fmt.Errorf("remote endpoint is required")
	}
	if !strings.Contains(raw, "://") {
		return netcatDialTarget{Kind: netcatEndpointTCP, Address: raw}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return netcatDialTarget{}, fmt.Errorf("invalid endpoint %q: %w", raw, err)
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "rstrm" {
		return netcatDialTarget{}, fmt.Errorf("invalid endpoint scheme %q (expected host:port or rstrm://<id-or-name>)", u.Scheme)
	}
	host, err := parseNetcatRstreamAuthority(u, false)
	if err != nil {
		return netcatDialTarget{}, err
	}
	return netcatDialTarget{Kind: netcatEndpointRstream, Address: host}, nil
}

func parseNetcatListenTarget(raw string) (netcatListenTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netcatListenTarget{}, fmt.Errorf("--listen requires an endpoint")
	}
	if !strings.Contains(raw, "://") {
		return netcatListenTarget{Kind: netcatEndpointTCP, Address: raw}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return netcatListenTarget{}, fmt.Errorf("invalid listen endpoint %q: %w", raw, err)
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "rstrm" {
		return netcatListenTarget{}, fmt.Errorf("invalid listen endpoint scheme %q (expected host:port or rstrm://[name])", u.Scheme)
	}
	host, err := parseNetcatRstreamAuthority(u, true)
	if err != nil {
		return netcatListenTarget{}, err
	}
	out := netcatListenTarget{Kind: netcatEndpointRstream}
	if host != "" {
		out.Name = &host
	}
	return out, nil
}

func parseNetcatRstreamAuthority(u *url.URL, allowEmpty bool) (string, error) {
	if u == nil {
		return "", errors.New("endpoint is required")
	}
	if u.User != nil {
		return "", fmt.Errorf("rstream endpoints do not support user info")
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("rstream endpoints do not support query parameters")
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("rstream endpoints do not support fragments")
	}
	value := strings.TrimSpace(u.Host)
	if value == "" {
		path := strings.TrimSpace(u.EscapedPath())
		switch path {
		case "", "/":
		default:
			if strings.Count(path, "/") != 1 {
				return "", fmt.Errorf("invalid rstream endpoint path %q", u.Path)
			}
			value = strings.TrimPrefix(path, "/")
		}
	} else if path := strings.TrimSpace(u.EscapedPath()); path != "" && path != "/" {
		return "", fmt.Errorf("invalid rstream endpoint path %q", u.Path)
	}
	if value == "" && !allowEmpty {
		return "", fmt.Errorf("rstream endpoint requires a tunnel id or name")
	}
	if value != "" && strings.Contains(value, "/") {
		return "", fmt.Errorf("invalid rstream endpoint %q", u.String())
	}
	return value, nil
}

func newNetcatDialer(target netcatDialTarget, client *rstream.Client) netcatDialer {
	switch target.Kind {
	case netcatEndpointRstream:
		return func(ctx context.Context) (net.Conn, error) {
			if client == nil {
				return nil, fmt.Errorf("rstream client is required")
			}
			return client.Dial(ctx, rstream.Addr{IdOrName: target.Address})
		}
	default:
		return func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", target.Address)
		}
	}
}

func newNetcatListenerFactory(target netcatListenTarget, client *rstream.Client) netcatListenerFactory {
	switch target.Kind {
	case netcatEndpointRstream:
		return func(ctx context.Context) (*netcatListenerResult, error) {
			if client == nil {
				return nil, fmt.Errorf("rstream client is required")
			}
			ctrl, err := client.Connect(ctx, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to rstream engine server: %w", err)
			}
			tunnelType := rstream.TunnelTypeBytestream
			props := rstream.TunnelProperties{
				Name:    target.Name,
				Type:    &tunnelType,
				Publish: rstream.BoolPtr(false),
			}
			tunnel, err := ctrl.CreateTunnel(ctx, props)
			if err != nil {
				ctrl.Close()
				return nil, fmt.Errorf("failed to create tunnel: %w", err)
			}
			listener, ok := tunnel.(interface{ net.Listener })
			if !ok {
				tunnel.Close()
				ctrl.Close()
				return nil, fmt.Errorf("tunnel does not implement net.Listener")
			}
			props, err = tunnel.Properties()
			if err != nil {
				listener.Close()
				ctrl.Close()
				return nil, fmt.Errorf("failed to get tunnel properties: %w", err)
			}
			display, err := netcatTunnelDisplay(props)
			if err != nil {
				listener.Close()
				ctrl.Close()
				return nil, err
			}
			return &netcatListenerResult{
				Listener:  &netcatManagedListener{Listener: listener, ctrl: ctrl},
				Display:   display,
				Generated: target.Name == nil,
			}, nil
		}
	default:
		return func(context.Context) (*netcatListenerResult, error) {
			listener, err := net.Listen("tcp", target.Address)
			if err != nil {
				return nil, fmt.Errorf("failed to listen on %s: %w", target.Address, err)
			}
			return &netcatListenerResult{Listener: listener, Display: listener.Addr().String()}, nil
		}
	}
}

func netcatTunnelDisplay(props rstream.TunnelProperties) (string, error) {
	if props.Name != nil && *props.Name != "" {
		return "rstrm://" + *props.Name, nil
	}
	if props.ID != nil && *props.ID != "" {
		return "rstrm://" + *props.ID, nil
	}
	return "", fmt.Errorf("failed to resolve tunnel identifier")
}

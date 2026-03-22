// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go"
)

func buildTunnelListParams(filter string) (*rstream.ListTunnelsParams, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, nil
	}
	parts := splitCSV(filter)
	if len(parts) == 0 {
		return nil, nil
	}
	fp := &rstream.ListTunnelsFilters{Labels: make(map[string]*string)}
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("expected key=value, got %q", part)
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch {
		case key == "id":
			fp.ID = &val
		case key == "name":
			fp.Name = &val
		case key == "type":
			fp.Type = &val
		case key == "status":
			fp.Status = &val
		case key == "client_id":
			fp.ClientID = &val
		case key == "user_id":
			fp.UserID = &val
		case key == "protocol":
			fp.Protocol = &val
		case key == "publish":
			b, err := parseBool(val)
			if err != nil {
				return nil, fmt.Errorf("publish: %w", err)
			}
			fp.Publish = &b
		case key == "http_version":
			fp.HTTPVersion = &val
		case strings.HasPrefix(key, "labels.") || strings.HasPrefix(key, "label."):
			labelKey := strings.TrimPrefix(strings.TrimPrefix(key, "labels."), "label.")
			if labelKey == "" {
				return nil, fmt.Errorf("invalid label filter key %q", key)
			}
			if val == "" || val == "*" {
				fp.Labels[labelKey] = nil
			} else {
				v := val
				fp.Labels[labelKey] = &v
			}
		default:
			return nil, fmt.Errorf("unknown filter key %q", key)
		}
	}
	return &rstream.ListTunnelsParams{Filters: fp}, nil
}

func buildClientListParams(filter string) (*rstream.ListClientsParams, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, nil
	}
	parts := splitCSV(filter)
	if len(parts) == 0 {
		return nil, nil
	}
	fp := &rstream.ListClientsFilters{Labels: make(map[string]*string)}
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("expected key=value, got %q", part)
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch {
		case key == "id":
			fp.ID = &val
		case key == "status":
			fp.Status = &val
		case key == "user_id":
			fp.UserID = &val
		case key == "agent":
			fp.Agent = &val
		case key == "channel":
			fp.Channel = &val
		case key == "version":
			fp.Version = &val
		case key == "os":
			fp.OS = &val
		case key == "arch":
			fp.Arch = &val
		case key == "protocol_version":
			fp.ProtocolVersion = &val
		case strings.HasPrefix(key, "labels.") || strings.HasPrefix(key, "label."):
			labelKey := strings.TrimPrefix(strings.TrimPrefix(key, "labels."), "label.")
			if labelKey == "" {
				return nil, fmt.Errorf("invalid label filter key %q", key)
			}
			if val == "" || val == "*" {
				fp.Labels[labelKey] = nil
			} else {
				v := val
				fp.Labels[labelKey] = &v
			}
		default:
			return nil, fmt.Errorf("unknown filter key %q", key)
		}
	}
	return &rstream.ListClientsParams{Filters: fp}, nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "y":
		return true, nil
	case "0", "f", "false", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", s)
	}
}

func splitCSV(s string) []string {
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

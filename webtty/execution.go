// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"os/user"
	"strings"
)

type ExecutionMode string

const (
	WebTTYExecutionModeSpawn ExecutionMode = "spawn"
	WebTTYExecutionModeLogin ExecutionMode = "login"
)

func ParseExecutionMode(raw string) (ExecutionMode, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", string(WebTTYExecutionModeSpawn):
		return WebTTYExecutionModeSpawn, nil
	case string(WebTTYExecutionModeLogin):
		return WebTTYExecutionModeLogin, nil
	default:
		return "", fmt.Errorf("invalid WebTTY execution mode %q (valid: spawn, login)", raw)
	}
}

type executionIdentity struct {
	userInfo           *UserInfo
	credentialRequired bool
	mode               ExecutionMode
}

func resolveExecutionIdentity(cfg *ServerConfig, requested *UsernameVariant) (*executionIdentity, error) {
	mode, err := serverExecutionMode(cfg)
	if err != nil {
		return nil, err
	}
	effective := cloneUsernameVariant(requested)
	switch mode {
	case WebTTYExecutionModeSpawn:
	case WebTTYExecutionModeLogin:
		if cfg != nil && cfg.DefaultUsername != nil && strings.TrimSpace(*cfg.DefaultUsername) != "" {
			defaultName := strings.TrimSpace(*cfg.DefaultUsername)
			if effective == nil {
				effective = &UsernameVariant{Name: &defaultName}
			}
		}
		if requested != nil && (cfg == nil || cfg.AllowClientUser == nil || !*cfg.AllowClientUser) {
			return nil, fmt.Errorf("client-selected OS users are disabled in login execution mode")
		}
		if effective == nil {
			return nil, fmt.Errorf("login execution mode requires a configured default user or explicitly allowed client user")
		}
	default:
		return nil, fmt.Errorf("invalid WebTTY execution mode %q", mode)
	}
	ui, err := GetUserInfo(effective)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	required, err := executionCredentialRequired(mode, ui, effective)
	if err != nil {
		return nil, err
	}
	return &executionIdentity{userInfo: ui, credentialRequired: required, mode: mode}, nil
}

func serverExecutionMode(cfg *ServerConfig) (ExecutionMode, error) {
	if cfg == nil || cfg.ExecutionMode == nil {
		return WebTTYExecutionModeSpawn, nil
	}
	return ParseExecutionMode(string(*cfg.ExecutionMode))
}

func cloneUsernameVariant(src *UsernameVariant) *UsernameVariant {
	if src == nil {
		return nil
	}
	out := &UsernameVariant{}
	if src.Name != nil {
		name := *src.Name
		out.Name = &name
	}
	if src.UID != nil {
		uid := *src.UID
		out.UID = &uid
	}
	return out
}

func currentUserMatches(ui *UserInfo) bool {
	if ui == nil {
		return false
	}
	current, err := user.Current()
	if err != nil {
		return false
	}
	return userInfoMatchesCurrent(ui, current)
}

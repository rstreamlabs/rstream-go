// See LICENSE file in the project root for license information.

package webtty

import (
	"os/user"
	"strings"
	"testing"
)

func TestParseExecutionMode(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    ExecutionMode
		wantErr bool
	}{
		{name: "default", raw: "", want: WebTTYExecutionModeSpawn},
		{name: "spawn", raw: "spawn", want: WebTTYExecutionModeSpawn},
		{name: "login", raw: "login", want: WebTTYExecutionModeLogin},
		{name: "trim case", raw: " LOGIN ", want: WebTTYExecutionModeLogin},
		{name: "invalid", raw: "sudo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseExecutionMode(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseExecutionMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseExecutionMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveExecutionIdentityLoginRequiresUser(t *testing.T) {
	mode := WebTTYExecutionModeLogin
	_, err := resolveExecutionIdentity(&ServerConfig{ExecutionMode: &mode}, nil)
	if err == nil {
		t.Fatalf("expected login mode without user to fail")
	}
	if !strings.Contains(err.Error(), "requires a configured default user") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExecutionPolicyRejectsMissingLoginUser(t *testing.T) {
	mode := WebTTYExecutionModeLogin
	err := ValidateExecutionPolicy(&ServerConfig{ExecutionMode: &mode})
	if err == nil || !strings.Contains(err.Error(), "existing local OS username") {
		t.Fatalf("ValidateExecutionPolicy() error = %v", err)
	}
}

func TestValidateExecutionPolicyAcceptsClientSelectedUserPolicy(t *testing.T) {
	mode := WebTTYExecutionModeLogin
	allowClientUser := true
	if err := ValidateExecutionPolicy(&ServerConfig{ExecutionMode: &mode, AllowClientUser: &allowClientUser}); err != nil {
		t.Fatalf("ValidateExecutionPolicy() error = %v", err)
	}
}

func TestValidateExecutionPolicyResolvesConfiguredLoginUser(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() error = %v", err)
	}
	mode := WebTTYExecutionModeLogin
	username := current.Username
	if err := ValidateExecutionPolicy(&ServerConfig{ExecutionMode: &mode, DefaultUsername: &username}); err != nil {
		t.Fatalf("ValidateExecutionPolicy() error = %v", err)
	}
}

func TestValidateExecutionPolicyRejectsUnknownLoginUser(t *testing.T) {
	mode := WebTTYExecutionModeLogin
	username := "rstream-webtty-user-that-must-not-exist"
	err := ValidateExecutionPolicy(&ServerConfig{ExecutionMode: &mode, DefaultUsername: &username})
	if err == nil || !strings.Contains(err.Error(), "not a usable local OS account") {
		t.Fatalf("ValidateExecutionPolicy() error = %v", err)
	}
}

func TestResolveExecutionIdentityLoginRejectsClientUserByDefault(t *testing.T) {
	mode := WebTTYExecutionModeLogin
	name := "alice"
	_, err := resolveExecutionIdentity(&ServerConfig{ExecutionMode: &mode}, &UsernameVariant{Name: &name})
	if err == nil {
		t.Fatalf("expected client-selected user to fail")
	}
	if !strings.Contains(err.Error(), "client-selected OS users are disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveExecutionIdentitySpawnDefaultsToCurrentUser(t *testing.T) {
	mode := WebTTYExecutionModeSpawn
	identity, err := resolveExecutionIdentity(&ServerConfig{ExecutionMode: &mode}, nil)
	if err != nil {
		t.Fatalf("resolveExecutionIdentity() error = %v", err)
	}
	if identity.userInfo == nil {
		t.Fatalf("expected user info")
	}
	if identity.credentialRequired {
		t.Fatalf("current-user spawn should not require credential setup")
	}
}

func TestResolveExecutionIdentityLoginAcceptsConfiguredCurrentUser(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() error = %v", err)
	}
	mode := WebTTYExecutionModeLogin
	username := current.Username
	identity, err := resolveExecutionIdentity(&ServerConfig{ExecutionMode: &mode, DefaultUsername: &username}, nil)
	if err != nil {
		t.Fatalf("resolveExecutionIdentity() error = %v", err)
	}
	if identity.userInfo == nil {
		t.Fatalf("expected user info")
	}
}

func TestResolveExecutionIdentityLoginAcceptsAllowedClientCurrentUser(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() error = %v", err)
	}
	mode := WebTTYExecutionModeLogin
	allowClientUser := true
	username := current.Username
	identity, err := resolveExecutionIdentity(&ServerConfig{ExecutionMode: &mode, AllowClientUser: &allowClientUser}, &UsernameVariant{Name: &username})
	if err != nil {
		t.Fatalf("resolveExecutionIdentity() error = %v", err)
	}
	if identity.userInfo == nil {
		t.Fatalf("expected user info")
	}
	if identity.credentialRequired {
		t.Fatalf("current-user login should not require credential setup")
	}
}

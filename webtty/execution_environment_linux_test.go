// See LICENSE file in the project root for license information.

//go:build linux

package webtty

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxLoginEnvironmentAddsValidatedUserRuntime(t *testing.T) {
	userInfo, err := GetUserInfo(nil)
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}
	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	busPath := filepath.Join(runtimeDir, "bus")
	listener, err := net.Listen("unix", busPath)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	defer listener.Close()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/untrusted/bus")

	env := []string{
		"XDG_RUNTIME_DIR=/client/runtime",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/client/bus",
	}
	addUnixLoginSessionEnvironment(&env, userInfo)
	assertEnvironmentValue(t, env, "XDG_RUNTIME_DIR", runtimeDir)
	assertEnvironmentValue(t, env, "DBUS_SESSION_BUS_ADDRESS", "unix:path="+busPath)
}

func TestLinuxLoginEnvironmentRejectsUnownedOrPublicRuntime(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o755); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	userInfo := &UserInfo{UID: ^uint32(0) - 1}
	env := []string{}
	addUnixLoginSessionEnvironment(&env, userInfo)
	if _, ok := environmentValue(env, "XDG_RUNTIME_DIR"); ok {
		t.Fatalf("unsafe runtime directory was inherited: %q", env)
	}
	if _, ok := environmentValue(env, "DBUS_SESSION_BUS_ADDRESS"); ok {
		t.Fatalf("unsafe D-Bus address was inherited: %q", env)
	}
}

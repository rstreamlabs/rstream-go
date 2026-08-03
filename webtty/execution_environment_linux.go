// See LICENSE file in the project root for license information.

//go:build linux

package webtty

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func addUnixLoginSessionEnvironment(env *[]string, userInfo *UserInfo) {
	runtimeDir := linuxUserRuntimeDir(userInfo)
	if runtimeDir == "" {
		return
	}
	AddEnvironmentVariable(env, "XDG_RUNTIME_DIR", runtimeDir, true)
	busPath := filepath.Join(runtimeDir, "bus")
	if linuxOwnedSocket(busPath, userInfo.UID) {
		AddEnvironmentVariable(env, "DBUS_SESSION_BUS_ADDRESS", "unix:path="+busPath, true)
	}
}

func linuxUserRuntimeDir(userInfo *UserInfo) string {
	if userInfo == nil {
		return ""
	}
	candidates := []string{}
	if currentUserMatches(userInfo) {
		if inherited := os.Getenv("XDG_RUNTIME_DIR"); inherited != "" {
			candidates = append(candidates, inherited)
		}
	}
	candidates = append(candidates, fmt.Sprintf("/run/user/%d", userInfo.UID))
	for _, candidate := range candidates {
		if linuxOwnedPrivateDirectory(candidate, userInfo.UID) {
			return candidate
		}
	}
	return ""
}

func linuxOwnedPrivateDirectory(path string, uid uint32) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uid
}

func linuxOwnedSocket(path string, uid uint32) bool {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uid
}

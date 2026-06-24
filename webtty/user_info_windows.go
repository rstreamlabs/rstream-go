// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type UserInfo struct {
	Name  string
	Shell string
	Home  string
}

func GetUserInfo(u *UsernameVariant) (*UserInfo, error) {
	name, err := currentWindowsUsername()
	if err != nil {
		return nil, fmt.Errorf("user lookup: %w", err)
	}
	if u != nil && u.UID != nil {
		return nil, fmt.Errorf("Windows user lookup by numeric UID is not supported")
	}
	if u != nil && u.Name != nil && !windowsUsernameMatches(name, *u.Name) {
		return nil, fmt.Errorf("changing user is not supported on Windows")
	}
	shell, err := DefaultShellWindows()
	if err != nil {
		return nil, fmt.Errorf("determine shell: %w", err)
	}
	if name == "" {
		return nil, fmt.Errorf("user lookup returned empty username")
	}
	home := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if home == "" {
		return nil, fmt.Errorf("user lookup returned empty home directory")
	}
	return &UserInfo{
		Name:  name,
		Home:  home,
		Shell: shell,
	}, nil
}

func windowsUsernameMatches(current string, requested string) bool {
	current = strings.TrimSpace(current)
	requested = strings.TrimSpace(requested)
	if strings.EqualFold(current, requested) {
		return true
	}
	parts := strings.Split(requested, `\`)
	return len(parts) > 0 && strings.EqualFold(current, strings.TrimSpace(parts[len(parts)-1]))
}

var (
	modadvapi32     = windows.NewLazySystemDLL("advapi32.dll")
	procGetUserName = modadvapi32.NewProc("GetUserNameW")
)

func currentWindowsUsername() (string, error) {
	size := uint32(64)
	for {
		buf := make([]uint16, size)
		r1, _, e1 := procGetUserName.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
		)
		if r1 != 0 {
			return windows.UTF16ToString(buf[:size-1]), nil
		}
		if e1 != nil && !errors.Is(e1, windows.ERROR_INSUFFICIENT_BUFFER) {
			return "", e1
		}
		if size == 0 {
			return "", fmt.Errorf("GetUserNameW returned an empty size")
		}
	}
}

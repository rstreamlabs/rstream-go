// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"fmt"
	"os/user"
	"strings"
)

func executionCredentialRequired(mode ExecutionMode, ui *UserInfo, requested *UsernameVariant) (bool, error) {
	if ui == nil {
		return false, fmt.Errorf("missing OS user info")
	}
	if currentUserMatches(ui) {
		return false, nil
	}
	if mode == WebTTYExecutionModeLogin || requested != nil {
		return false, fmt.Errorf("Windows login execution mode requires an OS user token provider; password-based WebTTY login is not supported")
	}
	return false, nil
}

func userInfoMatchesCurrent(ui *UserInfo, current *user.User) bool {
	if strings.EqualFold(strings.TrimSpace(ui.Name), strings.TrimSpace(current.Username)) {
		return true
	}
	parts := strings.Split(current.Username, `\`)
	return len(parts) > 0 && strings.EqualFold(strings.TrimSpace(ui.Name), strings.TrimSpace(parts[len(parts)-1]))
}

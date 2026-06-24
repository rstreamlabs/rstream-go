// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"fmt"
	"os/user"
	"strconv"
)

func executionCredentialRequired(mode ExecutionMode, ui *UserInfo, requested *UsernameVariant) (bool, error) {
	if ui == nil {
		return false, fmt.Errorf("missing OS user info")
	}
	if currentUserMatches(ui) {
		return false, nil
	}
	if mode == WebTTYExecutionModeLogin || requested != nil {
		return true, nil
	}
	return false, nil
}

func userInfoMatchesCurrent(ui *UserInfo, current *user.User) bool {
	uid, gid, err := lookupNumericUserIDs(current)
	if err != nil {
		return false
	}
	return uid == ui.UID && gid == ui.GID
}

func usernameVariantDisplay(u *UsernameVariant) string {
	if u == nil {
		return ""
	}
	if u.Name != nil {
		return *u.Name
	}
	if u.UID != nil {
		return strconv.FormatUint(uint64(*u.UID), 10)
	}
	return ""
}

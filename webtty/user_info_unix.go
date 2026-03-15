// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"fmt"
	"os/user"
	"strconv"
)

type UserInfo struct {
	Name  string
	Shell string
	Home  string
	UID   uint32
	GID   uint32
}

func GetUserInfo(u *UsernameVariant) (*UserInfo, error) {
	usr, err := lookupUser(u)
	if err != nil {
		return nil, err
	}
	uid, gid, err := lookupNumericUserIDs(usr)
	if err != nil {
		return nil, err
	}
	shell, err := DefaultShell(usr)
	if err != nil {
		return nil, fmt.Errorf("determine shell: %w", err)
	}
	return &UserInfo{
		Name:  usr.Username,
		Home:  usr.HomeDir,
		UID:   uid,
		GID:   gid,
		Shell: shell,
	}, nil
}

func lookupUser(u *UsernameVariant) (*user.User, error) {
	var (
		usr *user.User
		err error
	)
	if u == nil || (u.Name == nil && u.UID == nil) {
		usr, err = user.Current()
	} else if u.Name != nil {
		usr, err = user.Lookup(*u.Name)
	} else {
		usr, err = user.LookupId(strconv.FormatUint(uint64(*u.UID), 10))
	}
	if err != nil {
		return nil, fmt.Errorf("user lookup: %w", err)
	}
	return usr, nil
}

func lookupNumericUserIDs(usr *user.User) (uint32, uint32, error) {
	uid64, err := strconv.ParseUint(usr.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid uid %q: %w", usr.Uid, err)
	}
	gid64, err := strconv.ParseUint(usr.Gid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid gid %q: %w", usr.Gid, err)
	}
	return uint32(uid64), uint32(gid64), nil
}

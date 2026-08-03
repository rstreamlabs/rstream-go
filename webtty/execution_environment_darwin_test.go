// See LICENSE file in the project root for license information.

//go:build darwin

package webtty

import (
	"os"
	"testing"
)

func TestDarwinLoginEnvironmentPreservesCurrentUserSessionValues(t *testing.T) {
	userInfo, err := GetUserInfo(nil)
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}
	t.Setenv("TMPDIR", "/rstream/test/tmp")
	t.Setenv("__CF_USER_TEXT_ENCODING", "0x1F5:0:0")
	env := []string{
		"TMPDIR=/client/tmp",
		"__CF_USER_TEXT_ENCODING=client-value",
	}
	addUnixLoginSessionEnvironment(&env, userInfo)
	assertEnvironmentValue(t, env, "TMPDIR", "/rstream/test/tmp")
	assertEnvironmentValue(t, env, "__CF_USER_TEXT_ENCODING", "0x1F5:0:0")
}

func TestDarwinLoginEnvironmentDoesNotCopySessionValuesAcrossUsers(t *testing.T) {
	userInfo, err := GetUserInfo(nil)
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}
	t.Setenv("TMPDIR", "/rstream/test/tmp")
	t.Setenv("__CF_USER_TEXT_ENCODING", "0x1F5:0:0")
	userInfo.UID = uint32(os.Getuid()) + 1
	env := []string{}
	addUnixLoginSessionEnvironment(&env, userInfo)
	if _, ok := environmentValue(env, "TMPDIR"); ok {
		t.Fatalf("TMPDIR was copied to another user: %q", env)
	}
	if _, ok := environmentValue(env, "__CF_USER_TEXT_ENCODING"); ok {
		t.Fatalf("Core Foundation encoding was copied to another user: %q", env)
	}
}

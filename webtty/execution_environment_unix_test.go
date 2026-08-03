// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import "testing"

func TestUnixLoginEnvironmentForcesResolvedIdentity(t *testing.T) {
	identity := &executionIdentity{
		mode: WebTTYExecutionModeLogin,
		userInfo: &UserInfo{
			Name:  "resolved-user",
			Home:  "/resolved/home",
			Shell: "/resolved/shell",
		},
	}
	env := []string{
		"USER=client-user",
		"LOGNAME=client-user",
		"HOME=/client/home",
		"SHELL=/client/shell",
	}
	addPlatformExecutionEnvironment(&env, identity)
	assertEnvironmentValue(t, env, "USER", "resolved-user")
	assertEnvironmentValue(t, env, "LOGNAME", "resolved-user")
	assertEnvironmentValue(t, env, "HOME", "/resolved/home")
	assertEnvironmentValue(t, env, "SHELL", "/resolved/shell")
}

func TestUnixSpawnEnvironmentPreservesClientOverrides(t *testing.T) {
	identity := &executionIdentity{
		mode: WebTTYExecutionModeSpawn,
		userInfo: &UserInfo{
			Name:  "resolved-user",
			Home:  "/resolved/home",
			Shell: "/resolved/shell",
		},
	}
	env := []string{"HOME=/client/home"}
	addPlatformExecutionEnvironment(&env, identity)
	assertEnvironmentValue(t, env, "HOME", "/client/home")
	assertEnvironmentValue(t, env, "LOGNAME", "resolved-user")
}

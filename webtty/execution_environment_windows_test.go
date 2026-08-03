// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import "testing"

func TestWindowsLoginEnvironmentIsCompleteCaseInsensitiveAndSecretFree(t *testing.T) {
	t.Setenv("APPDATA", `C:\Users\operator\AppData\Roaming`)
	t.Setenv("LOCALAPPDATA", `C:\Users\operator\AppData\Local`)
	t.Setenv("PSMODULEPATH", `C:\Modules`)
	t.Setenv("SSH_AUTH_SOCK", `\\.\pipe\secret-agent`)
	identity := &executionIdentity{
		mode: WebTTYExecutionModeLogin,
		userInfo: &UserInfo{
			Name:  "operator",
			Home:  `C:\Users\operator`,
			Shell: `C:\Windows\System32\cmd.exe`,
		},
	}
	env := []string{"userprofile=C:\\client", "USERNAME=client"}
	addPlatformExecutionEnvironment(&env, identity)
	assertEnvironmentValue(t, env, "USERNAME", "operator")
	assertEnvironmentValue(t, env, "USERPROFILE", `C:\Users\operator`)
	assertEnvironmentValue(t, env, "HOME", `C:\Users\operator`)
	assertEnvironmentValue(t, env, "COMSPEC", `C:\Windows\System32\cmd.exe`)
	assertEnvironmentValue(t, env, "APPDATA", `C:\Users\operator\AppData\Roaming`)
	assertEnvironmentValue(t, env, "LOCALAPPDATA", `C:\Users\operator\AppData\Local`)
	assertEnvironmentValue(t, env, "PSMODULEPATH", `C:\Modules`)
	if _, ok := environmentValue(env, "SSH_AUTH_SOCK"); ok {
		t.Fatalf("SSH agent socket was inherited: %q", env)
	}
}

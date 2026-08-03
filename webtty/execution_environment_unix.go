// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

func addPlatformExecutionEnvironment(env *[]string, identity *executionIdentity) {
	if identity == nil || identity.userInfo == nil {
		return
	}
	forceIdentity := identity.mode == WebTTYExecutionModeLogin
	AddEnvironmentVariable(env, "USER", identity.userInfo.Name, forceIdentity)
	AddEnvironmentVariable(env, "LOGNAME", identity.userInfo.Name, forceIdentity)
	AddEnvironmentVariable(env, "SHELL", identity.userInfo.Shell, forceIdentity)
	AddEnvironmentVariable(env, "HOME", identity.userInfo.Home, forceIdentity)
	if identity.mode == WebTTYExecutionModeLogin {
		addUnixLoginSessionEnvironment(env, identity.userInfo)
	}
}

// See LICENSE file in the project root for license information.

//go:build windows

package webtty

var windowsSessionEnvironmentKeys = []string{
	"ALLUSERSPROFILE",
	"APPDATA",
	"COMMONPROGRAMFILES",
	"COMMONPROGRAMFILES(X86)",
	"COMMONPROGRAMW6432",
	"COMPUTERNAME",
	"COMSPEC",
	"CYGWIN",
	"HOMEDRIVE",
	"HOMEPATH",
	"LOCALAPPDATA",
	"LOGONSERVER",
	"NUMBER_OF_PROCESSORS",
	"OS",
	"PATHEXT",
	"PROCESSOR_ARCHITECTURE",
	"PROCESSOR_IDENTIFIER",
	"PROCESSOR_LEVEL",
	"PROCESSOR_REVISION",
	"PROGRAMDATA",
	"PROGRAMFILES",
	"PROGRAMFILES(X86)",
	"PROGRAMW6432",
	"PSMODULEPATH",
	"PUBLIC",
	"SYSTEMDRIVE",
	"SYSTEMROOT",
	"TEMP",
	"TMP",
	"USERDOMAIN",
	"USERDOMAIN_ROAMINGPROFILE",
	"USERNAME",
	"USERPROFILE",
	"WINDIR",
}

func addPlatformExecutionEnvironment(env *[]string, identity *executionIdentity) {
	force := identity != nil && identity.mode == WebTTYExecutionModeLogin
	addInheritedEnvironmentVariables(env, windowsSessionEnvironmentKeys, force)
	if identity == nil || identity.userInfo == nil {
		return
	}
	AddEnvironmentVariable(env, "USERNAME", identity.userInfo.Name, force)
	AddEnvironmentVariable(env, "USERPROFILE", identity.userInfo.Home, force)
	AddEnvironmentVariable(env, "COMSPEC", identity.userInfo.Shell, force)
	AddEnvironmentVariable(env, "HOME", identity.userInfo.Home, force)
}

// See LICENSE file in the project root for license information.

//go:build darwin

package webtty

func addUnixLoginSessionEnvironment(env *[]string, userInfo *UserInfo) {
	if !currentUserMatches(userInfo) {
		return
	}
	addInheritedEnvironmentVariables(env, []string{"TMPDIR", "__CF_USER_TEXT_ENCODING"}, true)
}

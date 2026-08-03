// See LICENSE file in the project root for license information.

package webtty

import (
	"strings"
	"testing"
)

func TestExecutionEnvironmentCopiesAdministrativeBasicsButNotSecrets(t *testing.T) {
	t.Setenv("PATH", "/rstream/test/bin")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("TZ", "UTC")
	t.Setenv("SSH_AUTH_SOCK", "/secret/agent.sock")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "secret")

	identity := &executionIdentity{
		mode: WebTTYExecutionModeSpawn,
		userInfo: &UserInfo{
			Name:  "operator",
			Home:  "/home/operator",
			Shell: "/bin/sh",
		},
	}
	env := []string{}
	addExecutionEnvironment(&env, identity)

	assertEnvironmentValue(t, env, "PATH", "/rstream/test/bin")
	assertEnvironmentValue(t, env, "LANG", "en_US.UTF-8")
	assertEnvironmentValue(t, env, "TZ", "UTC")
	for _, key := range []string{"SSH_AUTH_SOCK", "AWS_SECRET_ACCESS_KEY", "RSTREAM_AUTHENTICATION_TOKEN"} {
		if _, ok := environmentValue(env, key); ok {
			t.Fatalf("sensitive environment variable %s was inherited", key)
		}
	}
}

func environmentValue(env []string, key string) (string, bool) {
	for _, item := range env {
		existingKey, value, ok := strings.Cut(item, "=")
		if ok && environmentKeyEqual(existingKey, key) {
			return value, true
		}
	}
	return "", false
}

func assertEnvironmentValue(t *testing.T, env []string, key, want string) {
	t.Helper()
	got, ok := environmentValue(env, key)
	if !ok {
		t.Fatalf("environment variable %s is missing from %q", key, env)
	}
	if got != want {
		t.Fatalf("environment variable %s = %q, want %q", key, got, want)
	}
}

// See LICENSE file in the project root for license information.

package webtty

import "os"

var commonSessionEnvironmentKeys = []string{
	"LANG",
	"LANGUAGE",
	"LC_ADDRESS",
	"LC_ALL",
	"LC_COLLATE",
	"LC_CTYPE",
	"LC_IDENTIFICATION",
	"LC_MEASUREMENT",
	"LC_MESSAGES",
	"LC_MONETARY",
	"LC_NAME",
	"LC_NUMERIC",
	"LC_PAPER",
	"LC_TELEPHONE",
	"LC_TIME",
	"TZ",
}

func addExecutionEnvironment(env *[]string, identity *executionIdentity) {
	if path := os.Getenv("PATH"); path != "" {
		AddEnvironmentVariable(env, "PATH", path, false)
	}
	addInheritedEnvironmentVariables(env, commonSessionEnvironmentKeys, false)
	addPlatformExecutionEnvironment(env, identity)
}

func addInheritedEnvironmentVariables(env *[]string, keys []string, force bool) {
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			AddEnvironmentVariable(env, key, value, force)
		}
	}
}

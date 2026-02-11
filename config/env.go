// See LICENSE file in the project root for license information.

package config

import (
	"os"
	"strings"
)

type EnvSettings struct {
	ConfigPath string
	APIURL     string
	Context    string
	Engine     string
	Token      string
}

func ReadEnv() EnvSettings {
	return EnvSettings{
		ConfigPath: strings.TrimSpace(os.Getenv("RSTREAM_CONFIG")),
		APIURL:     strings.TrimSpace(os.Getenv("RSTREAM_API_URL")),
		Context:    strings.TrimSpace(os.Getenv("RSTREAM_CONTEXT")),
		Engine:     strings.TrimSpace(os.Getenv("RSTREAM_ENGINE")),
		Token:      strings.TrimSpace(os.Getenv("RSTREAM_AUTHENTICATION_TOKEN")),
	}
}

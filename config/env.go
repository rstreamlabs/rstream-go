// See LICENSE file in the project root for license information.

package config

import (
	"os"
	"strings"
)

type EnvSettings struct {
	ConfigPath          string
	APIURL              string
	Context             string
	Engine              string
	Token               string
	MTLSCert            string
	MTLSKey             string
	Region              string
	TunnelTransport     string
	UseQUIC             *bool
	ControlPlaneHeaders string
}

func ReadEnv() EnvSettings {
	return EnvSettings{
		ConfigPath:          strings.TrimSpace(os.Getenv("RSTREAM_CONFIG")),
		APIURL:              NormalizeAPIURL(os.Getenv("RSTREAM_API_URL")),
		Context:             strings.TrimSpace(os.Getenv("RSTREAM_CONTEXT")),
		Engine:              strings.TrimSpace(os.Getenv("RSTREAM_ENGINE")),
		Token:               strings.TrimSpace(os.Getenv("RSTREAM_AUTHENTICATION_TOKEN")),
		MTLSCert:            strings.TrimSpace(os.Getenv("RSTREAM_MTLS_CERT_FILE")),
		MTLSKey:             strings.TrimSpace(os.Getenv("RSTREAM_MTLS_KEY_FILE")),
		Region:              strings.TrimSpace(os.Getenv("RSTREAM_REGION")),
		TunnelTransport:     strings.TrimSpace(os.Getenv("RSTREAM_TUNNEL_TRANSPORT")),
		UseQUIC:             legacyQUICTransportSetting(),
		ControlPlaneHeaders: strings.TrimSpace(os.Getenv("RSTREAM_CONTROL_PLANE_HEADERS")),
	}
}

func legacyQUICTransportSetting() *bool {
	value, ok := os.LookupEnv("RSTREAM_QUIC_TRANSPORT")
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return nil
	}
	enabled := value == "1"
	return &enabled
}

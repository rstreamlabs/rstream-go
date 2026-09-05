//go:build !rstream_fips

// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/tls"

	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
)

func defaultE2EKeyEnvelopeSuite() KeyEnvelopeSuite {
	return KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM
}

func validateProfileKeyEnvelopeSuite(suite KeyEnvelopeSuite) error {
	return validateSupportedKeyEnvelopeSuite(suite)
}

func validateFIPSWebTTYTransport(WebTTYTransport) error {
	return nil
}

func validateFIPSWebTTYServerConfig(*ServerConfig) error {
	return nil
}

func validateFIPSWebTTYClientConfig(*SessionConfig, *tls.Config) error {
	return nil
}

func rejectFIPSWebSocketServer() error {
	return nil
}

func requireFIPSWebTTYRuntime() error {
	return fipsprofile.Unavailable("FIPS-only WebTTY runtime validation")
}

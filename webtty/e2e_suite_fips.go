//go:build rstream_fips

// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
)

func defaultE2EKeyEnvelopeSuite() KeyEnvelopeSuite {
	return KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce
}

func validateProfileKeyEnvelopeSuite(suite KeyEnvelopeSuite) error {
	if suite != KeyEnvelopeSuiteP256HKDFSHA256AES256GCMRandomNonce {
		return fmt.Errorf("WebTTY key envelope suite %d is not available in the rstream FIPS profile", suite)
	}
	return nil
}

func validateFIPSWebTTYTransport(transport WebTTYTransport) error {
	if err := fipsprofile.Require(); err != nil {
		return err
	}
	if transport != WebTTYTransportWebTransport {
		return fmt.Errorf("WebTTY transport %q is not available in the rstream FIPS profile; use webtransport", transport)
	}
	return nil
}

func validateFIPSWebTTYServerConfig(cfg *ServerConfig) error {
	if err := fipsprofile.Require(); err != nil {
		return err
	}
	if cfg == nil || cfg.EndpointIdentity == nil || cfg.PayloadCryptoResolver == nil {
		return errors.New("WebTTY requires endpoint identity and E2E payload encryption in the rstream FIPS profile")
	}
	if cfg.RequireSessionKeyGrant == nil || !*cfg.RequireSessionKeyGrant {
		return errors.New("WebTTY requires session key grants in the rstream FIPS profile")
	}
	if cfg.RequireClientProof == nil || !*cfg.RequireClientProof {
		return errors.New("WebTTY requires client proof in the rstream FIPS profile")
	}
	return validateWebTTYEndpointIdentity(*cfg.EndpointIdentity)
}

func validateFIPSWebTTYClientConfig(cfg *SessionConfig, tlsConfig *tls.Config) error {
	if err := fipsprofile.Require(); err != nil {
		return err
	}
	if cfg == nil || cfg.PayloadCrypto == nil || cfg.EndpointIdentity == nil {
		return errors.New("WebTTY requires endpoint identity and E2E payload encryption in the rstream FIPS profile")
	}
	if cfg.Attach == nil && cfg.ExpectedServerIdentity == nil {
		return errors.New("WebTTY requires an expected server identity in the rstream FIPS profile")
	}
	if cfg.PayloadCrypto.SessionKeyGrant != nil && cfg.PayloadCrypto.SessionKeyGrant.KeyEnvelopeSuite != defaultE2EKeyEnvelopeSuite() {
		return errors.New("WebTTY session key grant does not use the FIPS key envelope suite")
	}
	if err := validateWebTTYEndpointIdentity(*cfg.EndpointIdentity); err != nil {
		return err
	}
	if tlsConfig != nil {
		if tlsConfig.InsecureSkipVerify {
			return errors.New("WebTTY cannot disable TLS certificate verification in the rstream FIPS profile")
		}
		if len(tlsConfig.EncryptedClientHelloConfigList) > 0 {
			return errors.New("WebTTY cannot enable ECH in the rstream FIPS profile")
		}
		if tlsConfig.MinVersion != 0 && tlsConfig.MinVersion != tls.VersionTLS13 {
			return errors.New("WebTransport requires TLS 1.3 in the rstream FIPS profile")
		}
		if tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion != tls.VersionTLS13 {
			return errors.New("WebTransport requires TLS 1.3 in the rstream FIPS profile")
		}
		for _, curve := range tlsConfig.CurvePreferences {
			switch curve {
			case tls.CurveP256, tls.CurveP384, tls.SecP256r1MLKEM768, tls.SecP384r1MLKEM1024:
			default:
				return fmt.Errorf("WebTTY TLS curve %s is not approved for the rstream FIPS profile", curve)
			}
		}
	}
	return nil
}

func rejectFIPSWebSocketServer() error {
	return errors.New("WebSocket WebTTY is not available in the rstream FIPS profile; use WebTransport")
}

func requireFIPSWebTTYRuntime() error {
	return fipsprofile.Require()
}

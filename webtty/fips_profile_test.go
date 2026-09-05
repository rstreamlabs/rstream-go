//go:build rstream_fips

// See LICENSE file in the project root for license information.

package webtty

import (
	"strings"
	"testing"
)

func TestFIPSProfileRejectsCurrentWebTTYCrypto(t *testing.T) {
	if _, err := GenerateE2EIdentity(); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("GenerateE2EIdentity() error = %v, want FIPS profile rejection", err)
	}
	if _, err := E2EIdentityFromPrivateKey(make([]byte, E2EX25519PrivateKeySize)); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("E2EIdentityFromPrivateKey() error = %v, want FIPS profile rejection", err)
	}
	if _, err := NewE2EClientPayloadCrypto(E2EPayloadCryptoConfig{}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v, want FIPS profile rejection", err)
	}
}

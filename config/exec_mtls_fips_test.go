// See LICENSE file in the project root for license information.

//go:build rstream_fips

package config

import (
	"strings"
	"testing"
)

func TestFIPSProfileRejectsExternalMTLSSigner(t *testing.T) {
	_, _, err := MTLSConfigFromAuth(&Auth{MTLS: &MTLS{Storage: &MTLSStorage{Kind: MTLSStorageExec}}})
	if err == nil || !strings.Contains(err.Error(), "external mTLS signer is not available in the rstream FIPS profile") {
		t.Fatalf("FIPS must reject exec before invoking or inspecting the helper: %v", err)
	}
}

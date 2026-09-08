//go:build rstream_fips

// See LICENSE file in the project root for license information.

package fileserver

import (
	"strings"
	"testing"
)

func TestFIPSProfileRejectsFileSharingBackends(t *testing.T) {
	for _, backend := range []string{"webdav", "webrtc"} {
		t.Run(backend, func(t *testing.T) {
			server, err := New(Config{Root: t.TempDir(), Backend: backend})
			if server != nil {
				defer server.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "FIPS profile") {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

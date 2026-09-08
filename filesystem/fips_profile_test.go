//go:build rstream_fips

// See LICENSE file in the project root for license information.

package filesystem

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFIPSProfileRejectsFilesystemDiscoveryBeforeIO(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.NotFound(w, nil)
	}))
	defer server.Close()
	client := NewHTTPClient(server.URL, server.Client())
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "FIPS profile") {
		t.Errorf("Do() error = %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("network calls = %d", calls.Load())
	}
}

func TestFIPSProfileRejectsFilesystemBackend(t *testing.T) {
	local, err := Open(t.TempDir(), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for _, backend := range []string{"webdav", "webrtc"} {
		t.Run(backend, func(t *testing.T) {
			handler, err := NewBackend(local, BackendConfig{Backend: backend})
			if handler != nil {
				defer handler.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "FIPS profile") {
				t.Fatalf("NewBackend() error = %v", err)
			}
		})
	}
}

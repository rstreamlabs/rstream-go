//go:build rstream_fips

// See LICENSE file in the project root for license information.

package filesystem

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFIPSCompatibleWebDAVOverTLS(t *testing.T) {
	local, err := Open(t.TempDir(), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	handler, err := NewWebDAV(local, WebDAVConfig{Prefix: "/fs"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	defer client.CloseIdleConnections()
	payload := bytes.Repeat([]byte("FIPS WebDAV\x00\xff"), 1000)
	for _, test := range []struct {
		method string
		path   string
		body   []byte
		status int
	}{
		{method: "PUT", path: "/fs/payload.bin", body: payload, status: http.StatusCreated},
		{method: "GET", path: "/fs/payload.bin", status: http.StatusOK},
		{method: "PROPFIND", path: "/fs", status: http.StatusMultiStatus},
		{method: "DELETE", path: "/fs/payload.bin", status: http.StatusNoContent},
	} {
		t.Run(test.method, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), test.method, server.URL+test.path, bytes.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Depth", "1")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, body = %q", response.StatusCode, body)
			}
			if response.TLS == nil || response.TLS.Version != tls.VersionTLS13 || (response.TLS.CipherSuite != tls.TLS_AES_128_GCM_SHA256 && response.TLS.CipherSuite != tls.TLS_AES_256_GCM_SHA384) {
				t.Fatal("WebDAV did not negotiate the required TLS 1.3 AES-GCM connection")
			}
			if test.method == "GET" && (len(body) != len(payload) || sha256.Sum256(body) != sha256.Sum256(payload)) {
				t.Fatal("WebDAV download differs from the uploaded file")
			}
			if test.method == "PROPFIND" && !bytes.Contains(body, []byte("payload.bin")) {
				t.Fatal("WebDAV listing omitted the uploaded file")
			}
		})
	}
}

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

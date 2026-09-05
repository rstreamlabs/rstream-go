// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
)

func TestLegacyDiscoveryReleasesConnectionBeforeDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, rtc.Endpoint) {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "shared file")
	}))
	defer server.Close()
	transport := &http.Transport{MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := NewHTTPClient(server.URL+"/fs", &http.Client{Transport: transport})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "shared file" {
		t.Fatalf("download = %q, error = %v", body, err)
	}
}

func TestDiscoveryRejectsOversizedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, rtc.Endpoint) {
			_, _ = io.WriteString(w, `{"version":1,"backend":"webdav"}`+strings.Repeat(" ", rtc.MaxSignal))
			return
		}
		_, _ = io.WriteString(w, "shared file")
	}))
	defer server.Close()
	response, err := NewHTTPClient(server.URL+"/fs", server.Client()).Get(server.URL + "/fs/file")
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "signaling exceeds 128 KiB") {
		t.Fatalf("oversized discovery metadata error = %v", err)
	}
}

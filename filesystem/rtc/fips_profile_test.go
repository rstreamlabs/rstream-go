//go:build rstream_fips

// See LICENSE file in the project root for license information.

package rtc

import (
	"context"
	"github.com/pion/webrtc/v4"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFIPSProfileRejectsFilesystemSignalingBeforeICE(t *testing.T) {
	var calls atomic.Int32
	server := NewServer(ServerConfig{ICE: func(context.Context) ([]webrtc.ICEServer, error) {
		calls.Add(1)
		return nil, nil
	}})
	defer server.Close()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, Endpoint, nil))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "FIPS profile") {
		t.Fatalf("signaling status = %d, body = %s", response.Code, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("ICE calls = %d", calls.Load())
	}
}

func TestFIPSProfileRejectsFilesystemPeer(t *testing.T) {
	peer, err := NewPeer(nil, false)
	if peer != nil {
		defer peer.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "FIPS profile") {
		t.Fatalf("NewPeer() error = %v", err)
	}
}

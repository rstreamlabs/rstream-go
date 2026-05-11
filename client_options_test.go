// See LICENSE file in the project root for license information.

package rstream

import (
	"crypto/tls"
	"strings"
	"testing"
)

func TestNewClientValidatesAndCopiesOptions(t *testing.T) {
	if _, err := NewClient(ClientOptions{}); err == nil || !strings.Contains(err.Error(), "engine is required") {
		t.Fatalf("expected engine validation error, got %v", err)
	}
	zeroRTT := false
	tlsCfg := &tls.Config{ServerName: "engine.example.com"}
	transport := &Transport{ForceIPv4: BoolPtr(true)}
	client, err := NewClient(ClientOptions{
		Engine:          "engine.example.com:443",
		Token:           "token",
		Transport:       transport,
		TLSClientConfig: tlsCfg,
		NoToken:         true,
		ZeroRTT:         &zeroRTT,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.EngineURL == nil || *client.EngineURL != "engine.example.com:443" {
		t.Fatalf("engine not applied: %#v", client.EngineURL)
	}
	if client.Token == nil || *client.Token != "token" {
		t.Fatalf("token not applied: %#v", client.Token)
	}
	if client.Transport != transport || client.TLSClientConfig != tlsCfg || client.ZeroRTT != &zeroRTT {
		t.Fatalf("client did not preserve option references")
	}
	if client.NoToken == nil || !*client.NoToken {
		t.Fatalf("NoToken flag not applied")
	}
}

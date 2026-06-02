// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func TestEventsHandlerWritesRawWatchEventsByDefault(t *testing.T) {
	var stdout bytes.Buffer
	handler, err := newEventsHandler(t.Context(), eventsHandlerOptions{Stdout: &stdout})
	if err != nil {
		t.Fatalf("newEventsHandler() error = %v", err)
	}
	if err := handler(rstream.Event{Type: "state.initial", Object: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"type":"state.initial","object":{"ok":true}}` {
		t.Fatalf("stdout = %q", got)
	}
}

func TestEventsHandlerWebhookModeWritesWebhookPayloads(t *testing.T) {
	var stdout bytes.Buffer
	handler, err := newEventsHandler(t.Context(), eventsHandlerOptions{
		WebhookMode:   true,
		WebhookSecret: "whsec_test",
		WebhookID:     "cli_we_test",
		Stdout:        &stdout,
		Now:           fixedEventsNow,
		NewID:         fixedEventsID,
	})
	if err != nil {
		t.Fatalf("newEventsHandler() error = %v", err)
	}
	if err := handler(rstream.Event{Type: "state.initial", Object: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("state handler() error = %v", err)
	}
	if err := handler(rstream.Event{
		ID:        "evt_1",
		Type:      "tunnel.created",
		CreatedAt: "2026-06-02T12:00:00Z",
		Object:    json.RawMessage(`{"id":"tunnel-1"}`),
	}); err != nil {
		t.Fatalf("webhook handler() error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"id":"evt_1","type":"tunnel.created","created_at":"2026-06-02T12:00:00Z","object":{"id":"tunnel-1"}}` {
		t.Fatalf("stdout = %q", got)
	}
}

func TestEventsHandlerWebhookModeCanIncludeHeaders(t *testing.T) {
	var stdout bytes.Buffer
	handler, err := newEventsHandler(t.Context(), eventsHandlerOptions{
		WebhookMode:    true,
		WebhookSecret:  "whsec_test",
		WebhookID:      "cli_we_test",
		IncludeHeaders: true,
		Stdout:         &stdout,
		Now:            fixedEventsNow,
		NewID:          fixedEventsID,
	})
	if err != nil {
		t.Fatalf("newEventsHandler() error = %v", err)
	}
	if err := handler(rstream.Event{
		Type:   "client.deleted",
		Object: json.RawMessage(`{"id":"client-1"}`),
	}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	var out struct {
		Headers map[string]string    `json:"headers"`
		Body    rstream.WebhookEvent `json:"body"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.Body.ID != "evt_cli_fixed" || out.Body.Type != "client.deleted" {
		t.Fatalf("unexpected body: %#v", out.Body)
	}
	if out.Headers[rstream.WebhookEventIDHeader] != "evt_cli_fixed" || out.Headers[rstream.WebhookDeliveryIDHeader] != "cli_del_fixed" {
		t.Fatalf("unexpected headers: %#v", out.Headers)
	}
	if !strings.HasPrefix(out.Headers[rstream.WebhookSignatureHeader], "t=1780401600,v1=") {
		t.Fatalf("unexpected signature header: %#v", out.Headers)
	}
}

func TestEventsHandlerWebhookModeForwardsSignedPayloads(t *testing.T) {
	var forwardedBody []byte
	forwardedHeaders := http.Header{}
	forwarder := func(ctx context.Context, body []byte, headers http.Header) error {
		forwardedBody = append([]byte(nil), body...)
		for key, values := range headers {
			forwardedHeaders[key] = append([]string(nil), values...)
		}
		return nil
	}
	handler, err := newEventsHandler(t.Context(), eventsHandlerOptions{
		WebhookMode:   true,
		WebhookSecret: "whsec_test",
		WebhookID:     "cli_we_test",
		Forward:       forwarder,
		Now:           fixedEventsNow,
		NewID:         fixedEventsID,
	})
	if err != nil {
		t.Fatalf("newEventsHandler() error = %v", err)
	}
	if err := handler(rstream.Event{ID: "evt_1", Type: "client.created", Object: json.RawMessage(`{"id":"client-1"}`)}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if !bytes.Contains(forwardedBody, []byte(`"type":"client.created"`)) {
		t.Fatalf("forwarded body = %s", forwardedBody)
	}
	if forwardedHeaders.Get(rstream.WebhookIDHeader) != "cli_we_test" || forwardedHeaders.Get(rstream.WebhookDeliveryIDHeader) != "cli_del_fixed" {
		t.Fatalf("forwarded headers = %#v", forwardedHeaders)
	}
}

func TestEventsForwarderPostsHeadersAndBody(t *testing.T) {
	receivedBody := make(chan []byte, 1)
	receivedHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		receivedBody <- body
		receivedHeaders <- req.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	forwarder, err := newEventsForwarder(server.URL, false)
	if err != nil {
		t.Fatalf("newEventsForwarder() error = %v", err)
	}
	headers := http.Header{}
	headers.Set(rstream.WebhookSignatureHeader, "t=1780401600,v1=test")
	headers.Set(rstream.WebhookEventIDHeader, "evt_1")
	headers.Set(rstream.WebhookEventTypeHeader, "tunnel.created")
	headers.Set(rstream.WebhookIDHeader, "we_1")
	headers.Set(rstream.WebhookDeliveryIDHeader, "del_1")
	if err := forwarder(t.Context(), []byte(`{"id":"evt_1"}`), headers); err != nil {
		t.Fatalf("forwarder() error = %v", err)
	}
	if got := string(<-receivedBody); got != `{"id":"evt_1"}` {
		t.Fatalf("body = %q", got)
	}
	out := <-receivedHeaders
	if out.Get("Content-Type") != "application/json" || out.Get(rstream.WebhookSignatureHeader) == "" {
		t.Fatalf("headers = %#v", out)
	}
	if out.Get(rstream.WebhookEventIDHeader) != "evt_1" || out.Get(rstream.WebhookDeliveryIDHeader) != "del_1" {
		t.Fatalf("webhook headers = %#v", out)
	}
}

func TestEventsForwarderSupportsHTTPS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	forwarder, err := newEventsForwarder(server.URL, true)
	if err != nil {
		t.Fatalf("newEventsForwarder() error = %v", err)
	}
	if err := forwarder(t.Context(), []byte(`{"id":"evt_1"}`), nil); err != nil {
		t.Fatalf("forwarder() error = %v", err)
	}
}

func TestEventsForwarderRejectsUnsupportedSchemes(t *testing.T) {
	_, err := newEventsForwarder("ftp://example.test/hooks", false)
	if err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("newEventsForwarder() error = %v, want unsupported scheme error", err)
	}
}

func fixedEventsNow() time.Time {
	return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
}

func fixedEventsID(prefix string) (string, error) {
	return strings.TrimRight(prefix, "_") + "_fixed", nil
}

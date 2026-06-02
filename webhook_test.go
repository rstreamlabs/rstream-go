// See LICENSE file in the project root for license information.

package rstream

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEventToWebhookEvent(t *testing.T) {
	event := Event{
		ID:          "evt_1",
		Type:        "tunnel.created",
		CreatedAt:   "2026-06-02T12:00:00Z",
		UserID:      "user-1",
		WorkspaceID: "workspace-1",
		ProjectID:   "project-1",
		ClusterID:   "cluster-1",
		Object:      json.RawMessage(`{"id":"tunnel-1"}`),
	}
	webhookEvent, err := EventToWebhookEvent(event, "", time.Time{})
	if err != nil {
		t.Fatalf("EventToWebhookEvent() error = %v", err)
	}
	if webhookEvent.ID != event.ID || webhookEvent.Type != event.Type || webhookEvent.CreatedAt != event.CreatedAt {
		t.Fatalf("unexpected webhook event identity: %#v", webhookEvent)
	}
	if string(webhookEvent.Object) != `{"id":"tunnel-1"}` {
		t.Fatalf("object = %s", webhookEvent.Object)
	}
}

func TestEventToWebhookEventUsesFallbacks(t *testing.T) {
	createdAt := time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC)
	webhookEvent, err := EventToWebhookEvent(Event{Type: "client.deleted", Object: json.RawMessage(`{"id":"client-1"}`)}, "evt_cli_1", createdAt)
	if err != nil {
		t.Fatalf("EventToWebhookEvent() error = %v", err)
	}
	if webhookEvent.ID != "evt_cli_1" || webhookEvent.CreatedAt != "2026-06-02T12:30:00Z" {
		t.Fatalf("fallbacks not applied: %#v", webhookEvent)
	}
}

func TestEventToWebhookEventRejectsNonDeliverableEvents(t *testing.T) {
	for _, eventType := range []string{"state.initial", "client.updated", "tunnel.updated", "stream.summary"} {
		if _, err := EventToWebhookEvent(Event{ID: "evt_1", Type: eventType}, "", time.Time{}); err == nil || !strings.Contains(err.Error(), "not deliverable") {
			t.Fatalf("EventToWebhookEvent(%q) error = %v, want non-deliverable error", eventType, err)
		}
	}
	if _, err := EventToWebhookEvent(Event{Type: "client.created"}, "", time.Time{}); err == nil || !strings.Contains(err.Error(), "event id") {
		t.Fatalf("expected missing event id error, got %v", err)
	}
}

func TestWebhookSigningSecretGeneration(t *testing.T) {
	secret, err := GenerateWebhookSigningSecret()
	if err != nil {
		t.Fatalf("GenerateWebhookSigningSecret() error = %v", err)
	}
	if !regexp.MustCompile(`^whsec_[A-Za-z0-9_-]{43}$`).MatchString(secret) {
		t.Fatalf("unexpected secret format: %q", secret)
	}
}

func TestSignWebhookPayload(t *testing.T) {
	timestamp := time.Unix(1_700_000_000, 0)
	signature, err := SignWebhookPayload([]byte(`{"id":"evt_1"}`), "whsec_test", timestamp)
	if err != nil {
		t.Fatalf("SignWebhookPayload() error = %v", err)
	}
	want := "t=1700000000,v1=c89214b5b5da833daed6f0b8c5bb6bd58cea9022bd80ccc78230f3942d632925"
	if signature != want {
		t.Fatalf("signature = %q, want %q", signature, want)
	}
	if _, err := SignWebhookPayload(nil, " ", timestamp); err == nil || !strings.Contains(err.Error(), "signing secret") {
		t.Fatalf("expected missing secret error, got %v", err)
	}
}

func TestBuildWebhookHeaderValues(t *testing.T) {
	event := WebhookEvent{ID: "evt_1", Type: "client.created"}
	headers, err := BuildWebhookHeaderValues(
		[]byte(`{"id":"evt_1"}`),
		event,
		"whsec_test",
		WebhookHeaderOptions{
			WebhookID:  "cli_we_1",
			DeliveryID: "cli_del_1",
			Timestamp:  time.Unix(1_700_000_000, 0),
		},
	)
	if err != nil {
		t.Fatalf("BuildWebhookHeaderValues() error = %v", err)
	}
	out := http.Header{}
	headers.ApplyTo(out)
	if out.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", out.Get("Content-Type"))
	}
	if out.Get(WebhookSignatureHeader) == "" || out.Get(WebhookEventIDHeader) != "evt_1" || out.Get(WebhookEventTypeHeader) != "client.created" {
		t.Fatalf("unexpected headers: %#v", out)
	}
	if out.Get(WebhookIDHeader) != "cli_we_1" || out.Get(WebhookDeliveryIDHeader) != "cli_del_1" {
		t.Fatalf("unexpected webhook headers: %#v", out)
	}
	if _, err := BuildWebhookHeaderValues(
		[]byte(`{"id":"evt_1"}`),
		WebhookEvent{Type: "client.created"},
		"whsec_test",
		WebhookHeaderOptions{WebhookID: "cli_we_1", DeliveryID: "cli_del_1"},
	); err == nil || !strings.Contains(err.Error(), "event id") {
		t.Fatalf("expected missing event id error, got %v", err)
	}
	if _, err := BuildWebhookHeaderValues(
		[]byte(`{"id":"evt_1"}`),
		WebhookEvent{ID: "evt_1"},
		"whsec_test",
		WebhookHeaderOptions{WebhookID: "cli_we_1", DeliveryID: "cli_del_1"},
	); err == nil || !strings.Contains(err.Error(), "event type") {
		t.Fatalf("expected missing event type error, got %v", err)
	}
}

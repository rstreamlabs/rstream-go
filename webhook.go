// See LICENSE file in the project root for license information.

package rstream

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	WebhookSignatureHeader  = "rstream-signature"
	WebhookEventIDHeader    = "rstream-event-id"
	WebhookEventTypeHeader  = "rstream-event-type"
	WebhookIDHeader         = "rstream-webhook-id"
	WebhookDeliveryIDHeader = "rstream-delivery-id"
)

const webhookSecretBytes = 32

type WebhookEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	CreatedAt   string          `json:"created_at,omitempty"`
	UserID      string          `json:"user_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	ClusterID   string          `json:"cluster_id,omitempty"`
	Object      json.RawMessage `json:"object"`
}

type WebhookHeaderValues struct {
	Signature  string
	EventID    string
	EventType  string
	WebhookID  string
	DeliveryID string
}

type WebhookHeaderOptions struct {
	WebhookID  string
	DeliveryID string
	Timestamp  time.Time
}

func IsWebhookDeliverableEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "client.created", "client.deleted", "tunnel.created", "tunnel.deleted":
		return true
	default:
		return false
	}
}

func EventToWebhookEvent(event Event, fallbackID string, createdAt time.Time) (WebhookEvent, error) {
	eventType := strings.TrimSpace(event.Type)
	if !IsWebhookDeliverableEventType(eventType) {
		return WebhookEvent{}, fmt.Errorf("%q is not deliverable as a webhook event", event.Type)
	}
	eventID := strings.TrimSpace(event.ID)
	if eventID == "" {
		eventID = strings.TrimSpace(fallbackID)
	}
	if eventID == "" {
		return WebhookEvent{}, errors.New("webhook event id is required")
	}
	timestamp := strings.TrimSpace(event.CreatedAt)
	if timestamp == "" {
		timestamp = createdAt.UTC().Format(time.RFC3339Nano)
	}
	return WebhookEvent{
		ID:          eventID,
		Type:        eventType,
		CreatedAt:   timestamp,
		UserID:      event.UserID,
		WorkspaceID: event.WorkspaceID,
		ProjectID:   event.ProjectID,
		ClusterID:   event.ClusterID,
		Object:      event.Object,
	}, nil
}

func GenerateWebhookSigningSecret() (string, error) {
	buf := make([]byte, webhookSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func SignWebhookPayload(body []byte, secret string, timestamp time.Time) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", errors.New("webhook signing secret is required")
	}
	unixTimestamp := fmt.Sprintf("%d", timestamp.UTC().Unix())
	payload := append([]byte(unixTimestamp+"."), body...)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "t=" + unixTimestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil)), nil
}

func BuildWebhookHeaderValues(body []byte, event WebhookEvent, secret string, opts WebhookHeaderOptions) (WebhookHeaderValues, error) {
	eventID := strings.TrimSpace(event.ID)
	if eventID == "" {
		return WebhookHeaderValues{}, errors.New("webhook event id is required")
	}
	eventType := strings.TrimSpace(event.Type)
	if eventType == "" {
		return WebhookHeaderValues{}, errors.New("webhook event type is required")
	}
	webhookID := strings.TrimSpace(opts.WebhookID)
	if webhookID == "" {
		return WebhookHeaderValues{}, errors.New("webhook id is required")
	}
	deliveryID := strings.TrimSpace(opts.DeliveryID)
	if deliveryID == "" {
		return WebhookHeaderValues{}, errors.New("webhook delivery id is required")
	}
	timestamp := opts.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	signature, err := SignWebhookPayload(body, secret, timestamp)
	if err != nil {
		return WebhookHeaderValues{}, err
	}
	return WebhookHeaderValues{
		Signature:  signature,
		EventID:    eventID,
		EventType:  eventType,
		WebhookID:  webhookID,
		DeliveryID: deliveryID,
	}, nil
}

func (h WebhookHeaderValues) ApplyTo(headers http.Header) {
	headers.Set("Content-Type", "application/json")
	headers.Set(WebhookSignatureHeader, h.Signature)
	headers.Set(WebhookEventIDHeader, h.EventID)
	headers.Set(WebhookEventTypeHeader, h.EventType)
	headers.Set(WebhookIDHeader, h.WebhookID)
	headers.Set(WebhookDeliveryIDHeader, h.DeliveryID)
}

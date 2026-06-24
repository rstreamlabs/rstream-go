// See LICENSE file in the project root for license information.

package rstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebTTYAPIClientRoundTripAgainstLocalTLSServer(t *testing.T) {
	token := "api-token"
	now := time.Date(2026, 6, 8, 12, 30, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.EscapedPath() {
		case "/api/webtty/capabilities":
			writeWebTTYAPITestJSON(t, w, WebTTYCapabilities{
				ManagedProtocol:       true,
				StoreConfigured:       true,
				SessionListing:        true,
				Recording:             true,
				Replay:                true,
				E2E:                   true,
				ImplementedTransports: []WebTTYTransport{WebTTYTransportPlain, WebTTYTransportWebSocket, WebTTYTransportWebTransport},
				RecordingModes:        []WebTTYRecordingMode{WebTTYRecordingModeRecorded},
				EncryptionModes:       []WebTTYEncryptionMode{WebTTYEncryptionModeManaged, WebTTYEncryptionModeE2E},
				RequiredPermissions:   map[string][]string{"sessions_read": {"webtty.sessions.read-only"}},
			})
		case "/api/webtty/sessions":
			if got := r.URL.Query().Get("params"); !strings.Contains(got, `"server_id":"prod-shell"`) || !strings.Contains(got, `"status":"active"`) {
				t.Fatalf("sessions params = %q", got)
			}
			writeWebTTYAPITestJSON(t, w, []WebTTYSession{webTTYAPITestSession(now)})
		case "/api/webtty/sessions/session%2F1":
			writeWebTTYAPITestJSON(t, w, webTTYAPITestSession(now))
		case "/api/webtty/groups":
			if got := r.URL.Query().Get("params"); !strings.Contains(got, `"origin":"codex"`) {
				t.Fatalf("groups params = %q", got)
			}
			writeWebTTYAPITestJSON(t, w, []WebTTYSessionGroup{{ID: "group-1", ProjectID: "project-1", CreatedAt: now, UpdatedAt: now}})
		case "/api/webtty/groups/group%2F1":
			writeWebTTYAPITestJSON(t, w, WebTTYSessionGroup{ID: "group/1", ProjectID: "project-1", CreatedAt: now, UpdatedAt: now})
		case "/api/webtty/sessions/session%2F1/events":
			if got := r.URL.Query().Get("params"); !strings.Contains(got, `"from_seq":"7"`) {
				t.Fatalf("events params = %q", got)
			}
			writeWebTTYAPITestJSON(t, w, []WebTTYSessionEvent{{
				ID:                "event-1",
				SessionID:         "session/1",
				Seq:               "8",
				CreatedAt:         now,
				Type:              WebTTYSessionEventData,
				Direction:         WebTTYDirectionServerToClient,
				StreamType:        WebTTYStreamTypeStdout,
				PayloadLength:     2,
				PayloadCiphertext: []byte{1, 2},
				PayloadPlaintext:  []byte("ok"),
				Crypto:            &WebTTYCryptoMetadata{PayloadSuite: "aes-256-gcm", PayloadKeyID: "key-1"},
				Hash:              []byte{3, 4},
			}})
		case "/api/webtty/sessions/session%2F1/participants":
			if r.Method == http.MethodPost {
				var body AttachWebTTYParticipantRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode attach body: %v", err)
				}
				if body.Role != string(WebTTYParticipantRoleSpectator) || body.DeviceID != "device-1" || body.Transport != WebTTYTransportWebSocket {
					t.Fatalf("attach body = %#v", body)
				}
				writeWebTTYAPITestJSON(t, w, WebTTYParticipant{ID: "participant-1", SessionID: "session/1", Role: WebTTYParticipantRoleSpectator, AttachedAt: now, AttachGrant: []byte("grant"), AttachGrantExpiresAt: &now})
				return
			}
			writeWebTTYAPITestJSON(t, w, []WebTTYParticipant{{ID: "participant-1", SessionID: "session/1", Role: WebTTYParticipantRoleSpectator, AttachedAt: now, Live: WebTTYParticipantLive{Connected: true}}})
		case "/api/webtty/sessions/session%2F1/participants/participant%2F1":
			if r.Method != http.MethodPost {
				t.Fatalf("detach method = %s", r.Method)
			}
			writeWebTTYAPITestJSON(t, w, WebTTYParticipant{ID: "participant/1", SessionID: "session/1", Role: WebTTYParticipantRoleSpectator, AttachedAt: now, DetachedAt: &now})
		case "/api/webtty/sessions/session%2F1/key-grants":
			writeWebTTYAPITestJSON(t, w, []WebTTYKeyGrant{{ID: "grant-1", SessionID: "session/1", RecipientID: "device-1", RecipientKind: "workspace_device", Crypto: WebTTYCryptoMetadata{KeyEnvelopeSuite: "hpke"}, CreatedAt: now}})
		case "/api/webtty/sessions/session%2F1/key-grants/decrypt-material":
			if r.URL.Query().Get("recipient_id") != "device-1" || r.URL.Query().Get("recipient_kind") != "workspace_device" {
				t.Fatalf("unexpected decrypt material query: %s", r.URL.RawQuery)
			}
			writeWebTTYAPITestJSON(t, w, []WebTTYKeyGrantDecryptMaterial{{ID: "grant-1", SessionID: "session/1", RecipientID: "device-1", RecipientKind: "workspace_device", WrappedKey: []byte{5, 6}, Crypto: WebTTYCryptoMetadata{KeyEnvelopeSuite: "hpke", KeyContextRaw: "Y3R4"}, CreatedAt: now}})
		case "/api/webtty/sessions/session%2F1/control-requests":
			if r.Method == http.MethodPost {
				var body CreateWebTTYControlRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode control request body: %v", err)
				}
				if body.ParticipantID != "participant-1" || body.Reason != "need shell" {
					t.Fatalf("control request body = %#v", body)
				}
				writeWebTTYAPITestJSON(t, w, WebTTYControlRequest{ID: "request-1", SessionID: "session/1", RequesterParticipantID: "participant-1", Status: WebTTYControlRequestPending, CreatedAt: now, UpdatedAt: now})
				return
			} else if got := r.URL.Query().Get("params"); !strings.Contains(got, `"status":"pending"`) {
				t.Fatalf("control requests params = %q", got)
			}
			writeWebTTYAPITestJSON(t, w, []WebTTYControlRequest{{ID: "request-1", SessionID: "session/1", RequesterParticipantID: "participant-1", Status: WebTTYControlRequestPending, CreatedAt: now, UpdatedAt: now}})
		case "/api/webtty/sessions/session%2F1/control-requests/request%2F1":
			if r.Method != http.MethodPost {
				t.Fatalf("resolve method = %s", r.Method)
			}
			var body ResolveWebTTYControlRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode resolve body: %v", err)
			}
			if body.Action != "grant" || body.ApproverParticipantID != "approver-1" {
				t.Fatalf("resolve body = %#v", body)
			}
			writeWebTTYAPITestJSON(t, w, WebTTYControlRequest{ID: "request/1", SessionID: "session/1", RequesterParticipantID: "participant-1", ApproverParticipantID: "approver-1", Status: WebTTYControlRequestGranted, CreatedAt: now, UpdatedAt: now, ResolvedAt: &now})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testAPIClient(server, token)
	capabilities, err := client.GetWebTTYCapabilities(t.Context())
	if err != nil {
		t.Fatalf("GetWebTTYCapabilities() error = %v", err)
	}
	if !capabilities.ManagedProtocol || len(capabilities.ImplementedTransports) != 3 || capabilities.LiveAttach || capabilities.ControlTransfer {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	status := string(WebTTYSessionStatusActive)
	serverID := "prod-shell"
	sessions, err := client.ListWebTTYSessions(t.Context(), &ListWebTTYSessionsParams{Filters: &ListWebTTYSessionsFilters{ServerID: &serverID, Status: &status}})
	if err != nil {
		t.Fatalf("ListWebTTYSessions() error = %v", err)
	}
	if len(*sessions) != 1 || (*sessions)[0].ID != "session/1" || (*sessions)[0].EncryptionMode != WebTTYEncryptionModeE2E {
		t.Fatalf("sessions = %#v", sessions)
	}
	session, err := client.GetWebTTYSession(t.Context(), "session/1")
	if err != nil {
		t.Fatalf("GetWebTTYSession() error = %v", err)
	}
	if session.Context.Origin != WebTTYOriginCodex || session.DownTransport != WebTTYTransportWebSocket || !session.Live.Attachable || !session.Live.HasUpstream {
		t.Fatalf("session = %#v", session)
	}
	origin := string(WebTTYOriginCodex)
	groups, err := client.ListWebTTYSessionGroups(t.Context(), &ListWebTTYSessionGroupsParams{Filters: &ListWebTTYSessionGroupsFilters{Origin: &origin}})
	if err != nil {
		t.Fatalf("ListWebTTYSessionGroups() error = %v", err)
	}
	if len(*groups) != 1 || (*groups)[0].ID != "group-1" {
		t.Fatalf("groups = %#v", groups)
	}
	if group, err := client.GetWebTTYSessionGroup(t.Context(), "group/1"); err != nil || group.ID != "group/1" {
		t.Fatalf("GetWebTTYSessionGroup() = %#v, %v", group, err)
	}
	fromSeq := "7"
	events, err := client.ListWebTTYSessionEvents(t.Context(), "session/1", &ListWebTTYSessionEventsParams{FromSeq: &fromSeq})
	if err != nil {
		t.Fatalf("ListWebTTYSessionEvents() error = %v", err)
	}
	if len(*events) != 1 || (*events)[0].Seq != "8" || string((*events)[0].PayloadCiphertext) != string([]byte{1, 2}) || string((*events)[0].PayloadPlaintext) != "ok" {
		t.Fatalf("events = %#v", events)
	}
	participants, err := client.ListWebTTYParticipants(t.Context(), "session/1")
	if err != nil {
		t.Fatalf("ListWebTTYParticipants() error = %v", err)
	}
	if len(*participants) != 1 || (*participants)[0].Role != WebTTYParticipantRoleSpectator || !(*participants)[0].Live.Connected {
		t.Fatalf("participants = %#v", participants)
	}
	attached, err := client.AttachWebTTYParticipant(t.Context(), "session/1", AttachWebTTYParticipantRequest{Role: string(WebTTYParticipantRoleSpectator), DeviceID: "device-1", Transport: WebTTYTransportWebSocket})
	if err != nil {
		t.Fatalf("AttachWebTTYParticipant() error = %v", err)
	}
	if string(attached.AttachGrant) != "grant" || attached.AttachGrantExpiresAt == nil {
		t.Fatalf("attached participant grant = %#v", attached)
	}
	if participant, err := client.DetachWebTTYParticipant(t.Context(), "session/1", "participant/1", DetachWebTTYParticipantRequest{Reason: "done"}); err != nil || participant.DetachedAt == nil {
		t.Fatalf("DetachWebTTYParticipant() = %#v, %v", participant, err)
	}
	grants, err := client.ListWebTTYKeyGrants(t.Context(), "session/1")
	if err != nil {
		t.Fatalf("ListWebTTYKeyGrants() error = %v", err)
	}
	if len(*grants) != 1 {
		t.Fatalf("metadata grants should not include decrypt material: %#v", grants)
	}
	recipientID := "device-1"
	recipientKind := "workspace_device"
	decryptGrants, err := client.ListWebTTYKeyGrantDecryptMaterial(t.Context(), "session/1", &ListWebTTYKeyGrantDecryptMaterialParams{RecipientID: &recipientID, RecipientKind: &recipientKind})
	if err != nil {
		t.Fatalf("ListWebTTYKeyGrantDecryptMaterial() error = %v", err)
	}
	if len(*decryptGrants) != 1 || string((*decryptGrants)[0].WrappedKey) != string([]byte{5, 6}) || (*decryptGrants)[0].Crypto.KeyContextRaw != "Y3R4" {
		t.Fatalf("decrypt grants = %#v", decryptGrants)
	}
	pending := string(WebTTYControlRequestPending)
	requests, err := client.ListWebTTYControlRequests(t.Context(), "session/1", &ListWebTTYControlRequestsParams{Filters: &ListWebTTYControlRequestsFilters{Status: &pending}})
	if err != nil {
		t.Fatalf("ListWebTTYControlRequests() error = %v", err)
	}
	if len(*requests) != 1 || (*requests)[0].Status != WebTTYControlRequestPending {
		t.Fatalf("requests = %#v", requests)
	}
	if _, err := client.CreateWebTTYControlRequest(t.Context(), "session/1", CreateWebTTYControlRequest{ParticipantID: "participant-1", Reason: "need shell"}); err != nil {
		t.Fatalf("CreateWebTTYControlRequest() error = %v", err)
	}
	resolved, err := client.ResolveWebTTYControlRequest(t.Context(), "session/1", "request/1", ResolveWebTTYControlRequest{Action: "grant", ApproverParticipantID: "approver-1"})
	if err != nil {
		t.Fatalf("ResolveWebTTYControlRequest() error = %v", err)
	}
	if resolved.Status != WebTTYControlRequestGranted || resolved.ResolvedAt == nil {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestWebTTYSessionEventRejectsDecryptMaterialMetadata(t *testing.T) {
	var event WebTTYSessionEvent
	err := json.Unmarshal([]byte(`{
		"id": "event-1",
		"session_id": "session-1",
		"seq": "1",
		"created_at": "2026-06-06T12:00:01Z",
		"type": "data",
		"direction": "server_to_client",
		"stream_type": "stdout",
		"crypto": {
			"payload_suite": "aes-256-gcm",
			"key_envelopes": [{"recipient_key_id": "device-1"}]
		}
	}`), &event)
	if err == nil || !strings.Contains(err.Error(), "key_envelopes") {
		t.Fatalf("expected event key_envelopes rejection, got %v", err)
	}
}

func TestWebTTYKeyGrantRejectsDecryptMaterialMetadata(t *testing.T) {
	var grant WebTTYKeyGrant
	err := json.Unmarshal([]byte(`{
		"id": "grant-1",
		"session_id": "session-1",
		"recipient_id": "device-1",
		"recipient_kind": "workspace_device",
		"wrapped_key": "AQID",
		"crypto": {
			"payload_suite": "aes-256-gcm"
		},
		"created_at": "2026-06-06T12:00:01Z"
	}`), &grant)
	if err == nil || !strings.Contains(err.Error(), "wrapped_key") {
		t.Fatalf("expected wrapped_key rejection, got %v", err)
	}
	err = json.Unmarshal([]byte(`{
		"id": "grant-1",
		"session_id": "session-1",
		"recipient_id": "device-1",
		"recipient_kind": "workspace_device",
		"crypto": {
			"payload_suite": "aes-256-gcm",
			"key_envelopes": [{"recipient_key_id": "device-1"}]
		},
		"created_at": "2026-06-06T12:00:01Z"
	}`), &grant)
	if err == nil || !strings.Contains(err.Error(), "key_envelopes") {
		t.Fatalf("expected key_envelopes rejection, got %v", err)
	}
	var decryptMaterial WebTTYKeyGrantDecryptMaterial
	err = json.Unmarshal([]byte(`{
		"id": "grant-1",
		"session_id": "session-1",
		"recipient_id": "device-1",
		"recipient_kind": "workspace_device",
		"wrapped_key": "AQID",
		"crypto": {
			"payload_suite": "aes-256-gcm",
			"key_envelopes": [{"recipient_key_id": "device-1"}]
		},
		"created_at": "2026-06-06T12:00:01Z"
	}`), &decryptMaterial)
	if err != nil {
		t.Fatalf("decrypt material must accept wrapped key material: %v", err)
	}
}

func TestWebTTYAPIClientValidatesRequiredIDs(t *testing.T) {
	client := &Client{}
	if _, err := client.GetWebTTYSession(t.Context(), " "); err == nil || !strings.Contains(err.Error(), "session ID is required") {
		t.Fatalf("GetWebTTYSession(blank) = %v", err)
	}
	if _, err := client.GetWebTTYSessionGroup(t.Context(), " "); err == nil || !strings.Contains(err.Error(), "session group ID is required") {
		t.Fatalf("GetWebTTYSessionGroup(blank) = %v", err)
	}
	if _, err := client.ListWebTTYSessionEvents(t.Context(), "", nil); err == nil || !strings.Contains(err.Error(), "session ID is required") {
		t.Fatalf("ListWebTTYSessionEvents(blank) = %v", err)
	}
	if _, err := client.DetachWebTTYParticipant(t.Context(), "session-1", "", DetachWebTTYParticipantRequest{}); err == nil || !strings.Contains(err.Error(), "participant ID is required") {
		t.Fatalf("DetachWebTTYParticipant(blank) = %v", err)
	}
	if _, err := client.ResolveWebTTYControlRequest(t.Context(), "session-1", "", ResolveWebTTYControlRequest{}); err == nil || !strings.Contains(err.Error(), "control request ID is required") {
		t.Fatalf("ResolveWebTTYControlRequest(blank) = %v", err)
	}
}

func webTTYAPITestSession(now time.Time) WebTTYSession {
	return WebTTYSession{
		ID:              "session/1",
		ProjectID:       "project-1",
		ServerID:        "prod-shell",
		TunnelID:        "tunnel-1",
		ClientID:        "client-1",
		InitiatorUserID: "user-1",
		GroupID:         "group-1",
		Status:          WebTTYSessionStatusActive,
		SessionMode:     WebTTYSessionModeNonInteractive,
		RecordingMode:   WebTTYRecordingModeRecorded,
		EncryptionMode:  WebTTYEncryptionModeE2E,
		DownTransport:   WebTTYTransportWebSocket,
		UpTransport:     WebTTYTransportPlain,
		Context:         WebTTYContextMetadata{Origin: WebTTYOriginCodex, Purpose: "incident triage", InitiatorKind: WebTTYInitiatorAgent},
		StartedAt:       now,
		Live: WebTTYSessionLive{
			Available:               true,
			Attachable:              true,
			ParticipantCount:        1,
			ControllerParticipantID: "participant-1",
			HasUpstream:             true,
		},
	}
}

func writeWebTTYAPITestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

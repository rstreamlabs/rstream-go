// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetachWebTTYParticipantReconcilesOnlyConfirmedTerminalSessions(t *testing.T) {
	for _, scenario := range []struct {
		name                        string
		initial, verify, retry      int
		status                      WebTTYSessionStatus
		wrongID, invalidJSON        bool
		invalidDetach, disconnected bool
		wantPosts, wantGets         int32
		wantError                   int
	}{
		{name: "healthy detach", wantPosts: 1},
		{name: "invalid detach response", invalidDetach: true, wantPosts: 1, wantError: -1},
		{name: "lost detach transport", disconnected: true, wantPosts: 1, wantError: -1},
		{name: "closed legacy runtime", initial: 503, status: WebTTYSessionStatusClosed, wantPosts: 2, wantGets: 1},
		{name: "errored legacy runtime", initial: 503, status: WebTTYSessionStatusErrored, wantPosts: 2, wantGets: 1},
		{name: "active runtime", initial: 503, status: WebTTYSessionStatusActive, wantPosts: 1, wantGets: 1, wantError: 503},
		{name: "closing runtime", initial: 503, status: WebTTYSessionStatusClosing, wantPosts: 1, wantGets: 1, wantError: 503},
		{name: "opening runtime", initial: 503, status: WebTTYSessionStatusOpening, wantPosts: 1, wantGets: 1, wantError: 503},
		{name: "unknown state", initial: 503, wantPosts: 1, wantGets: 1, wantError: 503},
		{name: "different session", initial: 503, status: WebTTYSessionStatusClosed, wrongID: true, wantPosts: 1, wantGets: 1, wantError: 503},
		{name: "initial unauthenticated", initial: 401, wantPosts: 1, wantError: 401},
		{name: "initial forbidden", initial: 403, wantPosts: 1, wantError: 403},
		{name: "initial missing participant", initial: 404, wantPosts: 1, wantError: 404},
		{name: "initial store failure", initial: 500, wantPosts: 1, wantError: 500},
		{name: "verification unauthenticated", initial: 503, verify: 401, wantPosts: 1, wantGets: 1, wantError: 401},
		{name: "verification forbidden", initial: 503, verify: 403, wantPosts: 1, wantGets: 1, wantError: 403},
		{name: "verification missing session", initial: 503, verify: 404, wantPosts: 1, wantGets: 1, wantError: 404},
		{name: "verification store failure", initial: 503, verify: 500, wantPosts: 1, wantGets: 1, wantError: 500},
		{name: "invalid verification", initial: 503, invalidJSON: true, wantPosts: 1, wantGets: 1, wantError: -1},
		{name: "retry unauthenticated", initial: 503, status: WebTTYSessionStatusClosed, retry: 401, wantPosts: 2, wantGets: 1, wantError: 401},
		{name: "retry forbidden", initial: 503, status: WebTTYSessionStatusClosed, retry: 403, wantPosts: 2, wantGets: 1, wantError: 403},
		{name: "retry missing participant", initial: 503, status: WebTTYSessionStatusClosed, retry: 404, wantPosts: 2, wantGets: 1, wantError: 404},
		{name: "retry unavailable", initial: 503, status: WebTTYSessionStatusClosed, retry: 503, wantPosts: 2, wantGets: 1, wantError: 503},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			var posts, gets atomic.Int32
			request := DetachWebTTYParticipantRequest{Reason: "reader disconnected", Metadata: json.RawMessage(`{"source":"reader"}`)}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer detach-token" {
					t.Error("detach reconciliation changed the authenticated principal")
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				switch {
				case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/webtty/sessions/session%2F1/participants/reader%2F1":
					attempt := posts.Add(1)
					var body DetachWebTTYParticipantRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, request) {
						t.Errorf("detach body = %#v, error = %v", body, err)
					}
					if scenario.disconnected {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						if err := connection.Close(); err != nil {
							t.Error(err)
						}
						return
					}
					if scenario.invalidDetach {
						_, _ = w.Write([]byte("invalid JSON"))
						return
					}
					status := scenario.initial
					if attempt > 1 {
						status = scenario.retry
					}
					if status != 0 {
						http.Error(w, http.StatusText(status), status)
						return
					}
					writeWebTTYAPITestJSON(t, w, WebTTYParticipant{ID: "reader/1", SessionID: "session/1"})
				case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/webtty/sessions/session%2F1":
					gets.Add(1)
					if scenario.verify != 0 {
						http.Error(w, http.StatusText(scenario.verify), scenario.verify)
						return
					}
					if scenario.invalidJSON {
						_, _ = w.Write([]byte("invalid JSON"))
						return
					}
					id := "session/1"
					if scenario.wrongID {
						id = "another-session"
					}
					writeWebTTYAPITestJSON(t, w, WebTTYSession{ID: id, Status: scenario.status})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := testAPIClient(server, "detach-token")
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			participant, err := client.DetachWebTTYParticipant(ctx, "session/1", "reader/1", request)
			if scenario.wantError == 0 {
				if err != nil || participant == nil || participant.ID != "reader/1" || participant.SessionID != "session/1" {
					t.Fatalf("detach = %#v, %v", participant, err)
				}
			} else {
				if err == nil || participant != nil {
					t.Fatalf("detach failure = %#v, %v", participant, err)
				}
				var apiErr *APIError
				if scenario.wantError > 0 && (!errors.As(err, &apiErr) || apiErr.StatusCode != scenario.wantError) {
					t.Fatalf("detach error = %v, want HTTP %d", err, scenario.wantError)
				}
				var syntaxErr *json.SyntaxError
				if (scenario.invalidJSON || scenario.invalidDetach) && !errors.As(err, &syntaxErr) {
					t.Fatalf("decode error was lost: %v", err)
				}
				if scenario.disconnected && !errors.Is(err, io.EOF) {
					t.Fatalf("transport error was lost: %v", err)
				}
			}
			if posts.Load() != scenario.wantPosts || gets.Load() != scenario.wantGets {
				t.Fatalf("POST/GET count = %d/%d, want %d/%d", posts.Load(), gets.Load(), scenario.wantPosts, scenario.wantGets)
			}
		})
	}
}

func TestDetachWebTTYParticipantPreservesCallerDeadline(t *testing.T) {
	var posts, gets atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			http.Error(w, "runtime ended", http.StatusServiceUnavailable)
			return
		}
		gets.Add(1)
		// Keep verification in flight until the client closes it. Returning when
		// the caller's timer fires can send an empty 200 before the transport
		// observes cancellation, testing invalid JSON instead of the deadline.
		<-r.Context().Done()
	}))
	defer server.Close()
	client := testAPIClient(server, "detach-token")
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	participant, err := client.DetachWebTTYParticipant(ctx, "session-1", "reader-1", DetachWebTTYParticipantRequest{})
	if participant != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired detach = %#v, %v", participant, err)
	}
	if posts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("POST/GET count after deadline = %d/%d, want 1/1", posts.Load(), gets.Load())
	}
}

func TestDetachWebTTYParticipantCancellationBoundsReconciliation(t *testing.T) {
	for _, stage := range []string{"initial", "verification", "retry"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var posts, gets atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					attempt := posts.Add(1)
					if stage == "initial" || (stage == "retry" && attempt == 2) {
						cancel()
						http.Error(w, "cancelled", http.StatusServiceUnavailable)
						return
					}
					http.Error(w, "runtime ended", http.StatusServiceUnavailable)
					return
				}
				gets.Add(1)
				if stage == "verification" {
					cancel()
					http.Error(w, "cancelled", http.StatusServiceUnavailable)
					return
				}
				writeWebTTYAPITestJSON(t, w, WebTTYSession{ID: "session-1", Status: WebTTYSessionStatusClosed})
			}))
			defer server.Close()
			client := testAPIClient(server, "detach-token")
			defer client.CloseIdleConnections()
			participant, err := client.DetachWebTTYParticipant(ctx, "session-1", "reader-1", DetachWebTTYParticipantRequest{})
			if participant != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled detach = %#v, %v", participant, err)
			}
			wantPosts, wantGets := int32(1), int32(1)
			if stage == "initial" {
				wantGets = 0
			}
			if stage == "retry" {
				wantPosts = 2
			}
			if posts.Load() != wantPosts || gets.Load() != wantGets {
				t.Fatalf("POST/GET count after cancellation = %d/%d, want %d/%d", posts.Load(), gets.Load(), wantPosts, wantGets)
			}
		})
	}
}

func TestDetachWebTTYParticipantConcurrentReconciliationIsBounded(t *testing.T) {
	const callers = 12
	var posts, gets atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			http.Error(w, "runtime ended", http.StatusServiceUnavailable)
			return
		}
		gets.Add(1)
		writeWebTTYAPITestJSON(t, w, WebTTYSession{ID: "session-1", Status: WebTTYSessionStatusClosed})
	}))
	defer server.Close()
	client := testAPIClient(server, "detach-token")
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for range callers {
		workers.Go(func() {
			participant, err := client.DetachWebTTYParticipant(ctx, "session-1", "reader-1", DetachWebTTYParticipantRequest{})
			var apiErr *APIError
			if participant != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("unavailable detach = %#v, %v", participant, err)
			}
		})
	}
	workers.Wait()
	if posts.Load() != callers*2 || gets.Load() != callers {
		t.Fatalf("concurrent POST/GET count = %d/%d, want %d/%d", posts.Load(), gets.Load(), callers*2, callers)
	}
}

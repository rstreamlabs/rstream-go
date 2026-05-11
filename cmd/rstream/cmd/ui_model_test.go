// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func TestUIStoreAppliesEventsAndSortsSnapshot(t *testing.T) {
	store := newUIStore("sse")
	initial := uiInitialState{
		SnapshotAt: time.Now(),
		Clients: []rstream.ClientProperties{
			{ID: "client-offline", Status: "offline"},
			{ID: "client-online", Status: "online"},
		},
		Tunnels: []rstream.TunnelInventory{
			{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tun-b"), Name: rstream.StringPtr("beta")}, Status: "offline"},
			{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tun-a"), Name: rstream.StringPtr("alpha")}, Status: "online"},
		},
	}
	if err := store.applyEvent(rstream.Event{Type: "state.initial", Object: mustJSON(t, initial)}); err != nil {
		t.Fatalf("applyEvent(initial) error = %v", err)
	}
	snapshot := store.snapshot()
	if len(snapshot.Clients) != 2 || snapshot.Clients[0].ID != "client-online" {
		t.Fatalf("clients not sorted by status: %#v", snapshot.Clients)
	}
	if len(snapshot.Tunnels) != 2 || *snapshot.Tunnels[0].ID != "tun-a" {
		t.Fatalf("tunnels not sorted by status/name: %#v", snapshot.Tunnels)
	}
	if err := store.applyEvent(rstream.Event{Type: "client.updated", Object: mustJSON(t, rstream.ClientProperties{ID: "client-offline", Status: "online"})}); err != nil {
		t.Fatalf("applyEvent(client.updated) error = %v", err)
	}
	if err := store.applyEvent(rstream.Event{Type: "tunnel.deleted", Object: mustJSON(t, rstream.TunnelInventory{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tun-b")}})}); err != nil {
		t.Fatalf("applyEvent(tunnel.deleted) error = %v", err)
	}
	snapshot = store.snapshot()
	if snapshot.Clients[0].Status != "online" || len(snapshot.Tunnels) != 1 {
		t.Fatalf("updates/deletes not reflected: %#v", snapshot)
	}
}

func TestUIStoreConnectionStateAndHelpers(t *testing.T) {
	store := newUIStore("ws")
	store.setConnectionState(true, " connected ")
	select {
	case <-store.Changes():
	default:
		t.Fatalf("expected connection state change notification")
	}
	snapshot := store.snapshot()
	if !snapshot.Connected || snapshot.LastError != "connected" {
		t.Fatalf("unexpected connection snapshot: %#v", snapshot)
	}
	if trimOptionalString(nil) != "" || trimOptionalString(rstream.StringPtr(" value ")) != "value" {
		t.Fatalf("trimOptionalString returned unexpected values")
	}
	if uiStatusRank("online") >= uiStatusRank("offline") || uiStatusRank("unknown") <= uiStatusRank("offline") {
		t.Fatalf("uiStatusRank ordering is unexpected")
	}
}

func TestUIStoreRejectsMalformedEvents(t *testing.T) {
	store := newUIStore("sse")
	for _, eventType := range []string{"state.initial", "client.created", "client.deleted", "tunnel.created", "tunnel.deleted"} {
		if err := store.applyEvent(rstream.Event{Type: eventType, Object: json.RawMessage("{")}); err == nil {
			t.Fatalf("applyEvent(%s) accepted malformed JSON", eventType)
		}
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

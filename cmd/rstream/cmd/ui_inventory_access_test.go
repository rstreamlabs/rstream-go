// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rivo/tview"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func uiVisibleTestTunnels() []rstream.TunnelInventory {
	httpProtocol := rstream.ProtocolHTTP
	managedProtocol := rstream.ProtocolWebTTY
	return []rstream.TunnelInventory{
		{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tunnel-http"), Name: rstream.StringPtr("service"), Protocol: &httpProtocol}, ClientID: "unlisted-agent", Status: "online"},
		{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tunnel-shell"), Name: rstream.StringPtr("shell"), Protocol: &httpProtocol, Labels: map[string]string{webtty.WebTTYApplicationProtocolKey: webtty.WebTTYApplicationProtocol}}, ClientID: "unlisted-agent", Status: "online"},
		{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tunnel-managed"), Name: rstream.StringPtr("managed-shell"), Protocol: &managedProtocol}, ClientID: "unlisted-agent", Status: "online"},
	}
}

func TestUIRestrictedInventoryOverNetwork(t *testing.T) {
	for _, clientsOnly := range []bool{false, true} {
		for _, transport := range []string{"sse", "websocket"} {
			t.Run(fmt.Sprintf("%s/clientsOnly=%v", transport, clientsOnly), func(t *testing.T) {
				inventory := uiInitialState{Clients: []rstream.ClientProperties{}, Tunnels: uiVisibleTestTunnels()}
				deniedPath := "/api/clients"
				wantClients, wantTunnels, wantServers := 0, 3, 2
				if clientsOnly {
					inventory = uiInitialState{Clients: []rstream.ClientProperties{{ID: "client-visible", Status: "online"}}, Tunnels: []rstream.TunnelInventory{}}
					deniedPath = "/api/tunnels"
					wantClients, wantTunnels, wantServers = 1, 0, 0
				}
				initial := rstream.Event{Type: "state.initial", Object: mustJSON(t, inventory)}
				encoded := mustJSON(t, initial)
				var deniedListRequests atomic.Int32
				upgrader := websocket.Upgrader{}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case deniedPath:
						deniedListRequests.Add(1)
						http.Error(w, "Forbidden", http.StatusForbidden)
					case "/api/sse":
						w.Header().Set("Content-Type", "text/event-stream")
						if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
							t.Errorf("write snapshot: %v", err)
							return
						}
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					case "/api/websocket":
						conn, err := upgrader.Upgrade(w, r, nil)
						if err != nil {
							t.Errorf("upgrade: %v", err)
							return
						}
						defer conn.Close()
						if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
							t.Errorf("initial snapshot: %v", err)
							return
						}
						for {
							if _, _, err := conn.ReadMessage(); err != nil {
								return
							}
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				response, err := server.Client().Get(server.URL + deniedPath)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusForbidden {
					t.Fatalf("client list status = %d", response.StatusCode)
				}
				path, target := writeUIRuntimeTarget(t, server.URL)
				app := &uiApp{resolver: newUIRuntimeResolver(path, uiRuntimeOptions{})}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				result, err := app.prepareRuntimeSwitch(ctx, cancel, target, false, transport, 3*time.Second)
				if err != nil {
					t.Fatalf("start inventory with no client access: %v", err)
				}
				defer result.Close()
				snapshot := result.store.snapshot()
				if !snapshot.Connected || len(snapshot.Clients) != wantClients || len(snapshot.Tunnels) != wantTunnels || len(snapshot.WebTTY) != wantServers {
					t.Fatalf("unexpected inventory: %+v", snapshot)
				}
				if deniedListRequests.Load() != 1 {
					t.Fatalf("UI attempted a separate client list: requests=%d", deniedListRequests.Load())
				}
				app.snapshot = snapshot
				app.table = tview.NewTable()
				app.renderTunnels()
				if len(app.tunnelRows) != wantTunnels {
					t.Fatal("tunnel rows missing without clients")
				}
				app.renderWebTTY()
				if len(app.webttyRows) != wantServers {
					t.Fatal("WebTTY rows missing without clients")
				}
				app.renderClients()
				if len(app.clientRows) != wantClients {
					t.Fatal("empty client view is invalid")
				}
				t.Logf("%s: %s denied, UI connected with permitted inventory", transport, deniedPath)
			})
		}
	}
}

func TestUITunnelEventsWithoutClients(t *testing.T) {
	store := newUIStore("sse")
	store.clients["stale-client"] = rstream.ClientProperties{ID: "stale-client"}
	initial := uiInitialState{Tunnels: uiVisibleTestTunnels()}
	if err := store.applyEvent(rstream.Event{Type: "state.initial", Object: mustJSON(t, initial)}); err != nil {
		t.Fatal(err)
	}
	if len(store.snapshot().Clients) != 0 {
		t.Fatal("restricted snapshot retained old clients")
	}
	tunnel := uiVisibleTestTunnels()[1]
	tunnel.Status = "offline"
	if err := store.applyEvent(rstream.Event{Type: "tunnel.updated", Object: mustJSON(t, tunnel)}); err != nil {
		t.Fatal(err)
	}
	foundOffline := false
	for _, server := range store.snapshot().WebTTY {
		if server.TunnelID == "tunnel-shell" {
			foundOffline = server.Status == "offline"
		}
	}
	if !foundOffline {
		t.Fatal("WebTTY status did not update without clients")
	}
	if err := store.applyEvent(rstream.Event{Type: "tunnel.deleted", Object: mustJSON(t, tunnel)}); err != nil {
		t.Fatal(err)
	}
	if snapshot := store.snapshot(); len(snapshot.Tunnels) != 2 || len(snapshot.WebTTY) != 1 || len(snapshot.Clients) != 0 {
		t.Fatalf("unexpected inventory after removal: %+v", snapshot)
	}
	if err := store.applyEvent(rstream.Event{Type: "tunnel.created", Object: mustJSON(t, tunnel)}); err != nil {
		t.Fatal(err)
	}
	if snapshot := store.snapshot(); len(snapshot.Tunnels) != 3 || len(snapshot.WebTTY) != 2 || len(snapshot.Clients) != 0 {
		t.Fatalf("unexpected inventory after recreation: %+v", snapshot)
	}
}

func TestUIInventoryOmitsUnaddressableSelectedObjects(t *testing.T) {
	store := newUIStore("sse")
	initial := uiInitialState{
		Clients: []rstream.ClientProperties{{ID: ""}, {ID: "client-visible", Status: "online"}},
		Tunnels: []rstream.TunnelInventory{
			{TunnelProperties: rstream.TunnelProperties{ID: nil}},
			{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("")}},
			{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tunnel-visible")}},
		},
	}
	if err := store.applyEvent(rstream.Event{Type: "state.initial", Object: mustJSON(t, initial)}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"created", "updated", "deleted"} {
		for _, kind := range []string{"client", "tunnel"} {
			if err := store.applyEvent(rstream.Event{Type: kind + "." + action, Object: mustJSON(t, map[string]any{"labels": map[string]string{"env": "prod"}})}); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot := store.snapshot()
	if len(snapshot.Clients) != 1 || snapshot.Clients[0].ID != "client-visible" || len(snapshot.Tunnels) != 1 || *snapshot.Tunnels[0].ID != "tunnel-visible" {
		t.Fatalf("unaddressable objects entered UI: %+v", snapshot)
	}
	if err := store.applyEvent(rstream.Event{Type: "state.initial", Object: mustJSON(t, uiInitialState{})}); err != nil {
		t.Fatal(err)
	}
	snapshot = store.snapshot()
	if len(snapshot.Clients) != 0 || len(snapshot.Tunnels) != 0 || len(snapshot.WebTTY) != 0 {
		t.Fatal("replacement retained hidden objects")
	}
}

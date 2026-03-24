// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestUIStoreApplyInitialStateBuildsWebTTYInventory(t *testing.T) {
	t.Parallel()
	hostname := "host-a"
	clientID := "client-1"
	tunnelID := "tunnel-1"
	tunnelName := "webtty-a"
	tunnelType := string(rstream.TunnelTypeBytestream)
	status := "online"
	state := uiInitialState{
		Clients: []rstream.ClientProperties{
			{
				ID:     clientID,
				Status: status,
			},
		},
		Tunnels: []rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:   &tunnelID,
					Name: &tunnelName,
					Type: rstream.TunnelTypePtr(rstream.TunnelType(tunnelType)),
					Labels: map[string]string{
						webtty.WebTTYApplicationProtocolKey: webtty.WebTTYApplicationProtocol,
						webtty.WebTTYHostnameLabelKey:       hostname,
					},
				},
				Status:   status,
				ClientID: clientID,
			},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	store := newUIStore("websocket")
	if err := store.applyEvent(rstream.Event{Type: "state.initial", Object: raw}); err != nil {
		t.Fatalf("applyEvent() error = %v", err)
	}
	snapshot := store.snapshot()
	if got := len(snapshot.Clients); got != 1 {
		t.Fatalf("len(snapshot.Clients) = %d, want 1", got)
	}
	if got := len(snapshot.Tunnels); got != 1 {
		t.Fatalf("len(snapshot.Tunnels) = %d, want 1", got)
	}
	if got := len(snapshot.WebTTY); got != 1 {
		t.Fatalf("len(snapshot.WebTTY) = %d, want 1", got)
	}
	if got := snapshot.WebTTY[0].RstreamURL; got != "rstrm://webtty-a" {
		t.Fatalf("snapshot.WebTTY[0].RstreamURL = %q, want %q", got, "rstrm://webtty-a")
	}
}

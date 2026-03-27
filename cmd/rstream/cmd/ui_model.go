// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

type uiView string

const (
	uiViewClients uiView = "clients"
	uiViewTunnels uiView = "tunnels"
	uiViewWebTTY  uiView = "webtty"
)

type uiDetailMode string

const (
	uiDetailModeSummary uiDetailMode = "summary"
	uiDetailModeJSON    uiDetailMode = "json"
)

type uiState struct {
	View     uiView
	ClientID string
	TunnelID string
	Message  string
	Detail   uiDetailMode
}

type uiInitialState struct {
	SnapshotAt time.Time                  `json:"snapshot_at"`
	Clients    []rstream.ClientProperties `json:"clients"`
	Tunnels    []rstream.TunnelInventory  `json:"tunnels"`
}

type uiSnapshot struct {
	Connected bool
	LastError string
	Clients   []rstream.ClientProperties
	Tunnels   []rstream.TunnelInventory
	WebTTY    []webtty.ServerInfo
}

type uiStore struct {
	mu        sync.RWMutex
	transport string
	updates   chan struct{}
	connected bool
	lastError string
	clients   map[string]rstream.ClientProperties
	tunnels   map[string]rstream.TunnelInventory
}

func newUIStore(transport string) *uiStore {
	return &uiStore{
		transport: transport,
		updates:   make(chan struct{}, 1),
		clients:   make(map[string]rstream.ClientProperties),
		tunnels:   make(map[string]rstream.TunnelInventory),
	}
}

func (s *uiStore) Changes() <-chan struct{} { return s.updates }

func (s *uiStore) run(ctx context.Context, client *rstream.Client) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		s.setConnectionState(false, "")
		connectedOnce := false
		err := client.Watch(ctx, s.transport, nil, func(event rstream.Event) error {
			if err := s.applyEvent(event); err != nil {
				return err
			}
			connectedOnce = true
			s.setConnectionState(true, "")
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil && err != context.Canceled {
			s.setConnectionState(false, err.Error())
		} else {
			s.setConnectionState(false, "")
		}
		if connectedOnce {
			backoff = time.Second
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func (s *uiStore) snapshot() uiSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := uiSnapshot{
		Connected: s.connected,
		LastError: s.lastError,
		Clients:   make([]rstream.ClientProperties, 0, len(s.clients)),
		Tunnels:   make([]rstream.TunnelInventory, 0, len(s.tunnels)),
	}
	for _, client := range s.clients {
		out.Clients = append(out.Clients, client)
	}
	for _, tunnel := range s.tunnels {
		out.Tunnels = append(out.Tunnels, tunnel)
	}
	sort.SliceStable(out.Clients, func(i, j int) bool {
		leftStatus := uiStatusRank(out.Clients[i].Status)
		rightStatus := uiStatusRank(out.Clients[j].Status)
		if leftStatus != rightStatus {
			return leftStatus < rightStatus
		}
		return out.Clients[i].ID < out.Clients[j].ID
	})
	sort.SliceStable(out.Tunnels, func(i, j int) bool {
		leftStatus := uiStatusRank(out.Tunnels[i].Status)
		rightStatus := uiStatusRank(out.Tunnels[j].Status)
		if leftStatus != rightStatus {
			return leftStatus < rightStatus
		}
		leftName := trimOptionalString(out.Tunnels[i].Name)
		rightName := trimOptionalString(out.Tunnels[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return trimOptionalString(out.Tunnels[i].ID) < trimOptionalString(out.Tunnels[j].ID)
	})
	out.WebTTY = webtty.ParseServers(out.Tunnels)
	sortWebTTYServers(out.WebTTY)
	return out
}

func (s *uiStore) setConnectionState(connected bool, lastError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
	s.lastError = strings.TrimSpace(lastError)
	s.notify()
}

func (s *uiStore) applyEvent(event rstream.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch event.Type {
	case "state.initial":
		var initial uiInitialState
		if err := json.Unmarshal(event.Object, &initial); err != nil {
			return fmt.Errorf("decode state.initial: %w", err)
		}
		s.clients = make(map[string]rstream.ClientProperties, len(initial.Clients))
		for _, client := range initial.Clients {
			s.clients[client.ID] = client
		}
		s.tunnels = make(map[string]rstream.TunnelInventory, len(initial.Tunnels))
		for _, tunnel := range initial.Tunnels {
			if tunnel.ID != nil {
				s.tunnels[*tunnel.ID] = tunnel
			}
		}
	case "client.created", "client.updated":
		var client rstream.ClientProperties
		if err := json.Unmarshal(event.Object, &client); err != nil {
			return fmt.Errorf("decode %s: %w", event.Type, err)
		}
		s.clients[client.ID] = client
	case "client.deleted":
		var client rstream.ClientProperties
		if err := json.Unmarshal(event.Object, &client); err != nil {
			return fmt.Errorf("decode %s: %w", event.Type, err)
		}
		delete(s.clients, client.ID)
	case "tunnel.created", "tunnel.updated":
		var tunnel rstream.TunnelInventory
		if err := json.Unmarshal(event.Object, &tunnel); err != nil {
			return fmt.Errorf("decode %s: %w", event.Type, err)
		}
		if tunnel.ID != nil {
			s.tunnels[*tunnel.ID] = tunnel
		}
	case "tunnel.deleted":
		var tunnel rstream.TunnelInventory
		if err := json.Unmarshal(event.Object, &tunnel); err != nil {
			return fmt.Errorf("decode %s: %w", event.Type, err)
		}
		if tunnel.ID != nil {
			delete(s.tunnels, *tunnel.ID)
		}
	}
	s.notify()
	return nil
}

func (s *uiStore) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

func trimOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func uiStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "online":
		return 0
	case "connecting":
		return 1
	case "offline":
		return 2
	default:
		return 3
	}
}

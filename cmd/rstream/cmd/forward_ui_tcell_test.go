// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rstreamlabs/rstream-go"
)

func TestForwardUITCellStatusOutput(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen Init() error = %v", err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(80, 24)
	ui := &forwardUITCell{screen: screen}
	status := newForwardStatus(&rstream.ServerDetails{
		Version:  rstream.StringPtr("2.3.4"),
		Channel:  rstream.StringPtr("preview"),
		Update:   rstream.StringPtr("available"),
		Plan:     rstream.StringPtr("pro"),
		Provider: rstream.StringPtr("aws"),
		Region:   rstream.StringPtr("eu-west-1"),
	})
	status.Status = rstream.StringPtr("online")
	status.TunnelID = rstream.StringPtr("tun-1")
	status.Forwarding = rstream.StringPtr("https://demo.example.com")
	status.Forwarded = rstream.StringPtr("localhost:8080")
	ui.SetStatus(status)
	ui.draw()
	screen.Show()
	cells, width, height := screen.GetContents()
	var rows []string
	for y := 0; y < height; y++ {
		var row strings.Builder
		for x := 0; x < width; x++ {
			row.WriteString(string(cells[y*width+x].Runes))
		}
		rows = append(rows, strings.TrimSpace(row.String()))
	}
	got := strings.Join(rows, "\n")
	wantStatus := fmt.Sprintf("version     : %s\n"+
		"update      : available\n"+
		"status      : online\n"+
		"plan        : pro\n"+
		"provider    : aws\n"+
		"region      : eu-west-1\n"+
		"tunnel ID   : tun-1\n"+
		"forwarding  : https://demo.example.com\n"+
		"forwarded   : localhost:8080\n\n"+
		"incoming connections:\n\nno connection", formatVersion(rstream.Version, rstream.Channel))
	if !strings.Contains(got, wantStatus) {
		t.Fatalf("screen output =\n%s\nwant status:\n%s", got, wantStatus)
	}
	for _, label := range []string{"client version", "server version", "transport configured", "transport selected"} {
		if strings.Contains(got, label) {
			t.Errorf("screen output contains diagnostic label %q", label)
		}
	}
}

func TestForwardUITCellFinalizesScreenOnce(t *testing.T) {
	tests := []struct {
		name string
		exit func(t *testing.T, ui *forwardUITCell, screen tcell.Screen, cancel context.CancelFunc)
	}{
		{
			name: "stop",
			exit: func(t *testing.T, ui *forwardUITCell, _ tcell.Screen, _ context.CancelFunc) {
				t.Helper()
				if err := ui.Stop(); err != nil {
					t.Fatalf("Stop() error = %v", err)
				}
			},
		},
		{
			name: "context canceled",
			exit: func(_ *testing.T, _ *forwardUITCell, _ tcell.Screen, cancel context.CancelFunc) {
				cancel()
			},
		},
		{
			name: "q key",
			exit: func(t *testing.T, _ *forwardUITCell, screen tcell.Screen, _ context.CancelFunc) {
				t.Helper()
				if err := screen.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)); err != nil {
					t.Fatalf("PostEvent(q) error = %v", err)
				}
			},
		},
		{
			name: "control-c key",
			exit: func(t *testing.T, _ *forwardUITCell, screen tcell.Screen, _ context.CancelFunc) {
				t.Helper()
				if err := screen.PostEvent(tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)); err != nil {
					t.Fatalf("PostEvent(Ctrl-C) error = %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			screen := newCountingForwardScreen(t)
			ui := &forwardUITCell{
				screen: screen,
				stop:   make(chan struct{}),
				done:   make(chan struct{}),
				conns:  make([]forwardConnInfo, 0),
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := ui.Start(ctx)

			tt.exit(t, ui, screen, cancel)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("UI did not stop")
			}
			if err := ui.Stop(); err != nil {
				t.Fatalf("second Stop() error = %v", err)
			}
			if got := screen.finiCalls.Load(); got != 1 {
				t.Fatalf("screen Fini() calls = %d, want 1", got)
			}
		})
	}
}

func TestForwardUITCellStopBeforeStartDoesNotBlock(t *testing.T) {
	screen := newCountingForwardScreen(t)
	ui := &forwardUITCell{screen: screen, stop: make(chan struct{}), done: make(chan struct{}), conns: make([]forwardConnInfo, 0)}
	stopDone := make(chan error, 1)
	go func() { stopDone <- ui.Stop() }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() blocked before Start()")
	}
	done := ui.Start(t.Context())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start() did not observe the prior stop")
	}
	if got := screen.finiCalls.Load(); got != 1 {
		t.Fatalf("screen Fini() calls = %d, want 1", got)
	}
}

type countingForwardScreen struct {
	tcell.Screen
	finiCalls atomic.Int32
}

func newCountingForwardScreen(t *testing.T) *countingForwardScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen Init() error = %v", err)
	}
	return &countingForwardScreen{Screen: screen}
}

func (s *countingForwardScreen) Fini() {
	s.finiCalls.Add(1)
	s.Screen.Fini()
}

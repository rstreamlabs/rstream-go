// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

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

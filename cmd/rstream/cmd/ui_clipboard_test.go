// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"testing"
	"time"
)

func TestUIClipboardBoundsWorkAndJoinsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan string, 1)
	clipboard := &uiClipboard{ctx: ctx, command: uiClipboardCommand{Name: "test"}, enabled: true, queue: make(chan string, 1)}
	clipboard.runCommand = func(ctx context.Context, _ uiClipboardCommand, text string) {
		started <- text
		<-ctx.Done()
	}
	done := make(chan struct{})
	go func() {
		clipboard.run()
		close(done)
	}()
	if !clipboard.Copy("first") {
		t.Fatal("first copy was rejected")
	}
	select {
	case text := <-started:
		if text != "first" {
			t.Fatalf("started copy = %q, want first", text)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard worker did not start")
	}
	if !clipboard.Copy("second") {
		t.Fatal("buffered copy was rejected")
	}
	if clipboard.Copy("third") {
		t.Fatal("clipboard accepted work beyond its bound")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("clipboard worker was not joined after cancellation")
	}
}

//go:build !windows

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func TestReadPasswordContextHandlesControlCImmediatelyAndRestoresTerminal(t *testing.T) {
	primary, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	defer terminal.Close()
	before, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readPasswordContext(t.Context(), terminal)
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		current, stateErr := term.GetState(int(terminal.Fd()))
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		if !reflect.DeepEqual(current, before) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("password reader did not enter raw mode")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := primary.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read password error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Control-C did not interrupt the password prompt")
	}
	after, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("terminal state was not restored")
	}
}

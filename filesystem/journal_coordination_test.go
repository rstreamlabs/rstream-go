// See LICENSE file in the project root for license information.

//go:build !windows

package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestExternalJournalMutationCoordination(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			root, state := t.TempDir(), t.TempDir()
			initial, err := Open(root, Policy{StateDir: state})
			if err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			ownerPath := filepath.Join(state, stateDirectory, journalOwnerName)
			data, err := os.ReadFile(ownerPath)
			if err != nil {
				t.Fatal(err)
			}
			var owner journalOwner
			if err := json.Unmarshal(data, &owner); err != nil || owner.Version != 2 {
				t.Fatalf("new journal must reject older lock implementations: %+v %v", owner, err)
			}
			owner.Version = version
			data, err = json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ownerPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(root, alias); err != nil {
				t.Fatal(err)
			}
			first, _ := testLocal(t, root, Policy{StateDir: state})
			second, _ := testLocal(t, alias, Policy{StateDir: state})
			legacy, _ := testLocal(t, root, Policy{})
			release, err := first.lockMutation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if unlock, err := second.lockMutation(ctx); unlock != nil || !errors.Is(err, context.DeadlineExceeded) {
				if unlock != nil {
					unlock()
				}
				t.Fatalf("alias bypassed another owner's mutation: %v", err)
			}
			ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			unlock, err := legacy.lockMutation(ctx)
			if unlock != nil {
				defer unlock()
			}
			if version == 1 && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("existing journal lost coordination with older processes: %v", err)
			}
			if version == 2 && err != nil {
				t.Fatalf("new private journal still locks the export root: %v", err)
			}
			stored, err := os.ReadFile(ownerPath)
			if err != nil || string(stored) != string(data) {
				t.Fatalf("opening a journal silently migrated its coordination: %v", err)
			}
		})
	}
}

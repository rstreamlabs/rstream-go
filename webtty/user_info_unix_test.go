// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"os/user"
	"testing"
)

func TestLookupNumericUserIDs(t *testing.T) {
	t.Run("numeric ids are parsed", func(t *testing.T) {
		uid, gid, err := lookupNumericUserIDs(&user.User{Uid: "1000", Gid: "1001"})
		if err != nil {
			t.Fatalf("lookupNumericUserIDs returned error: %v", err)
		}
		if uid != 1000 || gid != 1001 {
			t.Fatalf("unexpected numeric ids: uid=%d gid=%d", uid, gid)
		}
	})

	t.Run("invalid uid returns error", func(t *testing.T) {
		_, _, err := lookupNumericUserIDs(&user.User{Uid: "S-1-5-21-1", Gid: "1001"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

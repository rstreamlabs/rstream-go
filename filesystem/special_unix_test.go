//go:build unix

// See LICENSE file in the project root for license information.

package filesystem

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSpecialFilesAndHiddenAliases(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".env", "secret")
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".env", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	local, _ := testLocal(t, root, Policy{ReadOnly: true, HideHidden: true})
	for _, name := range []string{"pipe", "alias"} {
		if file, err := local.OpenFile(t.Context(), name, os.O_RDONLY, 0); err == nil {
			_ = file.Close()
			t.Fatalf("opened forbidden path %s", name)
		}
	}
	if err := os.Symlink(".", filepath.Join(root, "cycle")); err != nil {
		t.Fatal(err)
	}
	if err := local.WriteZIP(t.Context(), io.Discard, "/"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("archive cycle: %v", err)
	}
}

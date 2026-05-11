// See LICENSE file in the project root for license information.

package runapply

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestShouldReloadEvent(t *testing.T) {
	cases := []struct {
		name  string
		event fsnotify.Event
		path  string
		want  bool
	}{
		{
			name:  "write event",
			event: fsnotify.Event{Name: "/tmp/config.yaml", Op: fsnotify.Write},
			path:  "/tmp/config.yaml",
			want:  true,
		},
		{
			name:  "rename event",
			event: fsnotify.Event{Name: "/tmp/config.yaml", Op: fsnotify.Rename},
			path:  "/tmp/config.yaml",
			want:  true,
		},
		{
			name:  "different path",
			event: fsnotify.Event{Name: "/tmp/other.yaml", Op: fsnotify.Write},
			path:  "/tmp/config.yaml",
			want:  false,
		},
		{
			name:  "irrelevant op",
			event: fsnotify.Event{Name: "/tmp/config.yaml", Op: fsnotify.Chmod},
			path:  "/tmp/config.yaml",
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReloadEvent(tc.event, tc.path); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestWatchTargetsIncludesSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	linkDir := filepath.Join(dir, "link")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatalf("mkdir link: %v", err)
	}
	target := filepath.Join(targetDir, "config.yaml")
	link := filepath.Join(linkDir, "config.yaml")
	if err := os.WriteFile(target, []byte("tunnels: []\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	targets := watchTargets(link)
	if len(targets) != 2 || !pathsMatch(targets[0], link) || !pathsMatch(targets[1], resolvedTarget) {
		t.Fatalf("watchTargets() = %#v, want link and target", targets)
	}
	dirs := watchDirs(targets)
	if len(dirs) != 2 || !pathsMatch(dirs[0], filepath.Dir(link)) || !pathsMatch(dirs[1], filepath.Dir(resolvedTarget)) {
		t.Fatalf("watchDirs() = %#v, want link and target dirs", dirs)
	}
	if !shouldReloadEvent(fsnotify.Event{Name: resolvedTarget, Op: fsnotify.Write}, targets...) {
		t.Fatalf("target write should trigger reload")
	}
}

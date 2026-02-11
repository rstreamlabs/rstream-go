// See LICENSE file in the project root for license information.

package runapply

import (
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

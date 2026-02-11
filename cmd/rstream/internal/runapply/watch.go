// See LICENSE file in the project root for license information.

package runapply

import (
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func parentDir(path string) string {
	if path == "" {
		return "."
	}
	return filepath.Dir(path)
}

func shouldReloadEvent(event fsnotify.Event, targetPath string) bool {
	if targetPath == "" {
		return false
	}
	if !pathsMatch(event.Name, targetPath) {
		return false
	}
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	return true
}

func pathsMatch(a, b string) bool {
	if a == b {
		return true
	}
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	if cleanA == cleanB {
		return true
	}
	return strings.EqualFold(cleanA, cleanB)
}

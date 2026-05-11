// See LICENSE file in the project root for license information.

package runapply

import (
	"os"
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

func shouldReloadEvent(event fsnotify.Event, targetPaths ...string) bool {
	if len(targetPaths) == 0 {
		return false
	}
	matched := false
	for _, targetPath := range targetPaths {
		if targetPath != "" && pathsMatch(event.Name, targetPath) {
			matched = true
			break
		}
	}
	if !matched {
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

func watchTargets(path string) []string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return []string{path}
	}
	targets := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && !pathsMatch(resolved, path) {
		targets = append(targets, resolved)
	}
	return targets
}

func watchDirs(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		dir := parentDir(path)
		if dir == "" {
			dir = "."
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	if len(out) == 0 {
		out = append(out, parentDir(filepath.Clean(strings.TrimSpace(paths[0]))))
	}
	return out
}

// See LICENSE file in the project root for license information.

// Package filesystem serves rooted filesystems independently of WebTTY.
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/webdav"
)

var ErrListingLimit = errors.New("directory exceeds the listing limit")

type Policy struct {
	ReadOnly   bool
	HideHidden bool
	Exclude    []string
	MaxEntries int
	AllowFile  bool
}

// Local owns a directory handle. Close it after all users have stopped.
type Local struct {
	root   *os.Root
	path   string
	file   string
	policy Policy
}

func Open(name string, policy Policy) (*Local, error) {
	if name == "" {
		return nil, fmt.Errorf("filesystem root is required")
	}
	for _, pattern := range policy.Exclude {
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("invalid exclusion %q: %w", pattern, err)
		}
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve filesystem root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	file := ""
	if !info.IsDir() {
		if !policy.AllowFile || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("filesystem root must be a directory or an allowed regular file")
		}
		file = filepath.Base(resolved)
		resolved = filepath.Dir(resolved)
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, err
	}
	policy.Exclude = append([]string(nil), policy.Exclude...)
	local := &Local{root: root, path: resolved, file: file, policy: policy}
	if file != "" && !local.allowed(file) {
		_ = root.Close()
		return nil, fmt.Errorf("selected file matches an exclusion")
	}
	return local, nil
}

func (f *Local) Close() error { return f.root.Close() }

func (f *Local) Name() string {
	if f.file != "" {
		return f.file
	}
	return filepath.Base(f.path)
}

func (f *Local) IsFile() bool { return f.file != "" }

func (f *Local) allowed(name string) bool {
	if f.file != "" {
		if name == "." {
			return true
		}
		if name != f.file {
			return false
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if f.file == "" && f.policy.HideHidden && part != "." && strings.HasPrefix(part, ".") {
			return false
		}
	}
	for _, pattern := range f.policy.Exclude {
		for current := filepath.ToSlash(name); current != "."; current = path.Dir(current) {
			candidate := current
			if !strings.Contains(pattern, "/") {
				candidate = path.Base(current)
			}
			if matched, _ := path.Match(strings.ToLower(pattern), strings.ToLower(candidate)); matched {
				return false
			}
		}
	}
	return true
}

func (f *Local) checkDirectoryLimit(ctx context.Context, name string) error {
	if f.policy.MaxEntries <= 0 || f.IsFile() {
		return nil
	}
	file, err := f.OpenFile(ctx, name, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return err
	}
	entries, err := file.(*localFile).File.Readdirnames(f.policy.MaxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > f.policy.MaxEntries {
		return ErrListingLimit
	}
	return ctx.Err()
}

func (f *Local) lexical(name string) (string, error) {
	if strings.ContainsAny(name, "\\\x00") {
		return "", os.ErrNotExist
	}
	relative := strings.TrimPrefix(name, "/")
	if relative == "" {
		relative = "."
	}
	if !filepath.IsLocal(filepath.FromSlash(relative)) {
		return "", os.ErrPermission
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			return "", os.ErrPermission
		}
	}
	relative = filepath.Clean(filepath.FromSlash(relative))
	if !f.allowed(relative) {
		return "", os.ErrNotExist
	}
	return relative, nil
}

// Resolve absolute in-root symlinks for compatibility, then perform the actual
// operation through os.Root so a concurrent symlink replacement cannot escape.
func (f *Local) resolve(name string, parent bool) (string, error) {
	relative, err := f.lexical(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(f.path, relative)
	lookup := target
	if parent {
		lookup = filepath.Dir(target)
	}
	resolved, err := filepath.EvalSymlinks(lookup)
	if err != nil {
		return "", err
	}
	if parent {
		resolved = filepath.Join(resolved, filepath.Base(target))
	}
	result, err := filepath.Rel(f.path, resolved)
	if err != nil || !filepath.IsLocal(result) {
		return "", os.ErrPermission
	}
	if !f.allowed(result) {
		return "", os.ErrNotExist
	}
	return result, nil
}

func (f *Local) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.policy.ReadOnly && flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		return nil, os.ErrPermission
	}
	relative, err := f.resolve(name, false)
	if errors.Is(err, os.ErrNotExist) && flag&os.O_CREATE != 0 {
		relative, err = f.resolve(name, true)
	}
	if err != nil {
		return nil, err
	}
	file, err := f.root.OpenFile(relative, flag|nonblockFlag, perm)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, os.ErrPermission
	}
	return &localFile{File: file, local: f, name: relative, ctx: ctx}, nil
}

func (f *Local) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	relative, err := f.resolve(name, false)
	if err != nil {
		return nil, err
	}
	info, err := f.root.Stat(relative)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, os.ErrPermission
	}
	return info, nil
}

func (f *Local) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	if err := f.writeAllowed(ctx); err != nil {
		return err
	}
	relative, err := f.resolve(name, true)
	if err != nil {
		return err
	}
	return f.root.Mkdir(relative, perm)
}

func (f *Local) RemoveAll(ctx context.Context, name string) error {
	if err := f.writeAllowed(ctx); err != nil {
		return err
	}
	relative, err := f.resolve(name, true)
	if err != nil {
		return err
	}
	if relative == "." {
		return os.ErrPermission
	}
	return f.root.RemoveAll(relative)
}

func (f *Local) Rename(ctx context.Context, oldName, newName string) error {
	if err := f.writeAllowed(ctx); err != nil {
		return err
	}
	oldPath, err := f.resolve(oldName, true)
	if err != nil {
		return err
	}
	newPath, err := f.resolve(newName, true)
	if err != nil {
		return err
	}
	if oldPath == "." || newPath == "." {
		return os.ErrPermission
	}
	return f.root.Rename(oldPath, newPath)
}

func (f *Local) writeAllowed(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.policy.ReadOnly {
		return os.ErrPermission
	}
	return nil
}

type localFile struct {
	*os.File
	local *Local
	name  string
	ctx   context.Context
	seen  int
}

func (f *localFile) Readdir(count int) ([]os.FileInfo, error) {
	if f.local.file != "" && f.name == "." {
		if f.seen > 0 {
			if count > 0 {
				return nil, io.EOF
			}
			return nil, nil
		}
		f.seen++
		info, err := f.local.Stat(f.ctx, f.local.file)
		if err != nil {
			return nil, err
		}
		return []os.FileInfo{info}, nil
	}
	var result []os.FileInfo
	for count <= 0 || len(result) < count {
		if err := f.ctx.Err(); err != nil {
			return nil, err
		}
		items, err := f.File.Readdir(1)
		if errors.Is(err, io.EOF) {
			if count > 0 && len(result) == 0 {
				return nil, io.EOF
			}
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		f.seen++
		if f.local.policy.MaxEntries > 0 && f.seen > f.local.policy.MaxEntries {
			return nil, ErrListingLimit
		}
		name := filepath.Join(f.name, items[0].Name())
		if !f.local.allowed(name) {
			continue
		}
		info, err := f.local.Stat(f.ctx, filepath.ToSlash(name))
		if err != nil {
			if f.ctx.Err() != nil {
				return nil, f.ctx.Err()
			}
			continue
		}
		result = append(result, namedInfo{FileInfo: info, name: items[0].Name()})
	}
	return result, nil
}

type namedInfo struct {
	os.FileInfo
	name string
}

func (i namedInfo) Name() string { return i.name }

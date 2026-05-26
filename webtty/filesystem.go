// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/webdav"
)

type FileSystemConfig struct {
	Root          string
	ReadOnly      bool
	MaxUploadSize *int64
	Logger        *slog.Logger
}

func NewFileSystemHandler(cfg *FileSystemConfig) (http.Handler, error) {
	resolved, err := resolveFileSystemConfig(cfg)
	if err != nil {
		return nil, err
	}
	logger := resolved.Logger.With("component", "webtty.filesystem")
	fs, err := newSafeWebDAVFileSystem(resolved.Root)
	if err != nil {
		return nil, err
	}
	handler := http.Handler(&webdav.Handler{
		Prefix:     WebTTYDefaultFSPath,
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				logger.Warn("webdav request failed", "method", r.Method, "path", r.URL.Path, "error", err)
			}
		},
	})
	if resolved.MaxUploadSize != nil && *resolved.MaxUploadSize > 0 {
		handler = maxUploadSizeHandler(handler, *resolved.MaxUploadSize)
	}
	if resolved.ReadOnly {
		handler = readOnlyFileSystemHandler(handler)
	}
	return handler, nil
}

type safeWebDAVFileSystem struct {
	root string
}

func newSafeWebDAVFileSystem(root string) (*safeWebDAVFileSystem, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve filesystem root symlinks: %w", err)
	}
	return &safeWebDAVFileSystem{root: filepath.Clean(resolved)}, nil
}

func (fs *safeWebDAVFileSystem) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	target, err := fs.safeCreatePath(name)
	if err != nil {
		return err
	}
	return os.Mkdir(target, perm)
}

func (fs *safeWebDAVFileSystem) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	target, err := fs.safeExistingPath(name)
	if err != nil {
		if flag&os.O_CREATE == 0 || !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		target, err = fs.safeCreatePath(name)
		if err != nil {
			return nil, err
		}
	}
	return os.OpenFile(target, flag, perm)
}

func (fs *safeWebDAVFileSystem) RemoveAll(_ context.Context, name string) error {
	target, err := fs.safeChildPath(name)
	if err != nil {
		return err
	}
	if target == fs.root {
		return os.ErrInvalid
	}
	return os.RemoveAll(target)
}

func (fs *safeWebDAVFileSystem) Rename(_ context.Context, oldName string, newName string) error {
	oldPath, err := fs.safeChildPath(oldName)
	if err != nil {
		return err
	}
	newPath, err := fs.safeChildPath(newName)
	if err != nil {
		return err
	}
	if oldPath == fs.root || newPath == fs.root {
		return os.ErrInvalid
	}
	return os.Rename(oldPath, newPath)
}

func (fs *safeWebDAVFileSystem) Stat(_ context.Context, name string) (os.FileInfo, error) {
	target, err := fs.safeExistingPath(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(target)
}

func (fs *safeWebDAVFileSystem) safeExistingPath(name string) (string, error) {
	target, err := fs.lexicalPath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !pathInsideRoot(fs.root, resolved) {
		return "", os.ErrPermission
	}
	return resolved, nil
}

func (fs *safeWebDAVFileSystem) safeCreatePath(name string) (string, error) {
	target, err := fs.lexicalPath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err == nil {
		return "", os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	if !pathInsideRoot(fs.root, parent) {
		return "", os.ErrPermission
	}
	return filepath.Join(parent, filepath.Base(target)), nil
}

func (fs *safeWebDAVFileSystem) safeChildPath(name string) (string, error) {
	target, err := fs.lexicalPath(name)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	if !pathInsideRoot(fs.root, parent) {
		return "", os.ErrPermission
	}
	return filepath.Join(parent, filepath.Base(target)), nil
}

func (fs *safeWebDAVFileSystem) lexicalPath(name string) (string, error) {
	if filepath.Separator != '/' && strings.ContainsRune(name, filepath.Separator) || strings.Contains(name, "\x00") {
		return "", os.ErrNotExist
	}
	cleaned := path.Clean("/" + name)
	relative := strings.TrimPrefix(cleaned, "/")
	return filepath.Join(fs.root, filepath.FromSlash(relative)), nil
}

func pathInsideRoot(root string, target string) bool {
	relative, err := filepath.Rel(root, filepath.Clean(target))
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func resolveFileSystemConfig(cfg *FileSystemConfig) (*FileSystemConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("filesystem config is required")
	}
	root := cfg.Root
	if root == "" {
		return nil, fmt.Errorf("filesystem root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve filesystem root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect filesystem root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("filesystem root must be a directory")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	cfg.Root = abs
	return cfg, nil
}

func maxUploadSizeHandler(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func readOnlyFileSystemHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if webDAVMethodWrites(r.Method) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func webDAVMethodWrites(method string) bool {
	switch method {
	case http.MethodDelete, http.MethodPatch, http.MethodPost, http.MethodPut, "COPY", "LOCK", "MKCOL", "MOVE", "PROPPATCH", "UNLOCK":
		return true
	default:
		return false
	}
}

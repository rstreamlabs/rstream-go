// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

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
	handler := http.Handler(&webdav.Handler{
		Prefix:     WebTTYDefaultFSPath,
		FileSystem: webdav.Dir(resolved.Root),
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

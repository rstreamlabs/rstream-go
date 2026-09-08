// See LICENSE file in the project root for license information.

package filesystem

import (
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"

	"golang.org/x/net/webdav"
)

type WebDAVConfig struct {
	Prefix        string
	ReadOnly      bool
	MaxUploadSize int64
	Download      bool
	BoundedDepth  bool
	Logger        *slog.Logger
}

func NewWebDAV(local *Local, cfg WebDAVConfig) (http.Handler, error) {
	if local == nil {
		return nil, fmt.Errorf("filesystem is required")
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "/fs"
	}
	if !strings.HasPrefix(cfg.Prefix, "/") || path.Clean(cfg.Prefix) != cfg.Prefix {
		return nil, fmt.Errorf("invalid WebDAV prefix %q", cfg.Prefix)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	dav := &webdav.Handler{
		Prefix: cfg.Prefix, FileSystem: local, LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				cfg.Logger.Debug("filesystem request failed", "method", r.Method, "path", r.URL.Path, "error", err)
			}
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-cache")
		if cfg.ReadOnly && !readMethod(r.Method) {
			http.Error(w, "Filesystem is read-only", http.StatusForbidden)
			return
		}
		if cfg.ReadOnly && r.Method == http.MethodOptions {
			w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND")
			w.Header().Set("DAV", "1")
			w.WriteHeader(http.StatusOK)
			return
		}
		if cfg.BoundedDepth && r.Method == "PROPFIND" {
			if depth := r.Header.Get("Depth"); depth != "0" && depth != "1" {
				w.Header().Set("Content-Type", "application/xml; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("<error xmlns=\"DAV:\"><propfind-finite-depth/></error>"))
				return
			}
			if r.Header.Get("Depth") == "1" {
				if err := local.checkDirectoryLimit(r.Context(), strings.TrimPrefix(r.URL.Path, cfg.Prefix)); errors.Is(err, ErrListingLimit) {
					http.Error(w, fmt.Sprintf("Directory exceeds the %d-entry listing limit. Share a smaller directory.", local.policy.MaxEntries), http.StatusInsufficientStorage)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		}
		if cfg.MaxUploadSize > 0 {
			if r.ContentLength > cfg.MaxUploadSize {
				http.Error(w, "Upload exceeds the size limit", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxUploadSize)
		}
		if cfg.Download && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(r.URL.Path)}))
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		}
		dav.ServeHTTP(w, r)
	}), nil
}

func readMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == "PROPFIND"
}

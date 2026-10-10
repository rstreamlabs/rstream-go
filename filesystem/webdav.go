// See LICENSE file in the project root for license information.

package filesystem

import (
	"errors"
	"fmt"
	"io"
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
		if r.Method == http.MethodPut && r.Header.Get("Content-Range") != "" {
			http.Error(w, "Partial uploads are not supported; retry the complete PUT", http.StatusBadRequest)
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
			limited := &uploadLimit{ResponseWriter: w, body: http.MaxBytesReader(w, r.Body, cfg.MaxUploadSize)}
			r.Body = limited
			w = limited
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

// WebDAV otherwise turns streaming body-limit errors into HTTP 405.
type uploadLimit struct {
	http.ResponseWriter
	body     io.ReadCloser
	exceeded bool
}

func (l *uploadLimit) Read(p []byte) (int, error) {
	n, err := l.body.Read(p)
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		l.exceeded = true
	}
	return n, err
}

func (l *uploadLimit) Close() error { return l.body.Close() }

func (l *uploadLimit) Unwrap() http.ResponseWriter { return l.ResponseWriter }

func (l *uploadLimit) WriteHeader(status int) {
	if l.exceeded {
		status = http.StatusRequestEntityTooLarge
	}
	l.ResponseWriter.WriteHeader(status)
}

func (l *uploadLimit) Write(p []byte) (int, error) {
	if l.exceeded {
		_, err := io.WriteString(l.ResponseWriter, "Upload exceeds the size limit\n")
		return len(p), err
	}
	return l.ResponseWriter.Write(p)
}

// See LICENSE file in the project root for license information.

// Package fileserver composes a browser UI and a read-only filesystem backend.
package fileserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/rstreamlabs/rstream-go/filesystem"
	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
)

const InfoPath = "/_rstream/files/v1/info"
const ArchivePath = "/_rstream/files/v1/archive"
const FSPath = "/fs"

type Config struct {
	Backend       string
	RTC           rtc.ServerConfig
	Root          string
	IncludeHidden bool
	Exclude       []string
	Username      string
	Password      string
	UI            http.Handler
	Logger        *slog.Logger
}

type Capabilities struct {
	List    bool `json:"list"`
	Read    bool `json:"read"`
	Write   bool `json:"write"`
	Resume  bool `json:"resume"`
	Archive bool `json:"archive"`
	E2EE    bool `json:"e2ee"`
}

type Info struct {
	Version      int          `json:"version"`
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	Backend      string       `json:"backend"`
	FSPath       string       `json:"fs_path"`
	ArchivePath  string       `json:"archive_path,omitempty"`
	Access       string       `json:"access"`
	Capabilities Capabilities `json:"capabilities"`
}

type Server struct {
	backend  *filesystem.BackendHandler
	local    *filesystem.Local
	handler  http.Handler
	logger   *slog.Logger
	info     Info
	access   atomic.Value
	archives chan struct{}
}

func New(cfg Config) (*Server, error) {
	backend, err := filesystem.ResolveBackend(cfg.Backend)
	if err != nil {
		return nil, err
	}
	local, err := filesystem.Open(cfg.Root, filesystem.Policy{ReadOnly: true, HideHidden: !cfg.IncludeHidden, Exclude: cfg.Exclude, MaxEntries: 10000, AllowFile: true})
	if err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{local: local, logger: cfg.Logger, archives: make(chan struct{}, 2)}
	dav, err := filesystem.NewBackend(local, filesystem.BackendConfig{Backend: backend, RTC: cfg.RTC, ArchivePath: ArchivePath, Archive: http.HandlerFunc(s.serveArchive), WebDAV: filesystem.WebDAVConfig{Prefix: FSPath, ReadOnly: true, Download: true, BoundedDepth: true, Logger: cfg.Logger}})
	if err != nil {
		_ = local.Close()
		return nil, err
	}
	s.backend = dav
	s.info = Info{Version: 1, Name: local.Name(), Kind: "directory", Backend: backend, FSPath: FSPath, ArchivePath: ArchivePath, Capabilities: Capabilities{List: true, Read: true, Resume: true, Archive: !local.IsFile()}}
	if local.IsFile() {
		s.info.Kind = "file"
		s.info.ArchivePath = ""
	}
	s.SetAccess("public")
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+InfoPath, s.serveInfo)
	mux.HandleFunc("GET "+ArchivePath, s.serveArchive)
	mux.Handle(FSPath, dav)
	mux.Handle(FSPath+"/", dav)
	if cfg.UI != nil {
		mux.Handle("GET /{$}", cfg.UI)
	}
	s.handler = mux
	if cfg.Password != "" {
		if cfg.Username == "" {
			cfg.Username = "rstream"
		}
		s.handler = basicAuth(mux, cfg.Username, cfg.Password)
		s.SetAccess("password")
	}
	return s, nil
}

func (s *Server) Close() error {
	return errors.Join(s.backend.Close(), s.local.Close())
}

func (s *Server) SetAccess(access string) { s.access.Store(access) }

func (s *Server) Info() Info {
	info := s.info
	info.Access, _ = s.access.Load().(string)
	return info
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	s.handler.ServeHTTP(w, r)
}

func (s *Server) serveInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	if err := json.NewEncoder(w).Encode(s.Info()); err != nil {
		s.logger.Debug("write filesystem metadata", "error", err)
	}
}

func (s *Server) serveArchive(w http.ResponseWriter, r *http.Request) {
	if !s.info.Capabilities.Archive {
		http.NotFound(w, r)
		return
	}
	target := r.URL.Query().Get("path")
	if target == "" {
		target = "/"
	}
	info, err := s.local.Stat(r.Context(), target)
	if err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	select {
	case s.archives <- struct{}{}:
		defer func() { <-s.archives }()
	default:
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Two archives are already downloading. Try again shortly.", http.StatusTooManyRequests)
		return
	}
	name := path.Base(strings.TrimRight(target, "/"))
	if name == "." || name == "/" || name == "" {
		name = s.info.Name
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		return
	}
	if err := s.local.WriteZIP(r.Context(), w, target); err != nil {
		if !errors.Is(err, r.Context().Err()) {
			s.logger.Warn("archive download interrupted", "path", target, "error", err)
		}
		panic(http.ErrAbortHandler)
	}
}

func basicAuth(next http.Handler, username, password string) http.Handler {
	wantUser := sha256.Sum256([]byte(username))
	wantPassword := sha256.Sum256([]byte(password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotUser := sha256.Sum256([]byte(user))
		gotPassword := sha256.Sum256([]byte(pass))
		valid := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) & subtle.ConstantTimeCompare(gotPassword[:], wantPassword[:])
		if !ok || valid != 1 {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"rstream files\", charset=\"UTF-8\"")
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "A username and password are required.", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

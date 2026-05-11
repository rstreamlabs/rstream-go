// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultHeartbeatInterval    time.Duration = 5 * time.Second
	defaultMaxMessageSize       int64         = 1024 * 1024
	defaultReadBufferSize       int           = 1024
	defaultSessionCloseDeadline time.Duration = 5 * time.Second
	defaultSessionOpenDeadline  time.Duration = 5 * time.Second
	defaultWriteBufferSize      int           = 1024
)

type ServerConfig struct {
	MaxMessageSize       *int64
	ReadBufferSize       *int
	WriteBufferSize      *int
	EnvVars              *map[string]string
	SessionOpenDeadline  *time.Duration
	SessionCloseDeadline *time.Duration
	HeartbeatInterval    *time.Duration
	AuthToken            *string
	AllowUnauthenticated *bool
	AllowedOrigins       []string
	Logger               *slog.Logger
}

type Handler struct {
	cfg        *ServerConfig
	upgrader   websocket.Upgrader
	logger     *slog.Logger
	sessionsMu sync.Mutex
	sessions   map[*session]struct{}
	sessionIDs *sessionIDGenerator
	draining   atomic.Bool
}

func NewWebTTYHandler(cfg *ServerConfig) *Handler {
	resolved := resolveServerConfig(cfg)
	upgrader := websocket.Upgrader{
		ReadBufferSize:  *resolved.ReadBufferSize,
		WriteBufferSize: *resolved.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			allowSameHost := resolved.AllowUnauthenticated == nil || !*resolved.AllowUnauthenticated
			return webTTYOriginAllowed(r, resolved.AllowedOrigins, allowSameHost)
		},
	}
	return &Handler{
		cfg:        resolved,
		upgrader:   upgrader,
		logger:     resolved.Logger.With("component", "webtty.server"),
		sessions:   make(map[*session]struct{}),
		sessionIDs: newSessionIDGenerator(),
	}
}

func resolveServerConfig(cfg *ServerConfig) *ServerConfig {
	if cfg == nil {
		cfg = &ServerConfig{}
	}
	if cfg.MaxMessageSize == nil {
		value := defaultMaxMessageSize
		cfg.MaxMessageSize = &value
	}
	if cfg.ReadBufferSize == nil {
		value := defaultReadBufferSize
		cfg.ReadBufferSize = &value
	}
	if cfg.WriteBufferSize == nil {
		value := defaultWriteBufferSize
		cfg.WriteBufferSize = &value
	}
	if cfg.EnvVars == nil {
		value := map[string]string{}
		cfg.EnvVars = &value
	}
	if cfg.SessionOpenDeadline == nil {
		value := defaultSessionOpenDeadline
		cfg.SessionOpenDeadline = &value
	}
	if cfg.SessionCloseDeadline == nil {
		value := defaultSessionCloseDeadline
		cfg.SessionCloseDeadline = &value
	}
	if cfg.HeartbeatInterval == nil {
		value := defaultHeartbeatInterval
		cfg.HeartbeatInterval = &value
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	if !h.authorize(r) {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("failed to upgrade websocket connection", "error", err)
		return
	}
	sessionID := h.sessionIDs.Generate()
	h.logger.Debug("websocket connection accepted", "session_id", sessionID)
	s := newSession(conn, h.cfg, h.logger.With("session_id", sessionID))
	if !h.registerSession(s) {
		h.logger.Info("rejecting websocket connection during shutdown", "session_id", sessionID)
		s.close()
		return
	}
	go func() {
		defer h.unregisterSession(s)
		s.run()
	}()
}

func (h *Handler) registerSession(s *session) bool {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	if h.draining.Load() {
		return false
	}
	h.sessions[s] = struct{}{}
	return true
}

func (h *Handler) unregisterSession(s *session) {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	delete(h.sessions, s)
}

func (h *Handler) snapshotSessions() []*session {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	out := make([]*session, 0, len(h.sessions))
	for s := range h.sessions {
		out = append(out, s)
	}
	return out
}

func (h *Handler) BeginDrain() {
	h.draining.Store(true)
}

func (h *Handler) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.BeginDrain()
	deadline := *h.cfg.SessionCloseDeadline
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}
	sessions := h.snapshotSessions()
	if len(sessions) == 0 {
		return nil
	}
	h.logger.Info("starting graceful session shutdown", "sessions", len(sessions))
	wg := sync.WaitGroup{}
	for _, s := range sessions {
		wg.Add(1)
		go func(session *session) {
			defer wg.Done()
			session.shutdown(ctx)
		}(s)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		h.logger.Info("all webtty sessions are closed")
		return nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			h.logger.Warn("graceful shutdown deadline reached, forced remaining sessions")
		}
		return fmt.Errorf("webtty shutdown failed: %w", ctx.Err())
	}
}

func (h *Handler) authorize(r *http.Request) bool {
	if h.cfg.AllowUnauthenticated != nil && *h.cfg.AllowUnauthenticated {
		return true
	}
	if h.cfg.AuthToken == nil || strings.TrimSpace(*h.cfg.AuthToken) == "" {
		return false
	}
	got := bearerToken(r.Header.Get("Authorization"))
	want := strings.TrimSpace(*h.cfg.AuthToken)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

func webTTYOriginAllowed(r *http.Request, allowed []string, allowSameHost bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	originValue := strings.ToLower(origin)
	originHost := strings.ToLower(u.Host)
	for _, allowedOrigin := range allowed {
		allowedOrigin = strings.ToLower(strings.TrimSpace(allowedOrigin))
		if allowedOrigin == "*" {
			return true
		}
		if allowedOrigin != "" && allowedOrigin == originValue {
			return true
		}
		if allowedOrigin != "" && allowedOrigin == originHost {
			return true
		}
	}
	if !allowSameHost {
		return false
	}
	return originHost == strings.ToLower(r.Host)
}

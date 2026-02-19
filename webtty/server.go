// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultMaxMessageSize       int64         = 1024 * 1024
	defaultReadBufferSize       int           = 1024
	defaultWriteBufferSize      int           = 1024
	defaultSessionCloseDeadline time.Duration = 8 * time.Second
)

type ServerConfig struct {
	MaxMessageSize       *int64
	ReadBufferSize       *int
	WriteBufferSize      *int
	EnvVars              *map[string]string
	SessionCloseDeadline *time.Duration
	Logger               *slog.Logger
}

type Handler struct {
	cfg         *ServerConfig
	upgrader    websocket.Upgrader
	logger      *slog.Logger
	sessionsMu  sync.Mutex
	sessions    map[*session]struct{}
	draining    atomic.Bool
	nextSession uint64
}

func NewWebTTYHandler(cfg *ServerConfig) *Handler {
	resolved := resolveServerConfig(cfg)
	upgrader := websocket.Upgrader{
		ReadBufferSize:  *resolved.ReadBufferSize,
		WriteBufferSize: *resolved.WriteBufferSize,
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}
	return &Handler{
		cfg:      resolved,
		upgrader: upgrader,
		logger:   resolved.Logger.With("component", "webtty.server"),
		sessions: make(map[*session]struct{}),
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
	if cfg.SessionCloseDeadline == nil {
		value := defaultSessionCloseDeadline
		cfg.SessionCloseDeadline = &value
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
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("failed to upgrade websocket connection", "error", err)
		return
	}
	sessionID := atomic.AddUint64(&h.nextSession, 1)
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
		<-done
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			h.logger.Warn("graceful shutdown deadline reached, forced remaining sessions")
		}
		return fmt.Errorf("webtty shutdown failed: %w", ctx.Err())
	}
}

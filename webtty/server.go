// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/webtransport-go"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
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
	MaxMessageSize              *int64
	ReadBufferSize              *int
	WriteBufferSize             *int
	EnvVars                     *map[string]string
	SessionOpenDeadline         *time.Duration
	SessionCloseDeadline        *time.Duration
	HeartbeatInterval           *time.Duration
	AuthToken                   *string
	AllowUnauthenticated        *bool
	AllowedOrigins              []string
	PayloadCrypto               *PayloadCrypto
	PayloadCryptoResolver       PayloadCryptoResolver
	RequireSessionKeyGrant      *bool
	EndpointIdentity            *WebTTYEndpointIdentity
	RequireClientProof          *bool
	AuthorizedClientSigningKeys map[string][]byte
	AuthorizedClientSigningKey  AuthorizedClientSigningKeyResolver
	ClientProofVerifier         ClientProofVerifier
	WorkspaceID                 string
	ProjectID                   string
	ServerID                    string
	ExecutionMode               *ExecutionMode
	DefaultUsername             *string
	AllowClientUser             *bool
	Logger                      *slog.Logger
}

type AuthorizedClientSigningKeyResolver func(ctx context.Context, signingKeyID []byte) ([]byte, error)

type ClientProofVerification struct {
	Proof      *pb.ClientProof
	Credential []byte
	Transcript ClientProofTranscript
}

type ClientProofVerifier func(ctx context.Context, verification ClientProofVerification) ([]byte, error)

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
	if cfg.ExecutionMode == nil {
		value := WebTTYExecutionModeSpawn
		cfg.ExecutionMode = &value
	}
	if cfg.AuthorizedClientSigningKeys == nil {
		cfg.AuthorizedClientSigningKeys = map[string][]byte{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func (h *Handler) Config() *ServerConfig {
	if h == nil {
		return nil
	}
	return h.cfg
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
	s := newSession(conn, h.cfg, h.logger.With("session_id", sessionID, "transport", string(WebTTYTransportWebSocket)), sessionID, WebTTYTransportWebSocket)
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

func (h *Handler) ServeConn(conn net.Conn) {
	if h.draining.Load() {
		_ = conn.Close()
		return
	}
	sessionID := h.sessionIDs.Generate()
	h.logger.Debug("plain connection accepted", "session_id", sessionID)
	s := newSession(newPlainMessageConn(conn), h.cfg, h.logger.With("session_id", sessionID, "transport", string(WebTTYTransportPlain)), sessionID, WebTTYTransportPlain)
	if !h.registerSession(s) {
		h.logger.Info("rejecting plain connection during shutdown", "session_id", sessionID)
		s.close()
		return
	}
	defer h.unregisterSession(s)
	s.run()
}

func (h *Handler) ServeWebTransportSession(ctx context.Context, wtSession *webtransport.Session) {
	if h.draining.Load() {
		_ = wtSession.CloseWithError(0, "server is draining")
		return
	}
	if ctx == nil {
		ctx = wtSession.Context()
	}
	stream, err := wtSession.AcceptStream(ctx)
	if err != nil {
		h.logger.Warn("failed to accept webtransport stream", "error", err)
		_ = wtSession.CloseWithError(0, "stream accept failed")
		return
	}
	sessionID := h.sessionIDs.Generate()
	h.logger.Debug("webtransport session accepted", "session_id", sessionID)
	s := newSession(newWebTransportMessageConn(wtSession, stream), h.cfg, h.logger.With("session_id", sessionID, "transport", string(WebTTYTransportWebTransport)), sessionID, WebTTYTransportWebTransport)
	if !h.registerSession(s) {
		h.logger.Info("rejecting webtransport session during shutdown", "session_id", sessionID)
		s.close()
		return
	}
	defer h.unregisterSession(s)
	s.run()
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
	return authorizeBearerRequest(r, h.cfg.AuthToken, h.cfg.AllowUnauthenticated != nil && *h.cfg.AllowUnauthenticated)
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
	originHost, ok := normalizeWebTTYOriginHost(u.Host, u.Scheme)
	if !ok {
		return false
	}
	for _, allowedOrigin := range allowed {
		allowedOrigin = strings.ToLower(strings.TrimSpace(allowedOrigin))
		if allowedOrigin == "*" {
			return true
		}
		if allowedOrigin != "" && allowedOrigin == originValue {
			return true
		}
		if webTTYAllowedOriginMatches(allowedOrigin, originHost, u.Scheme) {
			return true
		}
	}
	if !allowSameHost {
		return false
	}
	requestHost, ok := normalizeWebTTYOriginHost(r.Host, u.Scheme)
	return ok && originHost == requestHost
}

func webTTYAllowedOriginMatches(allowedOrigin string, originHost string, scheme string) bool {
	if allowedOrigin == "" {
		return false
	}
	parsed, err := url.Parse(allowedOrigin)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		if !strings.EqualFold(parsed.Scheme, scheme) {
			return false
		}
		allowedHost, ok := normalizeWebTTYOriginHost(parsed.Host, parsed.Scheme)
		return ok && allowedHost == originHost
	}
	allowedHost, ok := normalizeWebTTYOriginHost(allowedOrigin, scheme)
	return ok && allowedHost == originHost
}

func normalizeWebTTYOriginHost(host string, scheme string) (string, bool) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", false
	}
	hostname := host
	port := ""
	if strings.Contains(host, ":") {
		splitHost, splitPort, err := net.SplitHostPort(host)
		if err == nil {
			hostname = splitHost
			port = splitPort
		} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			hostname = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		} else {
			return "", false
		}
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return "", false
	}
	hostname = strings.ToLower(hostname)
	if port == "" || port == defaultWebTTYOriginPort(scheme) {
		return hostname, true
	}
	return strings.ToLower(net.JoinHostPort(hostname, port)), true
}

func defaultWebTTYOriginPort(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

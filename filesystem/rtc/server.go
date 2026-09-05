// See LICENSE file in the project root for license information.

package rtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

type ServerConfig struct {
	ICE             ICEProvider
	Handler         http.Handler
	MaxSessions     int
	LeaseDuration   time.Duration
	RestartInterval time.Duration
	RelayOnly       bool
	Logger          *slog.Logger
}

type Server struct {
	config   ServerConfig
	gate     sync.Mutex
	sessions map[string]*session
	closing  bool
	active   sync.WaitGroup
	slots    chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
}

type session struct {
	id          string
	peer        *webrtc.PeerConnection
	ctx         context.Context
	cancel      context.CancelFunc
	deadline    atomic.Int64
	opened      chan *webrtc.DataChannel
	credits     chan struct{}
	finished    chan struct{}
	claimed     atomic.Bool
	negotiating chan struct{}
}

func NewServer(config ServerConfig) *Server {
	if config.MaxSessions <= 0 {
		config.MaxSessions = 16
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 90 * time.Second
	}
	if config.RestartInterval <= 0 {
		config.RestartInterval = 5 * time.Minute
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{config: config, sessions: make(map[string]*session), slots: make(chan struct{}, config.MaxSessions), ctx: ctx, cancel: cancel}
}

func (s *Server) Close() error {
	s.gate.Lock()
	s.closing = true
	s.cancel()
	for _, session := range s.sessions {
		session.cancel()
	}
	s.gate.Unlock()
	s.active.Wait()
	return nil
}

func (s *Server) ActiveSessions() int {
	s.gate.Lock()
	defer s.gate.Unlock()
	return len(s.sessions)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := boundedContext(r.Context())
	defer cancel()
	if r.Method == http.MethodGet {
		servers, err := s.ice(ctx)
		if err != nil {
			http.Error(w, "Unable to obtain rstream STUN/TURN credentials", http.StatusServiceUnavailable)
			s.config.Logger.Warn("filesystem ICE credentials", "error", err)
			return
		}
		s.writeJSON(w, Info{Version: 1, Backend: "webrtc", ICEServers: servers, LeaseSeconds: max(1, int(s.config.LeaseDuration.Seconds())), RestartSeconds: max(1, int(s.config.RestartInterval.Seconds()))})
		return
	}
	if r.Method != http.MethodPost || strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		http.Error(w, "Expected JSON signaling request", http.StatusBadRequest)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
			http.Error(w, "Cross-origin signaling is not allowed", http.StatusForbidden)
			return
		}
	}
	var signal Signal
	if err := Decode(http.MaxBytesReader(w, r.Body, MaxSignal), &signal); err != nil {
		http.Error(w, "Invalid filesystem signaling request", http.StatusBadRequest)
		return
	}
	if signal.Action == "renew" || signal.Action == "close" || signal.Action == "restart" {
		s.gate.Lock()
		session := s.sessions[signal.Session]
		if session != nil && (session.ctx.Err() != nil || time.Now().UnixNano() >= session.deadline.Load()) {
			session = nil
		}
		if session != nil && signal.Action != "close" {
			session.deadline.Store(time.Now().Add(s.config.LeaseDuration).UnixNano())
		}
		s.gate.Unlock()
		if session == nil {
			http.Error(w, "Filesystem session expired", http.StatusGone)
			return
		}
		if signal.Action == "close" {
			session.cancel()
		}
		if signal.Action == "restart" {
			answer, err := s.restart(ctx, session, signal.SDP)
			if err != nil {
				session.cancel()
				http.Error(w, "Filesystem ICE restart failed", http.StatusBadRequest)
				return
			}
			s.writeJSON(w, answer)
			return
		}
		s.writeJSON(w, struct{}{})
		return
	}
	if signal.Action != "offer" || signal.Request == nil || len(signal.SDP) > 64<<10 || len(signal.Request.Body) > 16<<10 {
		http.Error(w, "Invalid filesystem offer", http.StatusBadRequest)
		return
	}
	if !ReadOnly(signal.Request.Method) {
		http.Error(w, "WebRTC filesystem is read-only; writing is not supported", http.StatusForbidden)
		return
	}
	session, err := s.open(ctx, signal)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errCapacity) {
			status = http.StatusTooManyRequests
		}
		http.Error(w, err.Error(), status)
		return
	}
	if !s.writeJSON(w, Answer{Session: session.id, SDP: session.peer.LocalDescription().SDP}) {
		session.cancel()
	}
}

var errCapacity = errors.New("filesystem WebRTC session limit reached or server stopping")

func (s *Server) restart(ctx context.Context, session *session, sdp string) (Answer, error) {
	select {
	case session.negotiating <- struct{}{}:
		defer func() { <-session.negotiating }()
	default:
		return Answer{}, fmt.Errorf("filesystem ICE negotiation already in progress")
	}
	if len(sdp) == 0 || len(sdp) > 64<<10 {
		return Answer{}, fmt.Errorf("invalid ICE restart offer")
	}
	servers, err := s.ice(ctx)
	if err != nil {
		return Answer{}, err
	}
	configuration := session.peer.GetConfiguration()
	configuration.ICEServers = servers
	if err := session.peer.SetConfiguration(configuration); err != nil {
		return Answer{}, err
	}
	if err := session.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		return Answer{}, err
	}
	answer, err := session.peer.CreateAnswer(nil)
	if err != nil {
		return Answer{}, err
	}
	sdp, err = Gather(ctx, session.peer, answer)
	return Answer{Session: session.id, SDP: sdp}, err
}

func (s *Server) ice(ctx context.Context) ([]webrtc.ICEServer, error) {
	if s.config.ICE == nil {
		return nil, nil
	}
	return s.config.ICE(ctx)
}

func (s *Server) open(ctx context.Context, signal Signal) (opened *session, failure error) {
	s.gate.Lock()
	if s.closing || len(s.slots) >= cap(s.slots) {
		s.gate.Unlock()
		return nil, errCapacity
	}
	s.slots <- struct{}{}
	s.active.Add(1)
	s.gate.Unlock()
	owned := false
	defer func() {
		if !owned {
			<-s.slots
			s.active.Done()
		}
	}()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	stopShutdown := context.AfterFunc(s.ctx, stop)
	defer stopShutdown()
	servers, err := s.ice(ctx)
	if err != nil {
		return nil, fmt.Errorf("obtain filesystem ICE configuration: %w", err)
	}
	peer, err := NewPeer(servers, s.config.RelayOnly)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		_ = peer.Close()
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	connection := &session{id: hex.EncodeToString(key), peer: peer, ctx: sessionCtx, cancel: cancel, opened: make(chan *webrtc.DataChannel, 1), credits: make(chan struct{}, Window), finished: make(chan struct{}, 1), negotiating: make(chan struct{}, 1)}
	connection.deadline.Store(time.Now().Add(s.config.LeaseDuration).UnixNano())
	s.gate.Lock()
	if s.closing || len(s.sessions) >= s.config.MaxSessions {
		s.gate.Unlock()
		cancel()
		_ = peer.Close()
		return nil, errCapacity
	}
	s.sessions[connection.id] = connection
	owned = true
	s.gate.Unlock()
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			cancel()
		}
	})
	peer.OnDataChannel(func(channel *webrtc.DataChannel) { connection.channel(channel) })
	go s.run(connection, *signal.Request)
	if err := peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: signal.SDP}); err != nil {
		cancel()
		return nil, err
	}
	answer, err := peer.CreateAnswer(nil)
	if err == nil {
		_, err = Gather(ctx, peer, answer)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return connection, nil
}

func (s *session) channel(channel *webrtc.DataChannel) {
	if channel.Label() != Protocol || !channel.Ordered() || channel.MaxPacketLifeTime() != nil || channel.MaxRetransmits() != nil || !s.claimed.CompareAndSwap(false, true) {
		s.cancel()
		return
	}
	channel.OnClose(s.cancel)
	channel.OnError(func(error) { s.cancel() })
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		if !message.IsString {
			s.cancel()
			return
		}
		switch string(message.Data) {
		case "credit":
			select {
			case s.credits <- struct{}{}:
			default:
				s.cancel()
			}
		case "done":
			select {
			case s.finished <- struct{}{}:
			default:
			}
		default:
			s.cancel()
		}
	})
	for range Window {
		s.credits <- struct{}{}
	}
	channel.OnOpen(func() {
		select {
		case s.opened <- channel:
		case <-s.ctx.Done():
		}
	})
}

func (s *Server) run(session *session, request Request) {
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(min(time.Second, s.config.LeaseDuration/2))
		defer ticker.Stop()
		for {
			select {
			case <-session.ctx.Done():
				return
			case <-ticker.C:
				if time.Now().UnixNano() >= session.deadline.Load() {
					session.cancel()
					return
				}
			}
		}
	}()
	defer func() {
		session.cancel()
		if err := session.peer.Close(); err != nil {
			s.config.Logger.Debug("close filesystem peer", "error", err)
		}
		<-watchDone
		s.gate.Lock()
		delete(s.sessions, session.id)
		s.gate.Unlock()
		<-s.slots
		s.active.Done()
	}()
	select {
	case <-session.ctx.Done():
		return
	case channel := <-session.opened:
		drained := make(chan struct{}, 1)
		channel.SetBufferedAmountLowThreshold(ChunkSize * Window / 2)
		channel.OnBufferedAmountLow(func() {
			select {
			case drained <- struct{}{}:
			default:
			}
		})
		writer := &responseWriter{ctx: session.ctx, channel: channel, credits: session.credits, drained: drained, header: make(http.Header)}
		s.serve(writer, request)
		select {
		case <-session.ctx.Done():
		case <-session.finished:
		}
	}
}

func (s *Server) serve(writer *responseWriter, request Request) {
	defer func() {
		if failure := recover(); failure != nil {
			_ = writer.send(Response{Error: "Filesystem transfer interrupted"})
			s.config.Logger.Debug("filesystem transfer interrupted", "error", failure)
		}
	}()
	req, err := http.NewRequestWithContext(writer.ctx, request.Method, request.URI, strings.NewReader(request.Body))
	if err != nil || req.URL.IsAbs() || req.URL.Host != "" || !strings.HasPrefix(request.URI, "/") {
		http.Error(writer, "Invalid filesystem request path", http.StatusBadRequest)
	} else {
		for name, values := range request.Header {
			for _, value := range values {
				req.Header.Add(name, value)
			}
		}
		s.config.Handler.ServeHTTP(writer, req)
	}
	writer.WriteHeader(http.StatusOK)
	if err := writer.send(Response{Done: true}); err != nil {
		s.config.Logger.Debug("finish filesystem transfer", "error", err)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, value any) bool {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.config.Logger.Debug("write filesystem signaling", "error", err)
		return false
	}
	return true
}

type responseWriter struct {
	ctx     context.Context
	channel *webrtc.DataChannel
	credits <-chan struct{}
	drained <-chan struct{}
	header  http.Header
	wrote   bool
	err     error
}

func (w *responseWriter) Header() http.Header { return w.header }

func (w *responseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.err = w.send(Response{Status: status, Header: w.header})
}

func (w *responseWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(data) > 0 {
		select {
		case <-w.ctx.Done():
			return written, w.ctx.Err()
		case <-w.credits:
		}
		for w.channel.BufferedAmount() > ChunkSize*Window {
			select {
			case <-w.ctx.Done():
				return written, w.ctx.Err()
			case <-w.drained:
			}
		}
		count := min(ChunkSize, len(data))
		if err := w.channel.Send(data[:count]); err != nil {
			return written, err
		}
		written += count
		data = data[count:]
	}
	return written, nil
}

func (w *responseWriter) send(value Response) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return w.channel.SendText(string(data))
}

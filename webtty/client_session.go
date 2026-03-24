// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type SessionConfig struct {
	URL               string
	DialContext       func(context.Context, string, string) (net.Conn, error)
	Interactive       bool
	AllocateTTY       bool
	SendHeartbeat     bool
	EnvVars           []string
	Workdir           *string
	Username          *string
	CmdArgs           []string
	MaxMessageSize    *int64
	ReadBufferSize    *int
	WriteBufferSize   *int
	OpenDeadline      *time.Duration
	CloseDeadline     *time.Duration
	HeartbeatInterval *time.Duration
	Logger            *slog.Logger
}

type ClientSessionStream string

const (
	ClientSessionStdout ClientSessionStream = "stdout"
	ClientSessionStderr ClientSessionStream = "stderr"
)

type ClientSessionEvent struct {
	Stream ClientSessionStream
	Data   []byte
}

type clientSessionResult struct {
	exitCode int
	err      error
}

type ClientSession struct {
	runtime            *clientRuntime
	loopCancel         context.CancelFunc
	doneRead           chan struct{}
	events             chan ClientSessionEvent
	resultCh           chan clientSessionResult
	closeTransportOnce sync.Once
	finalizeOnce       sync.Once
	resultMu           sync.Mutex
	closeResult        *clientSessionResult
}

func (cfg *ClientConfig) sessionConfig() *SessionConfig {
	if cfg == nil {
		return nil
	}
	return &SessionConfig{
		URL:               cfg.URL,
		DialContext:       cfg.DialContext,
		Interactive:       cfg.Interactive,
		AllocateTTY:       cfg.AllocateTTY,
		SendHeartbeat:     cfg.SendHeartbeat,
		EnvVars:           append([]string(nil), cfg.EnvVars...),
		Workdir:           cfg.Workdir,
		Username:          cfg.Username,
		CmdArgs:           append([]string(nil), cfg.CmdArgs...),
		MaxMessageSize:    cfg.MaxMessageSize,
		ReadBufferSize:    cfg.ReadBufferSize,
		WriteBufferSize:   cfg.WriteBufferSize,
		OpenDeadline:      cfg.OpenDeadline,
		CloseDeadline:     cfg.CloseDeadline,
		HeartbeatInterval: cfg.HeartbeatInterval,
		Logger:            cfg.Logger,
	}
}

func (cfg *SessionConfig) clientConfig() *ClientConfig {
	if cfg == nil {
		return nil
	}
	return &ClientConfig{
		URL:               cfg.URL,
		DialContext:       cfg.DialContext,
		Interactive:       cfg.Interactive,
		AllocateTTY:       cfg.AllocateTTY,
		SendHeartbeat:     cfg.SendHeartbeat,
		EnvVars:           append([]string(nil), cfg.EnvVars...),
		Workdir:           cfg.Workdir,
		Username:          cfg.Username,
		CmdArgs:           append([]string(nil), cfg.CmdArgs...),
		MaxMessageSize:    cfg.MaxMessageSize,
		ReadBufferSize:    cfg.ReadBufferSize,
		WriteBufferSize:   cfg.WriteBufferSize,
		OpenDeadline:      cfg.OpenDeadline,
		CloseDeadline:     cfg.CloseDeadline,
		HeartbeatInterval: cfg.HeartbeatInterval,
		Logger:            cfg.Logger,
	}
}

func resolveSessionConfig(cfg *SessionConfig) (*SessionConfig, error) {
	if cfg == nil {
		cfg = &SessionConfig{}
	}
	if cfg.URL == "" {
		cfg.URL = "ws://127.0.0.1:8080"
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
	if cfg.OpenDeadline == nil {
		value := defaultClientOpenDeadline
		cfg.OpenDeadline = &value
	}
	if cfg.CloseDeadline == nil {
		value := defaultClientCloseDeadline
		cfg.CloseDeadline = &value
	}
	if cfg.HeartbeatInterval == nil {
		value := defaultHeartbeatInterval
		cfg.HeartbeatInterval = &value
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg, nil
}

func OpenClientSession(ctx context.Context, cfg *SessionConfig) (*ClientSession, error) {
	resolved, err := resolveSessionConfig(cfg)
	if err != nil {
		return nil, err
	}
	endpoint, err := resolveWebTTYEndpoint(resolved.URL)
	if err != nil {
		return nil, err
	}
	dialer := &websocket.Dialer{
		ReadBufferSize:    *resolved.ReadBufferSize,
		WriteBufferSize:   *resolved.WriteBufferSize,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
		Proxy:             http.ProxyFromEnvironment,
	}
	if endpoint.RequiresCustomDial {
		if resolved.DialContext == nil {
			return nil, fmt.Errorf("websocket url scheme %q requires a custom dialer", "rstrm")
		}
		dialer.NetDialContext = resolved.DialContext
	}
	conn, resp, err := dialer.DialContext(ctx, endpoint.URL, nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed with status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	runtimeCfg := resolved.clientConfig()
	if *resolved.MaxMessageSize > 0 {
		conn.SetReadLimit(*resolved.MaxMessageSize)
	}
	runtime := &clientRuntime{
		conn:     conn,
		cfg:      runtimeCfg,
		logger:   runtimeCfg.Logger.With("component", "webtty.client"),
		logProto: stringsEqualFoldTrimmed(rstream.Channel, "dev"),
	}
	openMessage, err := runtime.buildOpenMessage()
	if err != nil {
		runtime.closeConn()
		return nil, err
	}
	doneRead := make(chan struct{})
	readEvents := make(chan clientEvent, 1)
	go runtime.readLoop(doneRead, readEvents)
	if err := runtime.writeMessage(openMessage); err != nil {
		close(doneRead)
		runtime.closeConn()
		return nil, fmt.Errorf("failed to send open message: %w", err)
	}
	if err := runtime.waitForOpen(ctx, readEvents); err != nil {
		close(doneRead)
		runtime.closeConn()
		return nil, err
	}
	loopCtx, loopCancel := context.WithCancel(context.Background())
	session := &ClientSession{
		runtime:    runtime,
		loopCancel: loopCancel,
		doneRead:   doneRead,
		events:     make(chan ClientSessionEvent, 128),
		resultCh:   make(chan clientSessionResult, 1),
	}
	loopErrCh := make(chan error, 1)
	if resolved.SendHeartbeat {
		go runtime.heartbeatLoop(loopCtx, loopErrCh)
	}
	go session.run(readEvents, loopErrCh)
	go func() {
		<-ctx.Done()
		_ = session.CloseWithError(ctx.Err())
	}()
	return session, nil
}

func (s *ClientSession) Events() <-chan ClientSessionEvent { return s.events }

func (s *ClientSession) Wait() (int, error) {
	result, ok := <-s.resultCh
	if !ok {
		return -1, io.EOF
	}
	return result.exitCode, result.err
}

func (s *ClientSession) SendInput(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return s.runtime.writeMessage(&pb.Message{
		Payload: &pb.Message_Data{
			Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: append([]byte(nil), data...)}},
		},
	})
}

func (s *ClientSession) SendText(text string) error {
	if text == "" {
		return nil
	}
	return s.SendInput([]byte(text))
}

func (s *ClientSession) SendEOF() error {
	return s.runtime.writeMessage(&pb.Message{
		Payload: &pb.Message_Data{
			Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}},
		},
	})
}

func (s *ClientSession) Resize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return s.runtime.writeMessage(&pb.Message{
		Payload: &pb.Message_Parameter{
			Parameter: &pb.Parameter{
				Parameter: &pb.Parameter_TerminalSize{
					TerminalSize: &pb.TerminalSize{Row: uint32(rows), Col: uint32(cols)},
				},
			},
		},
	})
}

func (s *ClientSession) Close() error {
	s.requestClose(-1, nil, false)
	return nil
}

func (s *ClientSession) CloseWithError(err error) error {
	s.requestClose(-1, err, true)
	return nil
}

func (s *ClientSession) run(readEvents <-chan clientEvent, loopErrCh <-chan error) {
	for {
		select {
		case event := <-readEvents:
			if event.err != nil {
				s.finalize(-1, event.err)
				return
			}
			switch payload := event.msg.Payload.(type) {
			case *pb.Message_Data:
				sessionEvent, ok, err := decodeClientSessionEvent(payload.Data)
				if err != nil {
					s.finalize(-1, err)
					return
				}
				if !ok {
					continue
				}
				s.events <- sessionEvent
			case *pb.Message_Close:
				s.finalize(int(payload.Close.ReturnCode), nil)
				return
			case *pb.Message_Heartbeat:
			case *pb.Message_Error:
				if msg := stringsTrimSpace(payload.Error.Msg); msg != "" {
					s.finalize(-1, fmt.Errorf("%w: %s", errClientServer, msg))
				} else {
					s.finalize(-1, errClientServer)
				}
				return
			default:
				s.finalize(-1, fmt.Errorf("%w: %T", errClientUnexpected, payload))
				return
			}
		case err := <-loopErrCh:
			if err != nil {
				s.finalize(-1, err)
				return
			}
		}
	}
}

func (s *ClientSession) requestClose(exitCode int, err error, sendError bool) {
	s.closeTransportOnce.Do(func() {
		s.resultMu.Lock()
		s.closeResult = &clientSessionResult{exitCode: exitCode, err: err}
		s.resultMu.Unlock()
		s.loopCancel()
		if sendError && err != nil {
			_ = s.runtime.sendClientError(err)
		}
		s.runtime.closeConn()
	})
}

func (s *ClientSession) finalize(exitCode int, err error) {
	s.finalizeOnce.Do(func() {
		s.loopCancel()
		close(s.doneRead)
		result := clientSessionResult{exitCode: exitCode, err: err}
		s.resultMu.Lock()
		if s.closeResult != nil {
			result = *s.closeResult
		}
		s.resultMu.Unlock()
		s.resultCh <- result
		close(s.resultCh)
		close(s.events)
	})
}

func decodeClientSessionEvent(data *pb.Data) (ClientSessionEvent, bool, error) {
	if data == nil {
		return ClientSessionEvent{}, false, fmt.Errorf("received empty data message")
	}
	var stream ClientSessionStream
	switch data.Type {
	case pb.Data_TYPE_STDOUT:
		stream = ClientSessionStdout
	case pb.Data_TYPE_STDERR:
		stream = ClientSessionStderr
	default:
		return ClientSessionEvent{}, false, fmt.Errorf("unexpected data stream type: %v", data.Type)
	}
	switch payload := data.Payload.(type) {
	case *pb.Data_Data:
		return ClientSessionEvent{Stream: stream, Data: append([]byte(nil), payload.Data...)}, true, nil
	case *pb.Data_Eos:
		return ClientSessionEvent{}, false, nil
	default:
		return ClientSessionEvent{}, false, fmt.Errorf("unexpected data payload type: %T", payload)
	}
}

func stringsEqualFoldTrimmed(value, target string) bool {
	return stringsTrimSpaceFold(value) == stringsTrimSpaceFold(target)
}

func stringsTrimSpaceFold(value string) string {
	return strings.ToLower(stringsTrimSpace(value))
}

func stringsTrimSpace(value string) string {
	return strings.TrimSpace(value)
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

type ClientConfig struct {
	URL               string
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
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
	Logger            *slog.Logger
}

type clientRuntime struct {
	conn        *websocket.Conn
	cfg         *ClientConfig
	logger      *slog.Logger
	logProto    bool
	writeMu     sync.Mutex
	stdinFile   *os.File
	stdinFD     int
	hasTerminal bool
}

type clientEvent struct {
	msg *pb.Message
	err error
}

const (
	defaultClientCloseDeadline      time.Duration = 5 * time.Second
	defaultClientOpenDeadline       time.Duration = 5 * time.Second
	defaultTerminalResizePollPeriod time.Duration = 300 * time.Millisecond
)

var (
	errClientOperationTimeout = errors.New("operation timeout")
	errClientProtocol         = errors.New("protocol error")
	errClientServer           = errors.New("server error")
	errClientUnexpected       = errors.New("unexpected message")
)

func RunClient(ctx context.Context, cfg *ClientConfig) (int, error) {
	resolved, err := resolveClientConfig(cfg)
	if err != nil {
		return -1, err
	}
	endpoint, err := normalizeWebTTYURL(resolved.URL)
	if err != nil {
		return -1, err
	}
	dialer := &websocket.Dialer{
		ReadBufferSize:    *resolved.ReadBufferSize,
		WriteBufferSize:   *resolved.WriteBufferSize,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
		Proxy:             http.ProxyFromEnvironment,
	}
	conn, resp, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if resp != nil {
			return -1, fmt.Errorf("websocket dial failed with status %d", resp.StatusCode)
		}
		return -1, fmt.Errorf("websocket dial failed: %w", err)
	}
	if *resolved.MaxMessageSize > 0 {
		conn.SetReadLimit(*resolved.MaxMessageSize)
	}
	runtime := &clientRuntime{
		conn:     conn,
		cfg:      resolved,
		logger:   resolved.Logger.With("component", "webtty.client"),
		logProto: strings.EqualFold(strings.TrimSpace(rstream.Channel), "dev"),
	}
	defer runtime.closeConn()
	if file, ok := resolved.Stdin.(*os.File); ok {
		runtime.stdinFile = file
		runtime.stdinFD = int(file.Fd())
	}
	if resolved.AllocateTTY {
		if runtime.stdinFile == nil || !term.IsTerminal(runtime.stdinFD) {
			return -1, fmt.Errorf("tty allocation requires stdin to be a terminal")
		}
		runtime.hasTerminal = true
		if resolved.Interactive {
			state, err := term.MakeRaw(runtime.stdinFD)
			if err != nil {
				return -1, fmt.Errorf("failed to switch terminal to raw mode: %w", err)
			}
			defer term.Restore(runtime.stdinFD, state)
		}
	}
	openMessage, err := runtime.buildOpenMessage()
	if err != nil {
		return -1, err
	}
	doneCh := make(chan struct{})
	defer close(doneCh)
	eventCh := make(chan clientEvent, 1)
	go runtime.readLoop(doneCh, eventCh)
	if err := runtime.writeMessage(openMessage); err != nil {
		return -1, fmt.Errorf("failed to send open message: %w", err)
	}
	if err := runtime.waitForOpen(ctx, eventCh); err != nil {
		return -1, err
	}
	runtime.logger.Debug("webtty session acknowledged")
	loopCtx, stopLoops := context.WithCancel(context.Background())
	defer stopLoops()
	loopErrCh := make(chan error, 1)
	if resolved.Interactive {
		go runtime.stdinLoop(loopCtx, loopErrCh)
	}
	if resolved.AllocateTTY {
		go runtime.resizeLoop(loopCtx, loopErrCh)
	}
	if resolved.SendHeartbeat {
		go runtime.heartbeatLoop(loopCtx, loopErrCh)
	}
	var pendingErr error
	var closeTimer *time.Timer
	var closeTimeout <-chan time.Time
	defer func() {
		if closeTimer == nil {
			return
		}
		if !closeTimer.Stop() {
			select {
			case <-closeTimer.C:
			default:
			}
		}
	}()
	ctxDone := ctx.Done()
	for {
		select {
		case event := <-eventCh:
			if event.err != nil {
				if pendingErr != nil {
					return -1, pendingErr
				}
				return -1, event.err
			}
			exitCode, done, err := runtime.handleSessionMessage(event.msg)
			if done {
				if pendingErr != nil {
					return -1, pendingErr
				}
				return exitCode, err
			}
			if err != nil {
				return -1, err
			}
		case err := <-loopErrCh:
			if err == nil || pendingErr != nil {
				continue
			}
			pendingErr = err
			stopLoops()
			if err := runtime.sendClientError(err); err != nil {
				return -1, pendingErr
			}
			if resolved.CloseDeadline != nil && *resolved.CloseDeadline > 0 {
				closeTimer = time.NewTimer(*resolved.CloseDeadline)
				closeTimeout = closeTimer.C
			}
		case <-ctxDone:
			ctxDone = nil
			if pendingErr != nil {
				continue
			}
			pendingErr = ctx.Err()
			stopLoops()
			if err := runtime.sendClientError(pendingErr); err != nil {
				return -1, pendingErr
			}
			if resolved.CloseDeadline != nil && *resolved.CloseDeadline > 0 {
				closeTimer = time.NewTimer(*resolved.CloseDeadline)
				closeTimeout = closeTimer.C
			}
		case <-closeTimeout:
			if pendingErr != nil {
				return -1, pendingErr
			}
			return -1, errClientOperationTimeout
		}
	}
}

func resolveClientConfig(cfg *ClientConfig) (*ClientConfig, error) {
	if cfg == nil {
		cfg = &ClientConfig{}
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
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg, nil
}

func normalizeWebTTYURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("websocket url is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "ws://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid websocket url: %w", err)
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "ws" && scheme != "wss" {
		return "", fmt.Errorf("invalid websocket scheme %q (expected ws or wss)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("websocket host is required")
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func parseClientEnvVars(specs []string) ([]*pb.Environment, error) {
	out := make([]*pb.Environment, 0, len(specs))
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		key := spec
		value := ""
		hasValue := false
		if idx := strings.Index(spec, "="); idx >= 0 {
			key = spec[:idx]
			value = spec[idx+1:]
			hasValue = true
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid environment variable %q", spec)
		}
		if !hasValue {
			resolved, ok := os.LookupEnv(key)
			if !ok {
				continue
			}
			value = resolved
		}
		out = append(out, &pb.Environment{Key: key, Value: value})
	}
	return out, nil
}

func parseClientUsername(raw *string) (*pb.Username, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	isNumeric := true
	for _, r := range value {
		if r < '0' || r > '9' {
			isNumeric = false
			break
		}
	}
	if isNumeric {
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid user id %q: %w", value, err)
		}
		return &pb.Username{Payload: &pb.Username_Id{Id: uint32(parsed)}}, nil
	}
	return &pb.Username{Payload: &pb.Username_Name{Name: value}}, nil
}

func (c *clientRuntime) buildOpenMessage() (*pb.Message, error) {
	env, err := parseClientEnvVars(c.cfg.EnvVars)
	if err != nil {
		return nil, err
	}
	if c.cfg.AllocateTTY {
		if termValue, ok := os.LookupEnv("TERM"); ok && termValue != "" {
			hasTERM := false
			for _, e := range env {
				if strings.EqualFold(strings.TrimSpace(e.Key), "TERM") {
					hasTERM = true
					break
				}
			}
			if !hasTERM {
				env = append(env, &pb.Environment{Key: "TERM", Value: termValue})
			}
		}
	}
	username, err := parseClientUsername(c.cfg.Username)
	if err != nil {
		return nil, err
	}
	config := &pb.Config{
		Options: &pb.Options{
			Interactive:   c.cfg.Interactive,
			AllocateTty:   c.cfg.AllocateTTY,
			SendHeartbeat: c.cfg.SendHeartbeat,
		},
		CmdArgs: append([]string(nil), c.cfg.CmdArgs...),
		EnvVars: env,
	}
	if c.cfg.Workdir != nil && strings.TrimSpace(*c.cfg.Workdir) != "" {
		config.Workdir = &pb.Workdir{Value: strings.TrimSpace(*c.cfg.Workdir)}
	}
	if username != nil {
		config.Username = username
	}
	return &pb.Message{Payload: &pb.Message_Open{Open: &pb.Open{Config: config}}}, nil
}

func (c *clientRuntime) waitForOpen(ctx context.Context, eventCh <-chan clientEvent) error {
	var timer *time.Timer
	var timeout <-chan time.Time
	if c.cfg.OpenDeadline != nil && *c.cfg.OpenDeadline > 0 {
		timer = time.NewTimer(*c.cfg.OpenDeadline)
		timeout = timer.C
		defer func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}()
	}
	for {
		select {
		case event := <-eventCh:
			if event.err != nil {
				return event.err
			}
			return c.handleOpenMessage(event.msg)
		case <-timeout:
			return errClientOperationTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *clientRuntime) handleOpenMessage(msg *pb.Message) error {
	if msg == nil {
		return errClientProtocol
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_Ack:
		return nil
	case *pb.Message_Error:
		if strings.TrimSpace(payload.Error.Msg) != "" {
			return fmt.Errorf("%w: %s", errClientServer, payload.Error.Msg)
		}
		return errClientServer
	default:
		return fmt.Errorf("%w: %T", errClientUnexpected, payload)
	}
}

func (c *clientRuntime) handleSessionMessage(msg *pb.Message) (int, bool, error) {
	if msg == nil {
		return -1, true, errClientProtocol
	}
	switch payload := msg.Payload.(type) {
	case *pb.Message_Data:
		if err := c.handleData(payload.Data); err != nil {
			return -1, true, err
		}
		return -1, false, nil
	case *pb.Message_Close:
		return int(payload.Close.ReturnCode), true, nil
	case *pb.Message_Heartbeat:
		return -1, false, nil
	case *pb.Message_Error:
		if strings.TrimSpace(payload.Error.Msg) != "" {
			return -1, true, fmt.Errorf("%w: %s", errClientServer, payload.Error.Msg)
		}
		return -1, true, errClientServer
	default:
		return -1, true, fmt.Errorf("%w: %T", errClientUnexpected, payload)
	}
}

func (c *clientRuntime) readLoop(done <-chan struct{}, eventCh chan<- clientEvent) {
	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				select {
				case eventCh <- clientEvent{err: io.EOF}:
				case <-done:
				}
			} else {
				select {
				case eventCh <- clientEvent{err: fmt.Errorf("failed to read websocket message: %w", err)}:
				case <-done:
				}
			}
			return
		}
		if messageType != websocket.BinaryMessage {
			select {
			case eventCh <- clientEvent{err: fmt.Errorf("%w: websocket message type %d", errClientUnexpected, messageType)}:
			case <-done:
			}
			return
		}
		msg := &pb.Message{}
		if err := proto.Unmarshal(payload, msg); err != nil {
			select {
			case eventCh <- clientEvent{err: fmt.Errorf("%w: failed to decode protobuf message: %v", errClientProtocol, err)}:
			case <-done:
			}
			return
		}
		c.logProtoMessage("received", msg)
		select {
		case eventCh <- clientEvent{msg: msg}:
		case <-done:
			return
		}
	}
}

func (c *clientRuntime) handleData(data *pb.Data) error {
	if data == nil {
		return fmt.Errorf("received empty data message")
	}
	var writer io.Writer
	switch data.Type {
	case pb.Data_TYPE_STDOUT:
		writer = c.cfg.Stdout
	case pb.Data_TYPE_STDERR:
		writer = c.cfg.Stderr
	default:
		return fmt.Errorf("unexpected data stream type: %v", data.Type)
	}
	switch payload := data.Payload.(type) {
	case *pb.Data_Data:
		if err := writeAll(writer, payload.Data); err != nil {
			return fmt.Errorf("failed to write stream payload: %w", err)
		}
	case *pb.Data_Eos:
		return nil
	default:
		return fmt.Errorf("unexpected data payload type: %T", payload)
	}
	return nil
}

func (c *clientRuntime) stdinLoop(ctx context.Context, errCh chan<- error) {
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := c.cfg.Stdin.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			msg := &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: chunk}}}}
			if werr := c.writeMessage(msg); werr != nil {
				select {
				case errCh <- fmt.Errorf("failed to send stdin payload: %w", werr):
				default:
				}
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				eos := &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}}}}
				if werr := c.writeMessage(eos); werr != nil {
					select {
					case errCh <- fmt.Errorf("failed to send stdin eos: %w", werr):
					default:
					}
				}
				return
			}
			select {
			case errCh <- fmt.Errorf("failed to read stdin: %w", err):
			default:
			}
			return
		}
	}
}

func (c *clientRuntime) resizeLoop(ctx context.Context, errCh chan<- error) {
	if !c.hasTerminal {
		return
	}
	lastRows := -1
	lastCols := -1
	sendSize := func() error {
		cols, rows, err := term.GetSize(c.stdinFD)
		if err != nil {
			return fmt.Errorf("failed to read terminal size: %w", err)
		}
		if rows == lastRows && cols == lastCols {
			return nil
		}
		lastRows = rows
		lastCols = cols
		msg := &pb.Message{Payload: &pb.Message_Parameter{Parameter: &pb.Parameter{Parameter: &pb.Parameter_TerminalSize{TerminalSize: &pb.TerminalSize{Row: uint32(rows), Col: uint32(cols)}}}}}
		if err := c.writeMessage(msg); err != nil {
			return fmt.Errorf("failed to send terminal size: %w", err)
		}
		return nil
	}
	if err := sendSize(); err != nil {
		select {
		case errCh <- err:
		default:
		}
		return
	}
	notifier := newTerminalResizeNotifier(defaultTerminalResizePollPeriod)
	defer notifier.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notifier.C():
			if err := sendSize(); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
	}
}

func (c *clientRuntime) heartbeatLoop(ctx context.Context, errCh chan<- error) {
	interval := *c.cfg.HeartbeatInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msg := &pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}}
			if err := c.writeMessage(msg); err != nil {
				select {
				case errCh <- fmt.Errorf("failed to send heartbeat: %w", err):
				default:
				}
				return
			}
		}
	}
}

func (c *clientRuntime) sendClientError(err error) error {
	if err == nil {
		return nil
	}
	msg := &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: err.Error()}}}
	return c.writeMessage(msg)
}

func (c *clientRuntime) writeMessage(msg *pb.Message) error {
	c.logProtoMessage("sending", msg)
	payload, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (c *clientRuntime) closeConn() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	_ = c.conn.Close()
}

func (c *clientRuntime) logProtoMessage(direction string, msg *pb.Message) {
	if !c.logProto {
		return
	}
	payload, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(msg)
	if err != nil {
		c.logger.Debug("failed to marshal protobuf for logs", "error", err)
		return
	}
	c.logger.Debug("protobuf message", "direction", direction, "payload", string(payload))
}

func writeAll(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return fmt.Errorf("writer returned %d bytes", n)
		}
		payload = payload[n:]
	}
	return nil
}

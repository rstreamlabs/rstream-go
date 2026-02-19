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
	if err := runtime.writeMessage(openMessage); err != nil {
		return -1, fmt.Errorf("failed to send open message: %w", err)
	}
	ackCh := make(chan struct{})
	closeCodeCh := make(chan int, 1)
	readErrCh := make(chan error, 1)
	go runtime.readLoop(ackCh, closeCodeCh, readErrCh)
	select {
	case <-ackCh:
		runtime.logger.Debug("webtty session acknowledged")
	case code := <-closeCodeCh:
		return code, nil
	case err := <-readErrCh:
		return -1, err
	case <-ctx.Done():
		_ = runtime.sendClientError(ctx.Err())
		return -1, ctx.Err()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	loopErrCh := make(chan error, 3)
	if resolved.Interactive {
		go runtime.stdinLoop(runCtx, loopErrCh)
	}
	if resolved.AllocateTTY {
		go runtime.resizeLoop(runCtx, loopErrCh)
	}
	if resolved.SendHeartbeat {
		go runtime.heartbeatLoop(runCtx, loopErrCh)
	}
	for {
		select {
		case code := <-closeCodeCh:
			cancel()
			return code, nil
		case err := <-readErrCh:
			cancel()
			return -1, err
		case err := <-loopErrCh:
			if err != nil {
				cancel()
				_ = runtime.sendClientError(err)
				return -1, err
			}
		case <-ctx.Done():
			cancel()
			_ = runtime.sendClientError(ctx.Err())
			return -1, ctx.Err()
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
	if cfg.HeartbeatInterval == nil {
		value := 20 * time.Second
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

func (c *clientRuntime) readLoop(ackCh chan<- struct{}, closeCodeCh chan<- int, errCh chan<- error) {
	ackSent := false
	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				errCh <- io.EOF
				return
			}
			errCh <- fmt.Errorf("failed to read websocket message: %w", err)
			return
		}
		if messageType != websocket.BinaryMessage {
			errCh <- fmt.Errorf("unexpected websocket message type: %d", messageType)
			return
		}
		msg := &pb.Message{}
		if err := proto.Unmarshal(payload, msg); err != nil {
			errCh <- fmt.Errorf("failed to decode protobuf message: %w", err)
			return
		}
		c.logProtoMessage("received", msg)
		switch body := msg.Payload.(type) {
		case *pb.Message_Ack:
			if !ackSent {
				ackSent = true
				ackCh <- struct{}{}
			}
		case *pb.Message_Data:
			if err := c.handleData(body.Data); err != nil {
				errCh <- err
				return
			}
		case *pb.Message_Close:
			closeCodeCh <- int(body.Close.ReturnCode)
			return
		case *pb.Message_Error:
			errCh <- fmt.Errorf("server error: %s", body.Error.Msg)
			return
		case *pb.Message_Heartbeat:
			continue
		default:
			errCh <- fmt.Errorf("unexpected protobuf message type: %T", body)
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
				errCh <- fmt.Errorf("failed to send stdin payload: %w", werr)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				eos := &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}}}}
				if werr := c.writeMessage(eos); werr != nil {
					errCh <- fmt.Errorf("failed to send stdin eos: %w", werr)
				}
				return
			}
			errCh <- fmt.Errorf("failed to read stdin: %w", err)
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
		errCh <- err
		return
	}
	notifier := newTerminalResizeNotifier(300 * time.Millisecond)
	defer notifier.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notifier.C():
			if err := sendSize(); err != nil {
				errCh <- err
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
				errCh <- fmt.Errorf("failed to send heartbeat: %w", err)
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

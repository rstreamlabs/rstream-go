// See LICENSE file in the project root for license information.

package rtc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

type ClientConfig struct {
	HTTP      *http.Client
	Endpoint  string
	Info      Info
	RelayOnly bool
}

// RoundTrip keeps the same response contract as HTTP while the payload travels
// over WebRTC. The response body owns the peer and must be read or closed.
func RoundTrip(request *http.Request, config ClientConfig) (*http.Response, error) {
	if request.Body != nil {
		defer request.Body.Close()
	}
	if !ReadOnly(request.Method) {
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("WebRTC filesystem is read-only; writing is not supported")), Request: request}, nil
	}
	input := Request{Method: request.Method, URI: request.URL.RequestURI(), Header: request.Header.Clone()}
	input.Header.Del("Authorization")
	input.Header.Del("Cookie")
	if request.Body != nil {
		body, err := io.ReadAll(io.LimitReader(request.Body, (16<<10)+1))
		if err != nil {
			return nil, fmt.Errorf("read filesystem request: %w", err)
		}
		if len(body) > 16<<10 {
			return nil, fmt.Errorf("filesystem request exceeds 16 KiB")
		}
		input.Body = string(body)
	}
	peer, err := NewPeer(config.Info.ICEServers, config.RelayOnly)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(request.Context())
	body := &responseBody{ctx: ctx, cancel: cancel, peer: peer, packets: make(chan []byte, Window), header: make(chan Response, 1), ended: make(chan struct{}), closed: make(chan struct{}), expected: -1, head: request.Method == http.MethodHead}
	channel, err := peer.CreateDataChannel(Protocol, nil)
	if err != nil {
		cancel(err)
		_ = peer.Close()
		return nil, err
	}
	body.channel = channel
	channel.OnMessage(body.message)
	channel.OnError(func(err error) { cancel(err) })
	channel.OnClose(func() {
		if !body.complete.Load() {
			cancel(fmt.Errorf("filesystem WebRTC channel closed before transfer completed"))
		}
	})
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if (state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected) && !body.complete.Load() {
			cancel(fmt.Errorf("filesystem WebRTC connection %s", state))
		}
	})
	go func() {
		defer close(body.closed)
		<-ctx.Done()
		_ = peer.Close()
	}()
	setup, stop := boundedContext(ctx)
	defer stop()
	answer, err := body.connect(setup, request, config, input)
	if err != nil {
		_ = body.Close()
		return nil, err
	}
	body.workers.Add(1)
	go func() {
		defer body.workers.Done()
		body.lease(config, request.Header, answer.Session)
	}()
	select {
	case <-body.ctx.Done():
		_ = body.Close()
		return nil, fmt.Errorf("filesystem WebRTC response: %w", context.Cause(body.ctx))
	case <-setup.Done():
		_ = body.Close()
		return nil, fmt.Errorf("filesystem WebRTC response: %w", setup.Err())
	case response := <-body.header:
		length := int64(-1)
		if value := response.Header.Get("Content-Length"); value != "" {
			length, err = strconv.ParseInt(value, 10, 64)
			if err != nil || length < 0 {
				_ = body.Close()
				return nil, fmt.Errorf("invalid filesystem content length")
			}
		}
		return &http.Response{StatusCode: response.Status, Status: fmt.Sprintf("%d %s", response.Status, http.StatusText(response.Status)), Header: response.Header, Body: body, ContentLength: length, Request: request}, nil
	}
}

type responseBody struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	peer      *webrtc.PeerConnection
	channel   *webrtc.DataChannel
	packets   chan []byte
	header    chan Response
	ended     chan struct{}
	closed    chan struct{}
	complete  atomic.Bool
	hasHeader atomic.Bool
	closeOnce sync.Once
	workers   sync.WaitGroup
	current   []byte
	head      bool
	expected  int64
	received  int64
}

func (b *responseBody) connect(ctx context.Context, request *http.Request, config ClientConfig, input Request) (Answer, error) {
	answer := Answer{}
	offer, err := b.peer.CreateOffer(nil)
	if err != nil {
		return answer, err
	}
	sdp, err := Gather(ctx, b.peer, offer)
	if err != nil {
		return answer, err
	}
	if err := SignalHTTP(ctx, config.HTTP, config.Endpoint, request.Header, Signal{Action: "offer", SDP: sdp, Request: &input}, &answer); err != nil {
		return answer, err
	}
	if answer.Session == "" || answer.SDP == "" {
		return answer, fmt.Errorf("invalid filesystem WebRTC answer")
	}
	return answer, b.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP})
}

func (b *responseBody) message(message webrtc.DataChannelMessage) {
	if !message.IsString {
		if len(message.Data) > ChunkSize || !b.hasHeader.Load() || b.complete.Load() {
			b.cancel(fmt.Errorf("invalid filesystem data frame"))
			return
		}
		b.received += int64(len(message.Data))
		if b.expected >= 0 && b.received > b.expected {
			b.cancel(fmt.Errorf("filesystem response exceeds content length"))
			return
		}
		select {
		case b.packets <- message.Data:
		default:
			b.cancel(fmt.Errorf("filesystem receive window exceeded"))
		}
		return
	}
	var response Response
	if len(message.Data) > MaxSignal || json.Unmarshal(message.Data, &response) != nil {
		b.cancel(fmt.Errorf("invalid filesystem response"))
		return
	}
	if response.Error != "" {
		b.cancel(fmt.Errorf("filesystem transfer: %s", response.Error))
		return
	}
	if response.Done {
		if !b.hasHeader.Load() || !b.complete.CompareAndSwap(false, true) {
			b.cancel(fmt.Errorf("invalid filesystem completion"))
			return
		}
		if b.expected >= 0 && b.received != b.expected {
			b.cancel(io.ErrUnexpectedEOF)
			return
		}
		close(b.ended)
		return
	}
	if response.Status < 200 || response.Status > 599 || !b.hasHeader.CompareAndSwap(false, true) {
		b.cancel(fmt.Errorf("invalid filesystem response headers"))
		return
	}
	if value := response.Header.Get("Content-Length"); value != "" {
		length, err := strconv.ParseInt(value, 10, 64)
		if err != nil || length < 0 {
			b.cancel(fmt.Errorf("invalid filesystem content length"))
			return
		}
		b.expected = length
	}
	if b.head || response.Status == http.StatusNoContent || response.Status == http.StatusNotModified {
		b.expected = 0
	}
	b.header <- response
}

func (b *responseBody) lease(config ClientConfig, headers http.Header, session string) {
	ticker := time.NewTicker(max(time.Second, time.Duration(config.Info.LeaseSeconds)*time.Second/3))
	defer ticker.Stop()
	restarted := time.Now()
	interval := time.Duration(config.Info.RestartSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := boundedContext(b.ctx)
			var err error
			if b.hasHeader.Load() && time.Since(restarted) >= interval {
				err = b.restart(ctx, config, headers, session)
				restarted = time.Now()
			} else {
				err = SignalHTTP(ctx, config.HTTP, config.Endpoint, headers, Signal{Action: "renew", Session: session}, nil)
			}
			cancel()
			if err != nil {
				b.cancel(fmt.Errorf("filesystem authorization renewal: %w", err))
				return
			}
		}
	}
}

func (b *responseBody) restart(ctx context.Context, config ClientConfig, headers http.Header, session string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.Endpoint, nil)
	if err != nil {
		return err
	}
	request.Header = headers.Clone()
	request.Header.Del("Range")
	response, err := HTTPDo(config.HTTP, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("filesystem ICE refresh failed (%d)", response.StatusCode)
	}
	var info Info
	if err := Decode(response.Body, &info); err != nil {
		return err
	}
	configuration := b.peer.GetConfiguration()
	configuration.ICEServers = info.ICEServers
	if err := b.peer.SetConfiguration(configuration); err != nil {
		return err
	}
	offer, err := b.peer.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		return err
	}
	sdp, err := Gather(ctx, b.peer, offer)
	if err != nil {
		return err
	}
	var answer Answer
	if err := SignalHTTP(ctx, config.HTTP, config.Endpoint, headers, Signal{Action: "restart", Session: session, SDP: sdp}, &answer); err != nil {
		return err
	}
	return b.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP})
}

func (b *responseBody) Read(target []byte) (int, error) {
	if err := context.Cause(b.ctx); err != nil {
		return 0, err
	}
	if len(target) == 0 {
		return 0, nil
	}
	if len(b.current) == 0 {
		select {
		case b.current = <-b.packets:
		default:
			select {
			case b.current = <-b.packets:
			case <-b.ctx.Done():
				return 0, context.Cause(b.ctx)
			case <-b.ended:
				select {
				case b.current = <-b.packets:
				default:
					_ = b.channel.SendText("done")
					return 0, io.EOF
				}
			}
		}
	}
	count := copy(target, b.current)
	b.current = b.current[count:]
	if len(b.current) == 0 {
		if err := b.channel.SendText("credit"); err != nil && !b.complete.Load() {
			b.cancel(err)
		}
	}
	return count, nil
}

func (b *responseBody) Close() error {
	b.closeOnce.Do(func() { b.cancel(context.Canceled) })
	<-b.closed
	b.workers.Wait()
	return nil
}

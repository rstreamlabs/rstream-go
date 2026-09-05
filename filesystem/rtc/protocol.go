// See LICENSE file in the project root for license information.

// Package rtc carries read-only filesystem responses over WebRTC DataChannels.
// HTTP remains the authenticated signaling channel, never a write fallback.
package rtc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

const Endpoint = "/.rstream/files/v1"
const Protocol = "rstream.files.v1"
const ChunkSize = 32 << 10
const Window = 32
const MaxSignal = 128 << 10

type ICEProvider func(context.Context) ([]webrtc.ICEServer, error)

type Info struct {
	Version        int                `json:"version"`
	Backend        string             `json:"backend"`
	ICEServers     []webrtc.ICEServer `json:"ice_servers,omitempty"`
	RestartSeconds int                `json:"restart_seconds,omitempty"`
	LeaseSeconds   int                `json:"lease_seconds,omitempty"`
}

type Request struct {
	Method string      `json:"method"`
	URI    string      `json:"uri"`
	Header http.Header `json:"headers,omitempty"`
	Body   string      `json:"body,omitempty"`
}

type Signal struct {
	Action  string   `json:"action"`
	Session string   `json:"session,omitempty"`
	SDP     string   `json:"sdp,omitempty"`
	Request *Request `json:"request,omitempty"`
}

type Answer struct {
	Session string `json:"session"`
	SDP     string `json:"sdp"`
}

type Response struct {
	Status int         `json:"status,omitempty"`
	Header http.Header `json:"headers,omitempty"`
	Done   bool        `json:"done,omitempty"`
	Error  string      `json:"error,omitempty"`
}

func ReadOnly(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == "PROPFIND"
}

func NewPeer(servers []webrtc.ICEServer, relay bool) (*webrtc.PeerConnection, error) {
	settings := webrtc.SettingEngine{}
	settings.SetSCTPMaxReceiveBufferSize(2 * ChunkSize * Window)
	settings.SetIncludeLoopbackCandidate(true)
	configuration := webrtc.Configuration{ICEServers: servers}
	if relay {
		configuration.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	return webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(configuration)
}

func Gather(ctx context.Context, peer *webrtc.PeerConnection, description webrtc.SessionDescription) (string, error) {
	complete := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(description); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-complete:
		return peer.LocalDescription().SDP, nil
	}
}

func Decode(reader io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(reader, MaxSignal+1))
	if err != nil {
		return err
	}
	if len(data) > MaxSignal {
		return fmt.Errorf("filesystem signaling exceeds 128 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one signaling object")
	}
	return nil
}

func SignalHTTP(ctx context.Context, client *http.Client, endpoint string, headers http.Header, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Del("Range")
	request.Header.Del("If-Range")
	request.Header.Del("Content-Length")
	request.Header.Set("Content-Type", "application/json")
	response, err := HTTPDo(client, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return fmt.Errorf("read filesystem signaling error (%d): %w", response.StatusCode, err)
		}
		return fmt.Errorf("filesystem signaling failed (%d): %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output == nil {
		return nil
	}
	return Decode(response.Body, output)
}

func boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 20*time.Second)
}

// HTTPDo retains the authenticated transport without following redirects.
func HTTPDo(client *http.Client, request *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return bounded.Do(request)
}

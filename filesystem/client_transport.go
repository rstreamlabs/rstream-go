// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
)

type ClientTransport struct {
	HTTP      *http.Client
	BaseURL   string
	RelayOnly bool
}

// NewHTTPClient selects the server's advertised backend for every operation.
// The signaling client retains its existing authentication and rstrm dialer.
func NewHTTPClient(baseURL string, signaling *http.Client) *http.Client {
	if signaling == nil {
		signaling = http.DefaultClient
	}
	client := *signaling
	client.Transport = &ClientTransport{HTTP: signaling, BaseURL: baseURL}
	return &client
}

func (t *ClientTransport) RoundTrip(request *http.Request) (result *http.Response, failure error) {
	defer func() {
		if failure != nil && request.Body != nil {
			_ = request.Body.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	base, err := url.Parse(t.BaseURL)
	if err != nil {
		return nil, err
	}
	if request.URL.Scheme != base.Scheme || request.URL.Host != base.Host {
		return nil, fmt.Errorf("filesystem requests must remain on the shared origin")
	}
	base.Path = strings.TrimRight(base.Path, "/") + rtc.Endpoint
	base.RawPath = ""
	discovery, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	discovery.Header = request.Header.Clone()
	discovery.Header.Del("Range")
	discovery.Header.Del("If-Range")
	response, err := rtc.HTTPDo(t.HTTP, discovery)
	if err != nil {
		return nil, fmt.Errorf("discover filesystem backend: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return t.httpTransport().RoundTrip(request)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discover filesystem backend: status %d", response.StatusCode)
	}
	var info rtc.Info
	if err := rtc.Decode(io.LimitReader(response.Body, rtc.MaxSignal), &info); err != nil {
		return nil, fmt.Errorf("invalid filesystem backend metadata: %w", err)
	}
	if info.Version != 1 {
		return nil, fmt.Errorf("unsupported filesystem protocol version %d", info.Version)
	}
	switch info.Backend {
	case BackendWebDAV:
		return t.httpTransport().RoundTrip(request)
	case BackendWebRTC:
		return rtc.RoundTrip(request, rtc.ClientConfig{HTTP: t.HTTP, Endpoint: base.String(), Info: info, RelayOnly: t.RelayOnly})
	default:
		return nil, fmt.Errorf("unsupported filesystem backend %q", info.Backend)
	}
}

func (t *ClientTransport) CloseIdleConnections() { t.HTTP.CloseIdleConnections() }

func (t *ClientTransport) httpTransport() http.RoundTripper {
	if t.HTTP != nil && t.HTTP.Transport != nil {
		return t.HTTP.Transport
	}
	return http.DefaultTransport
}

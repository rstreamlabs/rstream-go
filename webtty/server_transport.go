// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"

	rstream "github.com/rstreamlabs/rstream-go"
)

// ResolveTransport selects the advertised transport before opening a session.
// Legacy datagram servers use WebTransport; other legacy servers retain the
// historical WebSocket default and allow an explicit override.
func (s *ServerInfo) ResolveTransport(requested WebTTYTransport) (WebTTYTransport, error) {
	if requested != "" && !validWebTTYTransport(requested) {
		return "", fmt.Errorf("unsupported WebTTY transport %q", requested)
	}
	advertised := WebTTYTransport("")
	if s != nil {
		if s.Transport != nil {
			advertised = *s.Transport
			if !validWebTTYTransport(advertised) {
				return "", fmt.Errorf("server advertises an invalid WebTTY transport %q", advertised)
			}
		} else if s.TunnelType != nil && *s.TunnelType == rstream.TunnelTypeDatagram {
			advertised = WebTTYTransportWebTransport
		}
		if advertised != "" && s.TunnelType != nil {
			datagrams := *s.TunnelType == rstream.TunnelTypeDatagram
			if (advertised == WebTTYTransportWebTransport) != datagrams {
				return "", fmt.Errorf("server WebTTY transport %q conflicts with tunnel type %q", advertised, *s.TunnelType)
			}
		}
		if advertised == WebTTYTransportWebTransport && s.HTTPVersion != nil && *s.HTTPVersion != rstream.HTTP3 {
			return "", fmt.Errorf("server WebTransport requires HTTP/3, got %q", *s.HTTPVersion)
		}
	}
	if advertised != "" {
		if requested != "" && requested != advertised {
			return "", fmt.Errorf("requested WebTTY transport %q conflicts with server transport %q", requested, advertised)
		}
		return advertised, nil
	}
	if requested != "" {
		return requested, nil
	}
	return WebTTYTransportWebSocket, nil
}

func validWebTTYTransport(transport WebTTYTransport) bool {
	return transport == WebTTYTransportPlain || transport == WebTTYTransportWebSocket || transport == WebTTYTransportWebTransport
}

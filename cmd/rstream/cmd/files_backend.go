// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/filesystem"
	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
	"github.com/spf13/cobra"
)

func filesystemRTCConfig(cmd *cobra.Command, backend string) (rtc.ServerConfig, func() error, error) {
	if backend != filesystem.BackendWebRTC {
		return rtc.ServerConfig{}, func() error { return nil }, nil
	}
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return rtc.ServerConfig{}, nil, err
	}
	configured := runtime.Resolved.Context
	if configured == nil || configured.ProjectEndpoint == "" {
		return rtc.ServerConfig{}, nil, fmt.Errorf("WebRTC file sharing requires an rstream project context for STUN/TURN")
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return rtc.ServerConfig{}, nil, err
	}
	options := rstream.CreateTURNCredentialsOptions{Client: client, APIURL: runtime.Resolved.APIURL, Token: runtime.Resolved.Token, ProjectEndpoint: configured.ProjectEndpoint, TURNDomain: configured.TURNDomain, TURNRealm: configured.TURNRealm, TURNPort: configured.TURNPort, TURNSPort: configured.TURNSPort, TTL: time.Hour, ControlPlaneHeaders: runtime.Resolved.ControlPlaneHeaders}
	return rtc.ServerConfig{ICE: func(ctx context.Context) ([]webrtc.ICEServer, error) {
		credentials, err := rstream.CreateTURNCredentials(ctx, options)
		if err != nil {
			return nil, err
		}
		stun := make([]string, 0, 1)
		urls := make([]string, 0, len(credentials.URLs))
		for _, url := range credentials.URLs {
			if strings.HasPrefix(url, "turn:") && strings.Contains(url, "transport=udp") {
				stun = append(stun, "stun:"+strings.SplitN(strings.TrimPrefix(url, "turn:"), "?", 2)[0])
			}
			if !(strings.HasPrefix(url, "turns:") && strings.Contains(url, "transport=udp")) {
				urls = append(urls, url)
			}
		}
		servers := []webrtc.ICEServer{{URLs: urls, Username: credentials.Username, Credential: credentials.Credential}}
		if len(stun) > 0 {
			servers = append(servers, webrtc.ICEServer{URLs: stun})
		}
		return servers, nil
	}}, client.Close, nil
}

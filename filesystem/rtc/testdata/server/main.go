// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"
	"github.com/rstreamlabs/rstream-go/fileserver"
	filesui "github.com/rstreamlabs/rstream-go/fileserver/ui"
	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", "", "synthetic fixture directory")
	mode := flag.String("mode", "files", "files or webtty")
	backend := flag.String("backend", "webrtc", "filesystem backend")
	relay := flag.Bool("turn", false, "provide a local TURN test relay")
	rtcOnly := flag.Bool("rtc-only", true, "reject direct HTTP data requests")
	address := flag.String("addr", "127.0.0.1:0", "loopback listener")
	lease := flag.Duration("lease", 90*time.Second, "test authorization lease")
	restart := flag.Duration("restart", 5*time.Minute, "test ICE refresh interval")
	flag.Parse()
	if *mode != "files" && *mode != "webtty" {
		return fmt.Errorf("invalid fixture mode %q", *mode)
	}
	config := rtc.ServerConfig{LeaseDuration: *lease, RestartInterval: *restart}
	if *relay {
		listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer listener.Close()
		server, err := turn.NewServer(turn.ServerConfig{Realm: "files-fixture", AuthHandler: func(request *turn.RequestAttributes) (string, []byte, bool) {
			return request.Username, turn.GenerateAuthKey("reader", "files-fixture", "fixture-password"), request.Username == "reader"
		}, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: listener, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}}}})
		if err != nil {
			return err
		}
		defer server.Close()
		config.ICE = func(context.Context) ([]webrtc.ICEServer, error) {
			return []webrtc.ICEServer{{URLs: []string{"turn:" + listener.LocalAddr().String() + "?transport=udp"}, Username: "reader", Credential: "fixture-password"}}, nil
		}
	}
	var handler http.Handler
	if *mode == "files" {
		service, err := fileserver.New(fileserver.Config{Root: *root, Backend: *backend, RTC: config, UI: filesui.Handler()})
		if err != nil {
			return err
		}
		defer service.Close()
		handler = service
	} else {
		service, err := webtty.NewFileSystemHandler(&webtty.FileSystemConfig{Root: *root, Backend: *backend, RTC: config})
		if err != nil {
			return err
		}
		defer service.(io.Closer).Close()
		handler = service
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *rtcOnly && (strings.HasPrefix(r.URL.Path, "/fs") && !strings.HasSuffix(r.URL.Path, rtc.Endpoint) || r.URL.Path == "/_rstream/files/v1/archive") {
			http.Error(w, "Direct HTTP data disabled in WebRTC qualification fixture", 418)
			return
		}
		handler.ServeHTTP(w, r)
	})}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); _ = server.Close() }()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"url": "http://" + listener.Addr().String()}); err != nil {
		return err
	}
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

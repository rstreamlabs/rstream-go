// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	address, err := relayAddress()
	if err != nil {
		return err
	}
	udp, err := net.ListenPacket("udp4", net.JoinHostPort(address.String(), "0"))
	if err != nil {
		return err
	}
	defer udp.Close()
	relay, err := turn.NewServer(turn.ServerConfig{Realm: "probe", AuthHandler: func(r *turn.RequestAttributes) (string, []byte, bool) {
		return r.Username, turn.GenerateAuthKey("reader", "probe", "test-password"), r.Username == "reader"
	}, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: address, Address: address.String()}}}})
	if err != nil {
		return err
	}
	defer relay.Close()
	if os.Getenv("TURN_ONLY") == "1" {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"turn": "turn:" + udp.LocalAddr().String() + "?transport=udp"}); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	ice := []webrtc.ICEServer{{URLs: []string{"turn:" + udp.LocalAddr().String() + "?transport=udp"}, Username: "reader", Credential: "test-password"}}
	if external := os.Getenv("EXTERNAL_TURN"); external != "" {
		ice[0].URLs = []string{external}
	}
	settings := webrtc.SettingEngine{}
	settings.SetIncludeLoopbackCandidate(true)
	settings.SetSCTPMaxReceiveBufferSize(2 * 32768 * 32)
	peer, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(webrtc.Configuration{ICEServers: ice})
	if err != nil {
		return err
	}
	defer peer.Close()
	var workers sync.WaitGroup
	peer.OnDataChannel(func(channel *webrtc.DataChannel) {
		credits := make(chan struct{}, 32)
		for range 32 {
			credits <- struct{}{}
		}
		channel.OnMessage(func(message webrtc.DataChannelMessage) {
			if string(message.Data) == "credit" {
				select {
				case credits <- struct{}{}:
				default:
					cancel()
				}
			}
		})
		channel.OnOpen(func() {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for index := range 256 {
					select {
					case <-ctx.Done():
						return
					case <-credits:
					}
					data := make([]byte, 32768)
					binary.BigEndian.PutUint32(data, uint32(index))
					for i := 4; i < len(data); i++ {
						data[i] = byte(index % 251)
					}
					if err := channel.Send(data); err != nil {
						fmt.Fprintln(os.Stderr, "send", err)
						return
					}
				}
			}()
		})
	})
	var negotiations sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/signal", func(w http.ResponseWriter, r *http.Request) {
		negotiations.Lock()
		defer negotiations.Unlock()
		var offer webrtc.SessionDescription
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&offer); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		configuration := peer.GetConfiguration()
		configuration.ICEServers = ice
		if err := peer.SetConfiguration(configuration); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := peer.SetRemoteDescription(offer); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		answer, err := peer.CreateAnswer(nil)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		complete := webrtc.GatheringCompletePromise(peer)
		if err := peer.SetLocalDescription(answer); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		select {
		case <-complete:
		case <-r.Context().Done():
			return
		case <-ctx.Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(peer.LocalDescription())
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"url": "http://" + listener.Addr().String() + "/signal", "iceServers": ice}); err != nil {
		return err
	}
	err = server.Serve(listener)
	cancel()
	_ = peer.Close()
	workers.Wait()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func relayAddress() (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil {
				return ip.To4(), nil
			}
		}
	}
	return nil, fmt.Errorf("no active IPv4 interface")
}

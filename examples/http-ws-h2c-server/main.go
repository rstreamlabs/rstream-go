// See LICENSE file in the project root for license information.

// http-ws-h2c-server starts a WebSocket server over HTTP/2 cleartext (h2c)
// behind an rstream BytestreamTunnel. Downstream clients connect using a
// standard HTTP/1.1 or HTTP/2 WebSocket client — rstream translates protocols
// transparently. The server uses Extended CONNECT (RFC 8441).
//
// Run: GODEBUG=http2xconnect=1 go run . (internal only)
// Client: use examples/http-ws-h1-client with -tunnel ws-h2c-example.

package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
)

const maxWebSocketFramePayload = 1 << 20

// echoWSFrames reads WebSocket frames from r and writes them back to w,
// flushing after each frame. It returns on a close frame or on any I/O error.
// This minimal framing loop is the only server-side WebSocket code required
// when the transport is Extended CONNECT: the framing stays at the app layer
// while HTTP/2 handles multiplexing and flow control.
func echoWSFrames(r io.Reader, w io.Writer, flush func()) {
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return
		}
		opcode := hdr[0] & 0x0f
		masked := hdr[1]&0x80 != 0
		payloadLen := uint64(hdr[1] & 0x7f)
		switch payloadLen {
		case 126:
			ext := make([]byte, 2)
			if _, err := io.ReadFull(r, ext); err != nil {
				return
			}
			payloadLen = uint64(binary.BigEndian.Uint16(ext))
		case 127:
			ext := make([]byte, 8)
			if _, err := io.ReadFull(r, ext); err != nil {
				return
			}
			payloadLen = binary.BigEndian.Uint64(ext)
		}
		if payloadLen > maxWebSocketFramePayload {
			return
		}
		plen := int(payloadLen)
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(r, mask[:]); err != nil {
				return
			}
		}
		payload := make([]byte, plen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		// Opcode 0x8 = close; echo the close frame and stop.
		finOpcode := []byte{0x80 | opcode}
		var lenByte byte
		switch {
		case plen <= 125:
			lenByte = byte(plen)
		case plen <= 65535:
			lenByte = 126
		default:
			lenByte = 127
		}
		frame := []byte{finOpcode[0], lenByte}
		if lenByte == 126 {
			frame = append(frame, byte(plen>>8), byte(plen))
		} else if lenByte == 127 {
			ext := make([]byte, 8)
			binary.BigEndian.PutUint64(ext, uint64(plen))
			frame = append(frame, ext...)
		}
		frame = append(frame, payload...)
		if _, err := w.Write(frame); err != nil {
			return
		}
		flush()
		if opcode == 0x8 {
			return
		}
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	// Extended CONNECT (RFC 8441): the :protocol pseudo-header identifies the
	// upgrade protocol. x/net/http2 surfaces it in r.Header.
	if r.Method != http.MethodConnect || r.Header.Get(":protocol") != "websocket" {
		http.Error(w, "expected Extended CONNECT with :protocol=websocket", http.StatusBadRequest)
		return
	}
	// Respond 200 to acknowledge the tunnel; r.Body is the client→server stream.
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()
	echoWSFrames(r.Body, w, flusher.Flush)
}

func run(ctx context.Context, client *rstream.Client) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine: %w", err)
	}
	defer ctrl.Close()
	// HTTPVersion: rstream.HTTP2 tells the engine that the upstream speaks h2c.
	// Downstream clients still connect using any standard WebSocket client —
	// the engine handles the H1→H2C or H2→H2C protocol translation automatically.
	tunnel, err := ctrl.CreateTunnel(ctx, rstream.TunnelProperties{
		Name:        rstream.StringPtr("ws-h2c-example"),
		Publish:     rstream.BoolPtr(false),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP2),
	})
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	forwardingAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	bs, ok := tunnel.(rstream.BytestreamTunnel)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.BytestreamTunnel")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/websocket", wsHandler)
	h2srv := &http2.Server{}
	opts := &http2.ServeConnOpts{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		for {
			conn, err := bs.Accept()
			if err != nil {
				errCh <- err
				return
			}
			go h2srv.ServeConn(conn, opts)
		}
	}()
	select {
	case <-ctx.Done():
		return bs.Close()
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}
}

func main() {
	flag.Parse()
	// GODEBUG=http2xconnect=1 must be set before the process starts so that
	// x/net/http2 advertises SETTINGS_ENABLE_CONNECT_PROTOCOL to downstream
	// clients. If absent, WebSocket Extended CONNECT requests will be rejected.
	if _, ok := os.LookupEnv("GODEBUG"); !ok {
		log.Println("warning: GODEBUG=http2xconnect=1 is recommended for Extended CONNECT support")
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx, client); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

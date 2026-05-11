// See LICENSE file in the project root for license information.

// http-ws-h3-server starts a WebSocket server over HTTP/3 behind an rstream
// DatagramTunnel. Downstream clients connect with any standard WebSocket
// client — rstream translates the incoming protocol (H1 or H2 or H3) to
// Extended CONNECT over HTTP/3 (RFC 9220) toward this server transparently.
//
// Run: go run . (internal only)
// Client: use examples/http-ws-h1-client with -tunnel ws-h3-example.

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const maxWebSocketFramePayload = 1 << 20

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"ws-h3-example", "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{http3.NextProtoH3}}, nil
}

// echoWSFrames reads WebSocket frames from r and writes them back to w,
// flushing after each frame. It returns on a close frame or on any I/O error.
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
	// Extended CONNECT over HTTP/3 (RFC 9220): quic-go/http3 surfaces the
	// :protocol pseudo-header as r.Proto rather than r.Header.
	if r.Method != http.MethodConnect || r.Proto != "websocket" {
		http.Error(w, "expected Extended CONNECT with :protocol=websocket", http.StatusBadRequest)
		return
	}
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
	// Type: DatagramTunnel + HTTPVersion: HTTP3 configures an HTTP/3 upstream.
	// Downstream clients use any standard WebSocket client; rstream translates
	// from H1/H2/H3 WebSocket to Extended CONNECT (RFC 9220) toward this server.
	tunnel, err := ctrl.CreateTunnel(ctx, rstream.TunnelProperties{
		Name:        rstream.StringPtr("ws-h3-example"),
		Type:        rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish:     rstream.BoolPtr(false),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3),
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
	dg, ok := tunnel.(rstream.DatagramTunnel)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.DatagramTunnel")
	}
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	mux := http.NewServeMux()
	mux.HandleFunc("/websocket", wsHandler)
	srv := &http3.Server{
		TLSConfig: tlsCfg,
		Handler:   mux,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(rstream.PacketConnFromPacketListener(dg))
	}()
	select {
	case <-ctx.Done():
		return srv.Close()
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}
}

func main() {
	flag.Parse()
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

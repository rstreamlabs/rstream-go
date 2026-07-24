// See LICENSE file in the project root for license information.

// ws-matrix-server is the server side of the WebSocket coverage matrix.
// It opens an rstream tunnel and serves a WebSocket echo endpoint at /ws,
// supporting three upstream transport modes selectable via --upstream:
//
//   - h1  : HTTP/1.1 WebSocket (gorilla) over a BytestreamTunnel
//   - h2c : HTTP/2 Extended CONNECT (RFC 8441) over a BytestreamTunnel
//   - h3  : HTTP/3 Extended CONNECT (RFC 9220) over a DatagramTunnel
//
// The server prints "READY <forwarding-address>" to stdout once the tunnel is
// established, then echoes every received WebSocket frame back to the sender.

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/test/e2eenv"
	"golang.org/x/net/http2"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1)}
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

// reexec restarts the current process with the given GODEBUG value added.
func reexec(godebug string) {
	existing := os.Getenv("GODEBUG")
	if existing != "" {
		godebug = existing + "," + godebug
	}
	env := os.Environ()
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "GODEBUG=") {
			env[i] = "GODEBUG=" + godebug
			found = true
			break
		}
	}
	if !found {
		env = append(env, "GODEBUG="+godebug)
	}
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		log.Fatalf("re-exec failed: %v", err)
	}
	os.Exit(0)
}

// wsReadFrame reads one WebSocket frame from r, unmasks if needed, and returns
// the opcode and unmasked payload. FIN bit is ignored (fragmentation not needed
// for the echo test).
func wsReadFrame(r io.Reader) (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return
	}
	opcode = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	plen := int(hdr[1] & 0x7f)
	if plen == 126 {
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return
		}
		plen = int(binary.BigEndian.Uint16(ext))
	} else if plen == 127 {
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return
		}
		plen = int(binary.BigEndian.Uint64(ext))
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(r, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, plen)
	if _, err = io.ReadFull(r, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// wsWriteFrame writes one unmasked WebSocket frame (server→client) to w.
func wsWriteFrame(w io.Writer, opcode byte, payload []byte) error {
	hdr := []byte{0x80 | opcode}
	plen := len(payload)
	switch {
	case plen <= 125:
		hdr = append(hdr, byte(plen))
	case plen <= 65535:
		hdr = append(hdr, 126, byte(plen>>8), byte(plen))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0,
			byte(plen>>24), byte(plen>>16), byte(plen>>8), byte(plen))
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// echoWSFrames reads WebSocket frames from r and writes echo frames to w,
// calling flush after each write. Returns on close frame or error.
func echoWSFrames(r io.Reader, w io.Writer, flush func()) {
	for {
		opcode, payload, err := wsReadFrame(r)
		if err != nil {
			return
		}
		switch opcode {
		case 0x8: // close
			_ = wsWriteFrame(w, 0x8, payload)
			flush()
			return
		case 0x9: // ping
			_ = wsWriteFrame(w, 0xA, payload)
			flush()
		default:
			_ = wsWriteFrame(w, opcode, payload)
			flush()
		}
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

var gorillaUpgrader = websocket.Upgrader{CheckOrigin: sameOrigin}

func wsH1Handler(w http.ResponseWriter, r *http.Request) {
	conn, err := gorillaUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := conn.WriteMessage(mt, msg); err != nil {
			return
		}
	}
}

func wsExtendedConnectHandler(isH3 bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "origin does not match request host", http.StatusForbidden)
			return
		}
		isConnect := r.Method == http.MethodConnect
		var isWS bool
		if isH3 {
			isWS = r.Proto == "websocket"
		} else {
			isWS = r.Header.Get(":protocol") == "websocket"
		}
		if !isConnect || !isWS {
			http.Error(w, "expected WebSocket Extended CONNECT", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		echoWSFrames(r.Body, w, flusher.Flush)
	}
}

func runH1(ctx context.Context, tunnel rstream.BytestreamTunnel) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsH1Handler)
	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err := srv.Serve(tunnel)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runH2C(ctx context.Context, tunnel rstream.BytestreamTunnel) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsExtendedConnectHandler(false))
	h2srv := &http2.Server{}
	opts := &http2.ServeConnOpts{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		for {
			conn, err := tunnel.Accept()
			if err != nil {
				errCh <- err
				return
			}
			go h2srv.ServeConn(conn, opts)
		}
	}()
	select {
	case <-ctx.Done():
		_ = tunnel.Close()
		return nil
	case err := <-errCh:
		return err
	}
}

func runH3(ctx context.Context, tunnel rstream.DatagramTunnel) error {
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("TLS config: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsExtendedConnectHandler(true))
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	srv := &http3.Server{
		TLSConfig: tlsCfg,
		Handler:   mux,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(rstream.PacketConnFromPacketListener(tunnel))
	}()
	select {
	case <-ctx.Done():
		_ = srv.Close()
		return nil
	case err := <-errCh:
		return err
	}
}

func run(ctx context.Context, client *rstream.Client, upstream, name string) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ctrl.Close()
	props := rstream.TunnelProperties{
		Name: rstream.StringPtr(name),
	}
	props.AllowCrossRegionRouting, err = e2eenv.AllowCrossRegionRouting()
	if err != nil {
		return fmt.Errorf("cross-region routing: %w", err)
	}
	switch upstream {
	case "h2c":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP2)
	case "h3":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
	default:
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	defer tunnel.Close()
	fwdAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("forwarding address: %w", err)
	}
	fmt.Printf("READY %s\n", fwdAddr)
	switch upstream {
	case "h2c":
		bs, ok := tunnel.(rstream.BytestreamTunnel)
		if !ok {
			return fmt.Errorf("tunnel is not BytestreamTunnel")
		}
		return runH2C(ctx, bs)
	case "h3":
		dg, ok := tunnel.(rstream.DatagramTunnel)
		if !ok {
			return fmt.Errorf("tunnel is not DatagramTunnel")
		}
		return runH3(ctx, dg)
	default:
		bs, ok := tunnel.(rstream.BytestreamTunnel)
		if !ok {
			return fmt.Errorf("tunnel is not BytestreamTunnel")
		}
		return runH1(ctx, bs)
	}
}

func main() {
	upstream := flag.String("upstream", "h1", "upstream protocol: h1, h2c, h3")
	name := flag.String("name", "", "tunnel name (default: ws-matrix-<upstream>)")
	flag.Parse()
	if *name == "" {
		*name = "ws-matrix-" + *upstream
	}
	if *upstream == "h2c" && !strings.Contains(os.Getenv("GODEBUG"), "http2xconnect=1") {
		reexec("http2xconnect=1")
		return
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	if err := run(ctx, client, *upstream, *name); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

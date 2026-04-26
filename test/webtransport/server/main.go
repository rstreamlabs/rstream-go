// See LICENSE file in the project root for license information.

// http-wt-matrix-server is the server side of an end-to-end WebTransport coverage
// matrix. It exposes /webtransport and dispatches each incoming session on the
// `?case=...` query parameter — the test client opens one session per case so
// every case exercises a fresh relay through the engine reverse proxy.
//
// Cases:
//   - bidi-echo       : client-opened bidi stream, byte echo
//   - bidi-large      : same as echo, sized up to prove we traverse many
//     DATA frames without framing regressions
//   - bidi-multi      : N concurrent client-opened bidi streams, all echoed
//   - bidi-halfclose  : client Closes write, server reads to EOF then replies
//     with "eof-seen:<N>" on the same stream
//   - uni-c2s         : client-opened uni stream, server reports
//     "uni-received:len=<N>:sha=<hex>" on a server-opened bidi
//   - uni-s2c         : server-opened uni stream carrying a well-known payload
//   - datagram        : N datagrams, echoed back preserving ordering on the
//     best-effort path (unreliable; matrix tolerates loss)
//   - server-bidi     : server-opened bidi stream, client echoes
//   - close-code      : server CloseWithError(code, reason) — verifies the
//     session close propagates through the relay
//   - combo           : bidi + uni + datagrams on the same session (stress
//     the six goroutines of the reverse proxy relay)

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, NextProtos: []string{"h3"}}, nil
}

func parseN(q url.Values, def int) int {
	if raw := q.Get("n"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
	}
	return def
}

// sha256hex returns a short content fingerprint so the client can verify exact
// payload equality across the relay without shipping the full bytes back.
func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- per-case handlers (server side) -------------------------------------

func caseBidiEcho(ctx context.Context, sess *webtransport.Session) {
	s, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	defer s.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			if _, werr := s.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// caseBidiHalfclose reads until the client Closes its write side (half-close),
// then replies once with "eof-seen:<N>" to confirm end-of-stream semantics are
// preserved across the relay. The sum-of-bytes answer lets the client assert
// that everything it wrote before Close() arrived.
func caseBidiHalfclose(ctx context.Context, sess *webtransport.Session) {
	s, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	defer s.Close()
	data, err := io.ReadAll(s)
	if err != nil {
		return
	}
	fmt.Fprintf(s, "eof-seen:%d", len(data))
}

func caseBidiMulti(ctx context.Context, sess *webtransport.Session, n int) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		s, err := sess.AcceptStream(ctx)
		if err != nil {
			break
		}
		wg.Add(1)
		go func(s *webtransport.Stream) {
			defer wg.Done()
			defer s.Close()
			_, _ = io.Copy(s, s)
		}(s)
	}
	wg.Wait()
}

func caseUniC2S(ctx context.Context, sess *webtransport.Session) {
	rs, err := sess.AcceptUniStream(ctx)
	if err != nil {
		return
	}
	data, err := io.ReadAll(rs)
	if err != nil {
		return
	}
	// Report back on a server-opened bidi so the client can collect the result
	// AND we exercise server-initiated streams at the same time.
	s, err := sess.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	defer s.Close()
	fmt.Fprintf(s, "uni-received:len=%d:sha=%s", len(data), sha256hex(data))
}

// caseUniS2C opens a server→client unidirectional stream carrying a fixed
// payload. The client acknowledges on a separately-opened control bidi.
func caseUniS2C(ctx context.Context, sess *webtransport.Session) {
	ctrl, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	defer ctrl.Close()
	// Synchronize: wait for the client "go" so we don't race session setup.
	buf := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, buf); err != nil {
		return
	}
	us, err := sess.OpenUniStreamSync(ctx)
	if err != nil {
		return
	}
	if _, err := us.Write([]byte("server-uni-payload")); err != nil {
		_ = us.Close()
		return
	}
	_ = us.Close()
	_, _ = ctrl.Write([]byte("sent"))
}

func caseDatagram(ctx context.Context, sess *webtransport.Session, n int) {
	// We do NOT require exact N here: datagrams are unreliable and the engine's
	// relay may drop under pressure. We echo whatever arrives until the client
	// closes the session. The client tolerates partial loss (see matrix client).
	for i := 0; i < n*4; i++ {
		payload, err := sess.ReceiveDatagram(ctx)
		if err != nil {
			return
		}
		if err := sess.SendDatagram(payload); err != nil {
			return
		}
	}
}

// caseServerBidi exercises the reverse of the usual direction: the SERVER
// opens the bidi stream. The relay must accept the upstream-opened stream
// and proxy it to the downstream peer. The client writes "ack" back.
func caseServerBidi(ctx context.Context, sess *webtransport.Session) {
	ctrl, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	defer ctrl.Close()
	buf := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, buf); err != nil {
		return
	}
	s, err := sess.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	if _, err := s.Write([]byte("hello-from-server")); err != nil {
		_ = s.Close()
		return
	}
	_ = s.Close()
	// Read the client echo back on the same stream.
	echo, err := io.ReadAll(s)
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	fmt.Fprintf(ctrl, "got:%s", string(echo))
}

// caseCloseCode synchronizes with the client on the control stream (go → ack →
// done) so buffered stream data has been consumed by the client BEFORE the
// session is torn down — without the "done" round-trip, the server's
// CloseWithError races the in-flight ack bytes and the client observes a
// stream cancel (code 0) instead of reading "ack". After the round-trip the
// server tears the session down with the requested code + reason.
func caseCloseCode(ctx context.Context, sess *webtransport.Session, q url.Values) {
	ctrl, err := sess.AcceptStream(ctx)
	if err != nil {
		return
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, buf); err != nil {
		_ = ctrl.Close()
		return
	}
	if _, err := ctrl.Write([]byte("ack")); err != nil {
		_ = ctrl.Close()
		return
	}
	// Wait for the client's "done" so we know it has already read "ack".
	done := make([]byte, 4)
	if _, err := io.ReadFull(ctrl, done); err != nil {
		_ = ctrl.Close()
		return
	}
	_ = ctrl.Close()
	code := uint32(parseN(q, 42))
	reason := q.Get("reason")
	if reason == "" {
		reason = "matrix-close"
	}
	_ = sess.CloseWithError(webtransport.SessionErrorCode(code), reason)
}

// caseCombo multiplexes bidi echo, uni drain and datagram echo on a single
// session to exercise the six relay goroutines under concurrent load.
func caseCombo(ctx context.Context, sess *webtransport.Session, n int) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for {
			s, err := sess.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func(s *webtransport.Stream) {
				defer s.Close()
				_, _ = io.Copy(s, s)
			}(s)
		}
	}()
	go func() {
		defer wg.Done()
		for {
			rs, err := sess.AcceptUniStream(ctx)
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, rs) }()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n*8; i++ {
			payload, err := sess.ReceiveDatagram(ctx)
			if err != nil {
				return
			}
			if err := sess.SendDatagram(payload); err != nil {
				return
			}
		}
	}()
	<-sess.Context().Done()
	wg.Wait()
}

func dispatch(ctx context.Context, sess *webtransport.Session, q url.Values) {
	name := strings.TrimSpace(q.Get("case"))
	switch name {
	case "bidi-echo", "bidi-large":
		caseBidiEcho(ctx, sess)
	case "bidi-halfclose":
		caseBidiHalfclose(ctx, sess)
	case "bidi-multi":
		caseBidiMulti(ctx, sess, parseN(q, 4))
	case "uni-c2s":
		caseUniC2S(ctx, sess)
	case "uni-s2c":
		caseUniS2C(ctx, sess)
	case "datagram":
		caseDatagram(ctx, sess, parseN(q, 16))
	case "server-bidi":
		caseServerBidi(ctx, sess)
	case "close-code":
		caseCloseCode(ctx, sess, q)
	case "combo":
		caseCombo(ctx, sess, parseN(q, 4))
	default:
		_ = sess.CloseWithError(webtransport.SessionErrorCode(1), "unknown case: "+name)
	}
}

func run(ctx context.Context, client *rstream.Client, publish bool, publishedProtocol string, tokenAuth bool) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("wt-matrix"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if tokenAuth {
		tunnelProps.TokenAuth = rstream.BoolPtr(true)
	}
	if publish {
		switch strings.ToLower(strings.TrimSpace(publishedProtocol)) {
		case "http":
			tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
			tunnelProps.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
		case "quic":
			tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolQUIC)
		default:
			return fmt.Errorf("invalid published protocol %q (expected http or quic)", publishedProtocol)
		}
	}
	tunnel, err := ctrl.CreateTunnel(ctx, tunnelProps)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	forwardingAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	fmt.Printf("READY %s\n", forwardingAddr)
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       tlsCfg,
			EnableDatagrams: true,
		},
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtransport", func(w http.ResponseWriter, r *http.Request) {
		sess, err := server.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		// The request context is cancelled as soon as Upgrade returns (stdlib
		// behaviour), so we derive a handler context from the session instead.
		go dispatch(sess.Context(), sess, r.URL.Query())
	})
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(rstream.PacketConnFromPacketListener(packetListener)) }()
	select {
	case <-ctx.Done():
		log.Println("Shutting down WebTransport matrix server...")
		return server.Close()
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
	publishedProtocol := flag.String("published-protocol", "quic", "published edge protocol when -publish=true (http or quic)")
	tokenAuth := flag.Bool("token-auth", false, "require token auth on the published tunnel")
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
	if err := run(ctx, client, *publish, *publishedProtocol, *tokenAuth); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

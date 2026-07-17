// See LICENSE file in the project root for license information.

// connect-runtime-server publishes an HTTP tunnel and serves a real forward
// proxy CONNECT handler behind rstream. It is used by runtime e2e tests to
// verify TCP CONNECT over HTTP/1.1, HTTP/2 cleartext, and HTTP/3 upstreams.

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const tunneledQUICInitialPacketSize = 1200

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

func validateConnectTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if strings.ContainsAny(target, " \t\r\n/") {
		return "", fmt.Errorf("invalid CONNECT target %q", target)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", errors.New("CONNECT target host is empty")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return "", fmt.Errorf("CONNECT target port is invalid: %q", port)
	}
	return net.JoinHostPort(host, strconv.Itoa(portNum)), nil
}

func newProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		target, err := validateConnectTarget(r.Host)
		if err != nil {
			http.Error(w, "Bad CONNECT target", http.StatusBadRequest)
			return
		}
		upstream, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", target)
		if err != nil {
			http.Error(w, "Upstream unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		if r.ProtoMajor <= 1 {
			serveH1Connect(w, upstream)
			return
		}
		serveStreamConnect(w, r, upstream)
	})
}

func serveH1Connect(w http.ResponseWriter, upstream net.Conn) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijack unavailable", http.StatusInternalServerError)
		return
	}
	downstream, bufrw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer downstream.Close()
	if _, err := fmt.Fprint(bufrw, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := bufrw.Flush(); err != nil {
		return
	}
	relay(&bufferedConn{Conn: downstream, reader: bufrw.Reader}, upstream)
}

func serveStreamConnect(w http.ResponseWriter, r *http.Request, upstream net.Conn) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Flush unavailable", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	relay(&httpStreamConn{reader: r.Body, writer: w, flusher: flusher}, upstream)
}

func relay(left, right io.ReadWriteCloser) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = left.Close()
			_ = right.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer func() { wg.Done(); closeBoth() }()
		_, _ = io.Copy(right, left)
	}()
	go func() {
		defer func() { wg.Done(); closeBoth() }()
		_, _ = io.Copy(left, right)
	}()
	wg.Wait()
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}

type httpStreamConn struct {
	reader  io.ReadCloser
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (c *httpStreamConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *httpStreamConn) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if n > 0 {
		c.flusher.Flush()
	}
	return n, err
}

func (c *httpStreamConn) Close() error { return c.reader.Close() }

func runH1(ctx context.Context, tunnel rstream.BytestreamTunnel) error {
	srv := &http.Server{Handler: newProxyHandler()}
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
	srv := &http.Server{Handler: h2c.NewHandler(newProxyHandler(), &http2.Server{})}
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

func runH3(ctx context.Context, tunnel rstream.DatagramTunnel) error {
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("TLS config: %w", err)
	}
	srv := &http3.Server{
		TLSConfig: tlsCfg,
		Handler:   newProxyHandler(),
		QUICConfig: &quic.Config{
			InitialPacketSize: tunneledQUICInitialPacketSize,
		},
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
		Name:     rstream.StringPtr(name),
		Publish:  rstream.BoolPtr(true),
		Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP),
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
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP1_1)
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
			return errors.New("tunnel is not BytestreamTunnel")
		}
		return runH2C(ctx, bs)
	case "h3":
		dg, ok := tunnel.(rstream.DatagramTunnel)
		if !ok {
			return errors.New("tunnel is not DatagramTunnel")
		}
		return runH3(ctx, dg)
	default:
		bs, ok := tunnel.(rstream.BytestreamTunnel)
		if !ok {
			return errors.New("tunnel is not BytestreamTunnel")
		}
		return runH1(ctx, bs)
	}
}

func main() {
	upstream := flag.String("upstream", "h1", "upstream protocol: h1, h2c, h3")
	name := flag.String("name", "connect-runtime", "tunnel name")
	flag.Parse()
	if *upstream == "h3" {
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
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

// See LICENSE file in the project root for license information.

// stream-matrix-server is the server side of the end-to-end stream coverage
// matrix. It creates a BytestreamTunnel and runs an echo server so the
// client can verify raw byte relay through the engine.
//
// Variants: plain (raw bytes, unpublished), tls (TLS, both modes).
// With --publish the tunnel is registered on the engine's TLS listener
// (Protocol: TLS); without it the SDK dialer path is used and the server
// wraps the tunnel with tls.NewListener for application-level TLS.

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"syscall"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func generateTLSConfig(tlsALPN string) (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	nextProtos := []string{"http/1.1"}
	if tlsALPN != "" {
		nextProtos = []string{tlsALPN}
	}
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, NextProtos: nextProtos}, nil
}

func echoConn(conn net.Conn, expectedALPN string) {
	defer conn.Close()
	if expectedALPN != "" {
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			log.Printf("expected TLS connection with ALPN %q, got %T", expectedALPN, conn)
			return
		}
		if err := tlsConn.Handshake(); err != nil {
			log.Printf("tls handshake error: %v", err)
			return
		}
		if got := tlsConn.ConnectionState().NegotiatedProtocol; got != expectedALPN {
			log.Printf("unexpected ALPN: got %q, want %q", got, expectedALPN)
			return
		}
	}
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if _, werr := conn.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("read error: %v", err)
			}
			return
		}
	}
}

func serveListener(ctx context.Context, l net.Listener, expectedALPN string) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go echoConn(conn, expectedALPN)
	}
}

func run(ctx context.Context, client *rstream.Client, variant, name string, publish bool, hostname, tlsALPN, tlsMode string, upstreamTLS bool) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ctrl.Close()
	props := rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
		Publish: rstream.BoolPtr(publish),
	}
	if hostname != "" {
		props.Hostname = rstream.StringPtr(hostname)
	}
	if variant == "tls" && publish {
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolTLS)
		if tlsMode != "" {
			props.TLSMode = rstream.TLSModePtr(rstream.TLSMode(tlsMode))
		}
		if tlsALPN != "" {
			props.TLSALPNs = []string{tlsALPN}
		}
		if upstreamTLS {
			props.UpstreamTLS = rstream.BoolPtr(true)
		}
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
	bs, ok := tunnel.(rstream.BytestreamTunnel)
	if !ok {
		return fmt.Errorf("tunnel is not BytestreamTunnel")
	}
	if variant == "tls" && (!publish || upstreamTLS || tlsMode == string(rstream.TLSModePassthrough)) {
		tlsCfg, err := generateTLSConfig(tlsALPN)
		if err != nil {
			return fmt.Errorf("TLS config: %w", err)
		}
		return serveListener(ctx, tls.NewListener(bs, tlsCfg), tlsALPN)
	}
	return serveListener(ctx, bs, "")
}

func main() {
	variant := flag.String("variant", "plain", "variant: plain, tls")
	publish := flag.Bool("publish", false, "publish the tunnel on the engine's TLS listener")
	hostname := flag.String("host", "", "requested tunnel hostname")
	tlsMode := flag.String("tls-mode", "", "TLS mode for published TLS tunnels (terminated, passthrough)")
	tlsALPN := flag.String("tls-alpn", "", "custom ALPN for published TLS tunnels")
	upstreamTLS := flag.Bool("upstream-tls", false, "connect from the edge to this server with upstream TLS")
	name := flag.String("name", "", "tunnel name (default: stream-matrix-<variant>[-pub])")
	flag.Parse()
	if *name == "" {
		*name = "stream-matrix-" + *variant
		if *publish {
			*name += "-pub"
		}
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
	if err := run(ctx, client, *variant, *name, *publish, *hostname, *tlsALPN, *tlsMode, *upstreamTLS); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

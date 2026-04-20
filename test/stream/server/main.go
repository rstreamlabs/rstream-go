// See LICENSE file in the project root for license information.

// stream-matrix-server is the server side of the end-to-end stream coverage
// matrix. It creates a BytestreamTunnel and runs an echo server so the client
// can verify raw byte relay and TLS pass-through through the engine.
//
// Variants: plain (raw bytes), tls (server terminates TLS itself).

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
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, NextProtos: []string{"http/1.1"}}, nil
}

func echoConn(conn net.Conn) {
	defer conn.Close()
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

func serveListener(ctx context.Context, l net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go echoConn(conn)
	}
}

func run(ctx context.Context, client *rstream.Client, variant, name string) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ctrl.Close()
	props := rstream.TunnelProperties{
		Name: rstream.StringPtr(name),
		Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
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
	bs, ok := tunnel.(rstream.BytestreamTunnel)
	if !ok {
		return fmt.Errorf("tunnel is not BytestreamTunnel")
	}
	fmt.Printf("READY %s\n", fwdAddr)
	switch variant {
	case "tls":
		tlsCfg, err := generateTLSConfig()
		if err != nil {
			return fmt.Errorf("TLS config: %w", err)
		}
		return serveListener(ctx, tls.NewListener(bs, tlsCfg))
	default:
		return serveListener(ctx, bs)
	}
}

func main() {
	variant := flag.String("variant", "plain", "variant: plain, tls")
	name := flag.String("name", "", "tunnel name (default: stream-matrix-<variant>)")
	flag.Parse()
	if *name == "" {
		*name = "stream-matrix-" + *variant
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
	if err := run(ctx, client, *variant, *name); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

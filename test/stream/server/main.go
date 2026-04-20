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

func generateTLSConfig() (*tls.Config, error) {
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

func run(ctx context.Context, client *rstream.Client, variant, name string, publish bool) error {
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
	if variant == "tls" && publish {
		// Published mode: engine's TLS listener handles the TLS handshake;
		// the server receives plain bytes over the tunnel.
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolTLS)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	defer tunnel.Close()
	tprops, err := tunnel.Properties()
	if err != nil {
		return fmt.Errorf("tunnel properties: %w", err)
	}
	// Published tunnels: Host is the clean edge address (no annotation).
	// Unpublished tunnels: Host is nil; print the tunnel name as a ready signal.
	if tprops.Host != nil {
		fmt.Printf("READY %s\n", *tprops.Host)
	} else if tprops.Name != nil {
		fmt.Printf("READY rstrm://%s\n", *tprops.Name)
	} else {
		fmt.Printf("READY\n")
	}
	bs, ok := tunnel.(rstream.BytestreamTunnel)
	if !ok {
		return fmt.Errorf("tunnel is not BytestreamTunnel")
	}
	if variant == "tls" && !publish {
		// Unpublished mode: TLS is handled at the application layer;
		// the engine relays raw bytes and the server terminates TLS itself.
		tlsCfg, err := generateTLSConfig()
		if err != nil {
			return fmt.Errorf("TLS config: %w", err)
		}
		return serveListener(ctx, tls.NewListener(bs, tlsCfg))
	}
	return serveListener(ctx, bs)
}

func main() {
	variant := flag.String("variant", "plain", "variant: plain, tls")
	publish := flag.Bool("publish", false, "publish the tunnel on the engine's TLS listener")
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
	if err := run(ctx, client, *variant, *name, *publish); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

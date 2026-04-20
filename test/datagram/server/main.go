// See LICENSE file in the project root for license information.

// datagram-matrix-server is the server side of the end-to-end datagram coverage
// matrix. It creates a DatagramTunnel and echoes incoming packets so the client
// can verify relay through the engine.
//
// Variants: dtls, quic.
// With --publish the tunnel is registered on the engine's DTLS or QUIC listener
// with the matching Protocol; without it the SDK dialer path is used.

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

	"github.com/quic-go/quic-go"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const quicEchoALPN = "rstream-datagram-echo"

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
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{quicEchoALPN},
	}, nil
}

func handleDTLSConn(conn net.PacketConn) {
	defer conn.Close()
	buf := make([]byte, 2048)
	for {
		n, raddr, err := conn.ReadFrom(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("dtls: read error: %v", err)
			}
			return
		}
		if _, err := conn.WriteTo(buf[:n], raddr); err != nil {
			log.Printf("dtls: write error to %s: %v", raddr, err)
			return
		}
	}
}

func handleQUICStream(conn *quic.Conn, stream *quic.Stream) {
	defer stream.Close()
	buf := make([]byte, 2048)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := stream.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("quic: stream read error: %v", err)
			}
			return
		}
	}
}

func handleQUICDatagrams(conn *quic.Conn) {
	for {
		payload, err := conn.ReceiveDatagram(context.Background())
		if err != nil {
			return
		}
		if err := conn.SendDatagram(payload); err != nil {
			return
		}
	}
}

func handleQUICConn(conn *quic.Conn) {
	defer conn.CloseWithError(0, "server done")
	go handleQUICDatagrams(conn)
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		go handleQUICStream(conn, stream)
	}
}

func createTunnel(ctx context.Context, client *rstream.Client, variant, name string, publish bool) (rstream.Tunnel, error) {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	props := rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		switch variant {
		case "dtls":
			props.Protocol = rstream.ProtocolPtr(rstream.ProtocolDTLS)
		case "quic":
			props.Protocol = rstream.ProtocolPtr(rstream.ProtocolQUIC)
		}
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("create tunnel: %w", err)
	}
	go func() {
		<-ctx.Done()
		tunnel.Close()
		ctrl.Close()
	}()
	return tunnel, nil
}

// tunnelReady returns the string to print after "READY ".
// For published tunnels it is the clean edge address (no annotation).
// For unpublished tunnels it is the tunnel name prefixed with "rstrm://".
func tunnelReady(tunnel rstream.Tunnel) (string, error) {
	props, err := tunnel.Properties()
	if err != nil {
		return "", fmt.Errorf("tunnel properties: %w", err)
	}
	if props.Host != nil {
		return *props.Host, nil
	}
	if props.Name != nil {
		return "rstrm://" + *props.Name, nil
	}
	return "", fmt.Errorf("tunnel has neither host nor name")
}

func runDTLS(ctx context.Context, client *rstream.Client, name string, publish bool) error {
	tunnel, err := createTunnel(ctx, client, "dtls", name, publish)
	if err != nil {
		return err
	}
	defer tunnel.Close()
	host, err := tunnelReady(tunnel)
	if err != nil {
		return err
	}
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	fmt.Printf("READY %s\n", host)
	for {
		conn, _, err := packetListener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go handleDTLSConn(conn)
	}
}

func runQUIC(ctx context.Context, client *rstream.Client, name string, publish bool) error {
	tunnel, err := createTunnel(ctx, client, "quic", name, publish)
	if err != nil {
		return err
	}
	defer tunnel.Close()
	host, err := tunnelReady(tunnel)
	if err != nil {
		return err
	}
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("tls config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	transport := quic.Transport{Conn: rstream.PacketConnFromPacketListener(packetListener)}
	listener, err := transport.Listen(tlsCfg, &quic.Config{EnableDatagrams: true})
	if err != nil {
		return fmt.Errorf("quic listen: %w", err)
	}
	defer listener.Close()
	fmt.Printf("READY %s\n", host)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return fmt.Errorf("quic accept: %w", err)
		}
		go handleQUICConn(conn)
	}
}

func main() {
	variant := flag.String("variant", "dtls", "variant: dtls, quic")
	publish := flag.Bool("publish", false, "publish the tunnel on the engine's DTLS or QUIC listener")
	name := flag.String("name", "", "tunnel name (default: datagram-matrix-<variant>[-pub])")
	flag.Parse()
	if *name == "" {
		*name = "datagram-matrix-" + *variant
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
	switch *variant {
	case "dtls":
		if err := runDTLS(ctx, client, *name, *publish); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "quic":
		if err := runQUIC(ctx, client, *name, *publish); err != nil {
			log.Fatalf("server error: %v", err)
		}
	default:
		log.Fatalf("unknown variant %q: must be dtls or quic", *variant)
	}
}

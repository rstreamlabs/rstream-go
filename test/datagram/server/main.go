// See LICENSE file in the project root for license information.

// datagram-matrix-server is the server side of the end-to-end datagram coverage
// matrix. It creates a DatagramTunnel and echoes incoming packets, so the test
// client can verify DTLS and QUIC relay through the engine.
//
// Variants: dtls (pion/dtls v3 echo), quic (quic-go stream + datagram echo).

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
				log.Printf("read error from %s: %v", raddr, err)
			}
			return
		}
		log.Printf("dtls: received %d bytes from %s: %s", n, raddr, buf[:n])
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
			log.Printf("quic: stream received %d bytes from %s: %s", n, conn.RemoteAddr(), buf[:n])
			if _, werr := stream.Write(buf[:n]); werr != nil {
				log.Printf("quic: stream write error to %s: %v", conn.RemoteAddr(), werr)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("quic: stream read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
	}
}

func handleQUICDatagrams(conn *quic.Conn) {
	for {
		payload, err := conn.ReceiveDatagram(context.Background())
		if err != nil {
			log.Printf("quic: datagram read error from %s: %v", conn.RemoteAddr(), err)
			return
		}
		log.Printf("quic: datagram received %d bytes from %s: %s", len(payload), conn.RemoteAddr(), payload)
		if err := conn.SendDatagram(payload); err != nil {
			log.Printf("quic: datagram write error to %s: %v", conn.RemoteAddr(), err)
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
			log.Printf("quic: accept stream error: %v", err)
			return
		}
		go handleQUICStream(conn, stream)
	}
}

func runDTLS(ctx context.Context, client *rstream.Client, name string) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(ctx, rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(false),
	})
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	defer tunnel.Close()
	fwdAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("forwarding address: %w", err)
	}
	go func() {
		<-ctx.Done()
		tunnel.Close()
	}()
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	fmt.Printf("READY %s\n", fwdAddr)
	for {
		conn, _, err := packetListener.Accept()
		if err != nil {
			return fmt.Errorf("accept error: %w", err)
		}
		go handleDTLSConn(conn)
	}
}

func runQUIC(ctx context.Context, client *rstream.Client, name string) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(ctx, rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(false),
	})
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	defer tunnel.Close()
	fwdAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("forwarding address: %w", err)
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
	fmt.Printf("READY %s\n", fwdAddr)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return fmt.Errorf("quic accept error: %w", err)
		}
		go handleQUICConn(conn)
	}
}

func main() {
	variant := flag.String("variant", "dtls", "server variant: dtls or quic")
	name := flag.String("name", "", "tunnel name (default: datagram-matrix-<variant>)")
	flag.Parse()
	if *name == "" {
		*name = "datagram-matrix-" + *variant
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
		if err := runDTLS(ctx, client, *name); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "quic":
		if err := runQUIC(ctx, client, *name); err != nil {
			log.Fatalf("server error: %v", err)
		}
	default:
		log.Fatalf("unknown variant %q: must be dtls or quic", *variant)
	}
}

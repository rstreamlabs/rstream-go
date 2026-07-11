// See LICENSE file in the project root for license information.

// datagram-matrix-server is the server side of the end-to-end datagram coverage
// matrix. It creates a DatagramTunnel and echoes incoming packets so the client
// can verify relay through the engine.
//
// Variants: dtls, quic, sctp.
// With --publish the tunnel is registered on the engine's DTLS or QUIC listener
// with the matching Protocol; without it the SDK dialer path is used. SCTP uses
// pion/sctp on top of rstream datagrams, and uses the engine DTLS listener when
// published.

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

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
	"github.com/quic-go/quic-go"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const quicEchoALPN = "rstream-datagram-echo"
const tunneledQUICInitialPacketSize = 1200

func quicALPNs(tlsALPN string) []string {
	if tlsALPN != "" {
		return []string{tlsALPN}
	}
	return []string{quicEchoALPN}
}

func dtlsALPNs(tlsALPN string) []string {
	if tlsALPN != "" {
		return []string{tlsALPN}
	}
	return nil
}

func generateTLSConfig(tlsALPN string) (*tls.Config, error) {
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
		NextProtos:   quicALPNs(tlsALPN),
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

func handleQUICConn(conn *quic.Conn, expectedALPN string) {
	defer conn.CloseWithError(0, "server done")
	if expectedALPN != "" && conn.ConnectionState().TLS.NegotiatedProtocol != expectedALPN {
		log.Printf("quic: unexpected ALPN: got %q, want %q", conn.ConnectionState().TLS.NegotiatedProtocol, expectedALPN)
		return
	}
	go handleQUICDatagrams(conn)
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		go handleQUICStream(conn, stream)
	}
}

func handleSCTPStream(stream *sctp.Stream) {
	defer stream.Close()
	buf := make([]byte, 2048)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := stream.WriteSCTP(buf[:n], sctp.PayloadTypeWebRTCString); werr != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("sctp: stream read error: %v", err)
			}
			return
		}
	}
}

func handleSCTPConn(conn net.Conn) {
	defer conn.Close()
	assoc, err := sctp.Server(sctp.Config{NetConn: conn})
	if err != nil {
		log.Printf("sctp: association error: %v", err)
		return
	}
	defer assoc.Close()
	for {
		stream, err := assoc.AcceptStream()
		if err != nil {
			return
		}
		go handleSCTPStream(stream)
	}
}

func handleDTLSUpstreamConn(conn net.PacketConn, raddr net.Addr, certs []tls.Certificate, tlsALPN string) {
	dtlsConn, err := dtls.Server(conn, raddr, &dtls.Config{Certificates: certs, SupportedProtocols: dtlsALPNs(tlsALPN)})
	if err != nil {
		log.Printf("dtls upstream: handshake error: %v", err)
		conn.Close()
		return
	}
	if err := dtlsConn.Handshake(); err != nil {
		log.Printf("dtls upstream: handshake error: %v", err)
		dtlsConn.Close()
		return
	}
	if tlsALPN != "" {
		if state, ok := dtlsConn.ConnectionState(); !ok || state.NegotiatedProtocol != tlsALPN {
			log.Printf("dtls upstream: unexpected ALPN: got %q, want %q", state.NegotiatedProtocol, tlsALPN)
			dtlsConn.Close()
			return
		}
	}
	handleDTLSConn(rstream.PacketConnFromDTLSConn(dtlsConn))
}

func handleSCTPDTLSUpstreamConn(conn net.PacketConn, raddr net.Addr, certs []tls.Certificate, tlsALPN string) {
	dtlsConn, err := dtls.Server(conn, raddr, &dtls.Config{Certificates: certs, SupportedProtocols: dtlsALPNs(tlsALPN)})
	if err != nil {
		log.Printf("sctp dtls upstream: handshake error: %v", err)
		conn.Close()
		return
	}
	if err := dtlsConn.Handshake(); err != nil {
		log.Printf("sctp dtls upstream: handshake error: %v", err)
		dtlsConn.Close()
		return
	}
	if tlsALPN != "" {
		if state, ok := dtlsConn.ConnectionState(); !ok || state.NegotiatedProtocol != tlsALPN {
			log.Printf("sctp dtls upstream: unexpected ALPN: got %q, want %q", state.NegotiatedProtocol, tlsALPN)
			dtlsConn.Close()
			return
		}
	}
	handleSCTPConn(dtlsConn)
}

func createTunnel(ctx context.Context, client *rstream.Client, variant, name string, publish bool, hostname, tlsALPN string, upstreamTLS, guaranteedDelivery bool) (rstream.Tunnel, error) {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	props := rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if guaranteedDelivery {
		props.DatagramGuaranteedDelivery = rstream.BoolPtr(true)
	}
	if hostname != "" {
		props.Hostname = rstream.StringPtr(hostname)
	}
	if publish {
		switch variant {
		case "dtls":
			props.Protocol = rstream.ProtocolPtr(rstream.ProtocolDTLS)
			if upstreamTLS {
				props.UpstreamTLS = rstream.BoolPtr(true)
			}
		case "quic":
			props.Protocol = rstream.ProtocolPtr(rstream.ProtocolQUIC)
		case "sctp":
			props.Protocol = rstream.ProtocolPtr(rstream.ProtocolDTLS)
			if upstreamTLS {
				props.UpstreamTLS = rstream.BoolPtr(true)
			}
		}
		if tlsALPN != "" {
			props.TLSALPNs = []string{tlsALPN}
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

func tunnelReady(tunnel rstream.Tunnel) (string, error) {
	host, err := tunnel.ForwardingAddress()
	if err != nil {
		return "", fmt.Errorf("forwarding address: %w", err)
	}
	return host, nil
}

func runDTLS(ctx context.Context, client *rstream.Client, name string, publish bool, hostname, tlsALPN string, upstreamTLS, guaranteedDelivery bool) error {
	tunnel, err := createTunnel(ctx, client, "dtls", name, publish, hostname, tlsALPN, upstreamTLS, guaranteedDelivery)
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
	var certs []tls.Certificate
	if upstreamTLS {
		tlsCfg, err := generateTLSConfig("")
		if err != nil {
			return fmt.Errorf("tls config: %w", err)
		}
		certs = tlsCfg.Certificates
	}
	fmt.Printf("READY %s\n", host)
	for {
		conn, raddr, err := packetListener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		if upstreamTLS {
			go handleDTLSUpstreamConn(conn, raddr, certs, tlsALPN)
			continue
		}
		go handleDTLSConn(conn)
	}
}

func runQUIC(ctx context.Context, client *rstream.Client, name string, publish bool, hostname, tlsALPN string, guaranteedDelivery bool) error {
	tunnel, err := createTunnel(ctx, client, "quic", name, publish, hostname, tlsALPN, true, guaranteedDelivery)
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
	tlsCfg, err := generateTLSConfig(tlsALPN)
	if err != nil {
		return fmt.Errorf("tls config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	transport := quic.Transport{Conn: rstream.PacketConnFromPacketListener(packetListener)}
	quicConfig := &quic.Config{EnableDatagrams: true}
	if !publish {
		quicConfig.InitialPacketSize = tunneledQUICInitialPacketSize
	}
	listener, err := transport.Listen(tlsCfg, quicConfig)
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
		go handleQUICConn(conn, quicALPNs(tlsALPN)[0])
	}
}

func runSCTP(ctx context.Context, client *rstream.Client, name string, publish bool, hostname, tlsALPN string, upstreamTLS, guaranteedDelivery bool) error {
	tunnel, err := createTunnel(ctx, client, "sctp", name, publish, hostname, tlsALPN, upstreamTLS, guaranteedDelivery)
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
	var certs []tls.Certificate
	if upstreamTLS {
		tlsCfg, err := generateTLSConfig("")
		if err != nil {
			return fmt.Errorf("tls config: %w", err)
		}
		certs = tlsCfg.Certificates
	}
	fmt.Printf("READY %s\n", host)
	for {
		conn, raddr, err := packetListener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		if upstreamTLS {
			go handleSCTPDTLSUpstreamConn(conn, raddr, certs, tlsALPN)
			continue
		}
		go handleSCTPConn(rstream.ConnFromPacketConn(conn, raddr))
	}
}

func main() {
	variant := flag.String("variant", "dtls", "variant: dtls, quic, sctp")
	publish := flag.Bool("publish", false, "publish the tunnel on the engine's DTLS or QUIC listener")
	hostname := flag.String("host", "", "requested tunnel hostname")
	tlsALPN := flag.String("tls-alpn", "", "custom ALPN for published DTLS, QUIC, or SCTP tunnels")
	upstreamTLS := flag.Bool("upstream-tls", false, "connect from the edge to this server with upstream DTLS")
	guaranteedDelivery := flag.Bool("datagram-guaranteed-delivery", false, "require reliable delivery through the rstream tunnel")
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
		if err := runDTLS(ctx, client, *name, *publish, *hostname, *tlsALPN, *upstreamTLS, *guaranteedDelivery); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "quic":
		if err := runQUIC(ctx, client, *name, *publish, *hostname, *tlsALPN, *guaranteedDelivery); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "sctp":
		if err := runSCTP(ctx, client, *name, *publish, *hostname, *tlsALPN, *upstreamTLS, *guaranteedDelivery); err != nil {
			log.Fatalf("server error: %v", err)
		}
	default:
		log.Fatalf("unknown variant %q: must be dtls, quic, or sctp", *variant)
	}
}

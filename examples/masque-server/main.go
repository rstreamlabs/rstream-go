// See LICENSE file in the project root for license information.

// masque-server runs a MASQUE server behind an rstream DatagramTunnel and
// serves either CONNECT-UDP or CONNECT-IP. When published as HTTP/3, standard
// MASQUE clients can reach it through the rstream edge. In unpublished mode,
// rstream SDK clients can reach it through the internal datagram dialer.
//
// Run: go run . (internal only) or go run . -publish (published H3 endpoint)

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
	"log"
	"math/big"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	connectip "github.com/quic-go/connect-ip-go"
	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/yosida95/uritemplate/v3"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
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

func createTunnel(ctx context.Context, client *rstream.Client, name string, publish bool) (rstream.Tunnel, error) {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return nil, err
	}
	props := rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		props.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	go func() {
		<-ctx.Done()
		tunnel.Close()
		ctrl.Close()
	}()
	return tunnel, nil
}

func connectUDPTemplate(host string) (*uritemplate.Template, error) {
	return uritemplate.New("https://" + host + "/.well-known/masque/udp/{target_host}/{target_port}/")
}

func connectIPTemplate(host string) (*uritemplate.Template, error) {
	return uritemplate.New("https://" + host + "/connect-ip")
}

func writeParseError(w http.ResponseWriter, err error) {
	var udpErr *masque.ProxyRequestParseError
	if errors.As(err, &udpErr) {
		http.Error(w, udpErr.Error(), udpErr.HTTPStatus)
		return
	}
	var ipErr *connectip.RequestParseError
	if errors.As(err, &ipErr) {
		http.Error(w, ipErr.Error(), ipErr.HTTPStatus)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func handleConnectUDP(proxy *masque.Proxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tpl, err := connectUDPTemplate(r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req, err := masque.ParseProxyRequest(r, tpl)
		if err != nil {
			writeParseError(w, err)
			return
		}
		if err := proxy.Proxy(w, req); err != nil {
			log.Printf("CONNECT-UDP error: %v", err)
		}
	}
}

func handleConnectIP(proxy *connectip.Proxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tpl, err := connectIPTemplate(r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req, err := connectip.ParseRequest(r, tpl)
		if err != nil {
			writeParseError(w, err)
			return
		}
		conn, err := proxy.Proxy(w, req)
		if err != nil {
			log.Printf("CONNECT-IP setup error: %v", err)
			return
		}
		defer conn.Close()
		ctx := r.Context()
		clientPrefix := netip.MustParsePrefix("10.77.0.2/32")
		serverIP := netip.MustParseAddr("10.77.0.1")
		if err := conn.AssignAddresses(ctx, []netip.Prefix{clientPrefix}); err != nil {
			log.Printf("CONNECT-IP address assignment error: %v", err)
			return
		}
		if err := conn.AdvertiseRoute(ctx, []connectip.IPRoute{{StartIP: serverIP, EndIP: serverIP, IPProtocol: 1}}); err != nil {
			log.Printf("CONNECT-IP route advertisement error: %v", err)
			return
		}
		buf := make([]byte, 1500)
		for {
			n, err := conn.ReadPacket(buf)
			if err != nil {
				return
			}
			reply, err := swapIPv4Packet(buf[:n])
			if err != nil {
				log.Printf("CONNECT-IP dropping packet: %v", err)
				continue
			}
			if _, err := conn.WritePacket(reply); err != nil {
				log.Printf("CONNECT-IP write error: %v", err)
				return
			}
		}
	}
}

func swapIPv4Packet(packet []byte) ([]byte, error) {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return nil, fmt.Errorf("expected IPv4 packet, got %d bytes", len(packet))
	}
	out := append([]byte(nil), packet...)
	copy(out[12:16], packet[16:20])
	copy(out[16:20], packet[12:16])
	out[8] = 64
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], ipv4HeaderChecksum(out[:20]))
	return out, nil
}

func ipv4HeaderChecksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(header); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func run(ctx context.Context, client *rstream.Client, variant, name string, publish bool) error {
	tunnel, err := createTunnel(ctx, client, name, publish)
	if err != nil {
		return err
	}
	forwarding, err := tunnel.ForwardingAddress()
	if err != nil {
		return err
	}
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement PacketListener")
	}
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return err
	}
	udpProxy := &masque.Proxy{}
	defer udpProxy.Close()
	ipProxy := &connectip.Proxy{}
	mux := http.NewServeMux()
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "connect-udp":
		mux.HandleFunc("/.well-known/masque/udp/", handleConnectUDP(udpProxy))
	case "connect-ip":
		mux.HandleFunc("/connect-ip", handleConnectIP(ipProxy))
	default:
		return fmt.Errorf("invalid variant %q", variant)
	}
	server := &http3.Server{Handler: mux, TLSConfig: tlsCfg, EnableDatagrams: true}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(rstream.PacketConnFromPacketListener(packetListener)) }()
	fmt.Printf("Server listening on %s\n", forwarding)
	select {
	case <-ctx.Done():
		_ = server.Close()
		return nil
	case err := <-errCh:
		return err
	}
}

func main() {
	variant := flag.String("variant", "connect-udp", "connect-udp or connect-ip")
	name := flag.String("name", "masque-example", "rstream tunnel name")
	publish := flag.Bool("publish", false, "publish the tunnel")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, client, *variant, *name, *publish); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

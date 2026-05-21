// See LICENSE file in the project root for license information.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	masque "github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"
)

const (
	socksVersion = byte(0x05)
	socksNoAuth  = byte(0x00)
	socksConnect = byte(0x01)
	socksUDP     = byte(0x03)
	socksIPv4    = byte(0x01)
	socksDomain  = byte(0x03)
	socksIPv6    = byte(0x04)
)

func main() {
	mode := flag.String("mode", "http", "proxy mode: http, socks5, masque")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	publicHost := flag.String("public-host", "masque.c.localhost.rstream.io", "public hostname advertised by MASQUE mode")
	certFile := flag.String("cert", "", "TLS certificate file for MASQUE mode")
	keyFile := flag.String("key", "", "TLS private key file for MASQUE mode")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var err error
	switch *mode {
	case "http":
		err = runHTTPProxy(ctx, *addr)
	case "socks5":
		err = runSOCKS5Proxy(ctx, *addr)
	case "masque":
		err = runMASQUEProxy(ctx, *addr, *publicHost, *certFile, *keyFile)
	default:
		err = fmt.Errorf("unsupported proxy mode %q", *mode)
	}
	if err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func runHTTPProxy(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	fmt.Printf("READY http://%s\n", listener.Addr().String())
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go handleHTTPProxyConn(conn)
	}
}

func handleHTTPProxyConn(conn net.Conn) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = conn.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
		return
	}
	upstream, err := net.Dial("tcp", req.Host)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer upstream.Close()
	_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	relay(conn, upstream)
}

func runSOCKS5Proxy(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	fmt.Printf("READY socks5://%s\n", listener.Addr().String())
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go handleSOCKS5Conn(conn)
	}
}

func handleSOCKS5Conn(conn net.Conn) {
	defer conn.Close()
	if err := socksNegotiate(conn); err != nil {
		return
	}
	command, target, err := socksReadRequest(conn)
	if err != nil {
		return
	}
	switch command {
	case socksConnect:
		handleSOCKSConnect(conn, target)
	case socksUDP:
		handleSOCKSUDP(conn)
	default:
		_ = socksWriteReply(conn, 0x07, "0.0.0.0:0")
	}
}

func socksNegotiate(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	_, err := conn.Write([]byte{socksVersion, socksNoAuth})
	return err
}

func socksReadRequest(conn net.Conn) (byte, string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, "", err
	}
	if header[0] != socksVersion {
		return 0, "", fmt.Errorf("unexpected SOCKS request version %d", header[0])
	}
	host, port, err := socksReadAddress(conn, header[3])
	if err != nil {
		return 0, "", err
	}
	return header[1], net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func socksReadAddress(reader io.Reader, atyp byte) (string, int, error) {
	var host string
	switch atyp {
	case socksIPv4:
		raw := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", 0, err
		}
		host = net.IP(raw).String()
	case socksIPv6:
		raw := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", 0, err
		}
		host = net.IP(raw).String()
	case socksDomain:
		size := []byte{0}
		if _, err := io.ReadFull(reader, size); err != nil {
			return "", 0, err
		}
		raw := make([]byte, int(size[0]))
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", 0, err
		}
		host = string(raw)
	default:
		return "", 0, fmt.Errorf("unsupported SOCKS address type 0x%02x", atyp)
	}
	portRaw := make([]byte, 2)
	if _, err := io.ReadFull(reader, portRaw); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(portRaw)), nil
}

func handleSOCKSConnect(conn net.Conn, target string) {
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_ = socksWriteReply(conn, 0x05, "0.0.0.0:0")
		return
	}
	defer upstream.Close()
	_ = socksWriteReply(conn, 0x00, upstream.LocalAddr().String())
	relay(conn, upstream)
}

func handleSOCKSUDP(conn net.Conn) {
	relayConn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		_ = socksWriteReply(conn, 0x01, "0.0.0.0:0")
		return
	}
	defer relayConn.Close()
	_ = socksWriteReply(conn, 0x00, net.JoinHostPort("0.0.0.0", strconv.Itoa(relayConn.LocalAddr().(*net.UDPAddr).Port)))
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		close(done)
		_ = relayConn.Close()
	}()
	relaySOCKSUDP(relayConn, done)
}

func relaySOCKSUDP(conn *net.UDPConn, done <-chan struct{}) {
	buf := make([]byte, 65535)
	var client *net.UDPAddr
	for {
		select {
		case <-done:
			return
		default:
		}
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if client == nil || addr.IP.Equal(client.IP) && addr.Port == client.Port {
			client = addr
			target, payload, err := parseSOCKSUDPDatagram(buf[:n])
			if err != nil {
				return
			}
			targetAddr, err := net.ResolveUDPAddr("udp", target)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(payload, targetAddr)
			continue
		}
		if client != nil {
			packet := buildSOCKSUDPDatagram(addr.String(), buf[:n])
			_, _ = conn.WriteToUDP(packet, client)
		}
	}
}

func parseSOCKSUDPDatagram(packet []byte) (string, []byte, error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return "", nil, fmt.Errorf("invalid SOCKS UDP header")
	}
	reader := bytes.NewReader(packet[4:])
	host, port, err := socksReadAddress(reader, packet[3])
	if err != nil {
		return "", nil, err
	}
	payloadOffset := 4 + len(packet[4:]) - reader.Len()
	return net.JoinHostPort(host, strconv.Itoa(port)), packet[payloadOffset:], nil
}

func buildSOCKSUDPDatagram(source string, payload []byte) []byte {
	host, portRaw, _ := net.SplitHostPort(source)
	port, _ := strconv.Atoi(portRaw)
	addr := socksAddressBytes(host, port)
	packet := []byte{0, 0, 0}
	packet = append(packet, addr...)
	packet = append(packet, payload...)
	return packet
}

func socksWriteReply(conn net.Conn, status byte, bound string) error {
	host, portRaw, err := net.SplitHostPort(bound)
	if err != nil {
		host = "0.0.0.0"
		portRaw = "0"
	}
	port, _ := strconv.Atoi(portRaw)
	reply := []byte{socksVersion, status, 0}
	reply = append(reply, socksAddressBytes(host, port)...)
	_, err = conn.Write(reply)
	return err
}

func socksAddressBytes(host string, port int) []byte {
	portRaw := make([]byte, 2)
	binary.BigEndian.PutUint16(portRaw, uint16(port))
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out := []byte{socksIPv4}
			out = append(out, v4...)
			return append(out, portRaw...)
		}
		out := []byte{socksIPv6}
		out = append(out, ip.To16()...)
		return append(out, portRaw...)
	}
	out := []byte{socksDomain, byte(len(host))}
	out = append(out, host...)
	return append(out, portRaw...)
}

func runMASQUEProxy(ctx context.Context, addr, publicHost, certFile, keyFile string) error {
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("MASQUE mode requires --cert and --key")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	udpConn, err := net.ListenUDP("udp", mustUDPAddr(addr))
	if err != nil {
		return err
	}
	defer udpConn.Close()
	_, port, err := net.SplitHostPort(udpConn.LocalAddr().String())
	if err != nil {
		return err
	}
	publicAddr := net.JoinHostPort(publicHost, port)
	template := uritemplate.MustNew("https://" + publicAddr + "/.well-known/masque/udp/{target_host}/{target_port}/")
	mux := http.NewServeMux()
	proxy := &masque.Proxy{}
	server := &http3.Server{
		TLSConfig:       &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{http3.NextProtoH3}},
		QUICConfig:      &quic.Config{EnableDatagrams: true, InitialPacketSize: 1452},
		EnableDatagrams: true,
		Handler:         mux,
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		req, err := masque.ParseRequest(r, template)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := proxy.Proxy(w, req); err != nil {
			log.Printf("MASQUE proxy error: %v", err)
		}
	})
	go func() {
		<-ctx.Done()
		_ = proxy.Close()
		_ = server.Close()
		_ = udpConn.Close()
	}()
	fmt.Printf("READY https://%s/.well-known/masque/udp/{target_host}/{target_port}/\n", publicAddr)
	return server.Serve(udpConn)
}

func mustUDPAddr(addr string) *net.UDPAddr {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return udpAddr
}

func relay(a net.Conn, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
}

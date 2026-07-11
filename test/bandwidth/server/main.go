// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func echoStream(conn net.Conn) {
	defer conn.Close()
	_, _ = io.Copy(conn, conn)
}

func echoPackets(conn net.PacketConn) {
	defer conn.Close()
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if _, err := conn.WriteTo(buf[:n], addr); err != nil {
			return
		}
	}
}

func run(ctx context.Context, tunnelType, name string, guaranteed bool) error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer control.Close()
	props := rstream.TunnelProperties{
		Name:    rstream.StringPtr(name),
		Publish: rstream.BoolPtr(false),
	}
	switch tunnelType {
	case "bytestream":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
	case "datagram":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
		if guaranteed {
			props.DatagramGuaranteedDelivery = rstream.BoolPtr(true)
		}
	default:
		return fmt.Errorf("unsupported tunnel type %q", tunnelType)
	}
	tunnel, err := control.CreateTunnel(ctx, props)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	defer tunnel.Close()
	go func() {
		<-ctx.Done()
		_ = tunnel.Close()
		_ = control.Close()
	}()
	fmt.Printf("READY %s\n", name)
	switch typed := tunnel.(type) {
	case rstream.BytestreamTunnel:
		for {
			conn, err := typed.Accept()
			if err != nil {
				return err
			}
			go echoStream(conn)
		}
	case rstream.PacketListener:
		for {
			conn, _, err := typed.Accept()
			if err != nil {
				return err
			}
			go echoPackets(conn)
		}
	default:
		return fmt.Errorf("unexpected tunnel implementation %T", tunnel)
	}
}

func main() {
	tunnelType := flag.String("type", "datagram", "tunnel type: bytestream or datagram")
	name := flag.String("name", "bandwidth-limit", "private tunnel name")
	guaranteed := flag.Bool("datagram-guaranteed-delivery", false, "carry datagrams over reliable streams")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *tunnelType, *name, *guaranteed); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

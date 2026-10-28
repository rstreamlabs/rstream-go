// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func exchange(ctx context.Context, client *rstream.Client, target string, index int, native bool) error {
	conn, err := client.PacketDial(ctx, rstream.Addr{IdOrName: target})
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, ok := rstream.PacketConnDatagramReceiveStats(conn); ok != native {
		return fmt.Errorf("native datagram path = %t, want %t", ok, native)
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	payload := []byte(fmt.Sprintf("self-dial-%03d", index))
	if _, err := conn.WriteTo(payload, &rstream.Addr{IdOrName: target}); err != nil {
		return err
	}
	data := make([]byte, 64)
	n, _, err := conn.ReadFrom(data)
	if err != nil {
		return err
	}
	if !bytes.Equal(data[:n], payload) {
		return fmt.Errorf("unexpected datagram reply: %q", data[:n])
	}
	return conn.Close()
}

func run(name string, guaranteed, shared, native bool) error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return err
	}
	defer control.Close()
	tunnel, err := control.CreateTunnel(ctx, rstream.TunnelProperties{Name: &name, Type: rstream.TunnelTypePtr(rstream.TunnelTypeDatagram), Publish: rstream.BoolPtr(false), DatagramGuaranteedDelivery: &guaranteed})
	if err != nil {
		return err
	}
	defer tunnel.Close()
	listener, ok := tunnel.(rstream.DatagramTunnel)
	if !ok {
		return fmt.Errorf("unexpected tunnel type %T", tunnel)
	}
	props, err := tunnel.Properties()
	if err != nil {
		return err
	}
	if props.ID == nil {
		return errors.New("missing tunnel identifier")
	}
	caller := client
	if shared {
		caller = &rstream.Client{EngineURL: client.EngineURL, Token: client.Token, NoToken: client.NoToken, TLSClientConfig: client.TLSClientConfig, Transport: client.Transport, ZeroRTT: rstream.BoolPtr(false)}
		defer caller.Close()
	}
	server := rstream.PacketConnFromPacketListenerWithUniquePeers(listener)
	serverDone := make(chan error, 1)
	go func() {
		data := make([]byte, 64)
		for {
			n, addr, err := server.ReadFrom(data)
			if err != nil {
				serverDone <- err
				return
			}
			if _, err = server.WriteTo(data[:n], addr); err != nil {
				serverDone <- err
				return
			}
		}
	}()
	defer func() { _ = server.Close(); <-serverDone }()
	for index, target := range []string{name, *props.ID} {
		if err := exchange(ctx, caller, target, index, native); err != nil {
			return fmt.Errorf("sequential exchange: %w", err)
		}
	}
	const count = 8
	results := make(chan error, count)
	var workers sync.WaitGroup
	for index := range count {
		workers.Add(1)
		go func() { defer workers.Done(); results <- exchange(ctx, caller, name, index+2, native) }()
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			return fmt.Errorf("concurrent exchange: %w", err)
		}
	}
	if err := exchange(ctx, caller, name, 99, native); err != nil {
		return fmt.Errorf("exchange after closing peers: %w", err)
	}
	return nil
}

func main() {
	name := flag.String("name", "datagram-self", "private tunnel name")
	guaranteed := flag.Bool("guaranteed", false, "require reliable datagrams")
	shared := flag.Bool("shared-transport", false, "use two Clients sharing the same transport")
	native := flag.Bool("native", true, "expect native QUIC datagrams")
	flag.Parse()
	if err := run(*name, *guaranteed, *shared, *native); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("PASS private datagram self-dial")
}

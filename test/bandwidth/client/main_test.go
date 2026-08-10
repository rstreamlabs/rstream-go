package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestExchangeBytestreamPipelinesRequests(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() { _ = server.Close() })
	deadline := time.Now().Add(time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	serverResult := make(chan error, 1)
	go func() {
		all := make([]byte, 64)
		if _, err := io.ReadFull(server, all); err != nil {
			serverResult <- err
			return
		}
		_, err := io.Copy(server, bytes.NewReader(all))
		serverResult <- err
	}()
	if err := exchangeBytestream(client, 32, 2); err != nil {
		t.Fatal(err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestExchangeBytestreamReadTimeoutJoinsWriter(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() { _ = server.Close() })
	if err := client.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := exchangeBytestream(client, 32, 2); err == nil {
		t.Fatal("expected exchange timeout")
	}
}

func TestExchangeDatagramsPipelinesAndAcceptsReordering(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	client, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(time.Second)
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	serverResult := make(chan error, 1)
	go func() {
		packets := make([][]byte, 2)
		var peer net.Addr
		for i := range packets {
			buf := make([]byte, 32)
			n, addr, readErr := server.ReadFrom(buf)
			if readErr != nil {
				serverResult <- readErr
				return
			}
			packets[i] = buf[:n]
			peer = addr
		}
		for i := len(packets) - 1; i >= 0; i-- {
			if _, writeErr := server.WriteTo(packets[i], peer); writeErr != nil {
				serverResult <- writeErr
				return
			}
		}
		serverResult <- nil
	}()
	if err := exchangeDatagrams(client, server.LocalAddr(), 32, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestExchangeDatagramsReadTimeoutJoinsWindowedWriter(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	client, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := exchangeDatagrams(client, server.LocalAddr(), 32, 4, 1); err == nil {
		t.Fatal("expected exchange timeout")
	}
}

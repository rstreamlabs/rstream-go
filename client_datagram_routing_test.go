// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/pb"
)

func TestClientPacketDialSelfUsesIndependentChannelID(t *testing.T) {
	dialer := newQueuedDatagramDialer(2)
	dialer.enqueue(serveControlChannelWithDatagramProxyConnection)
	client := newTestClientWithDatagramDialer(dialer)
	control, err := client.Connect(t.Context(), &Config{EnableHeartbeat: BoolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	tunnel, err := control.CreateTunnel(t.Context(), TunnelProperties{Name: StringPtr("dns"), Type: TunnelTypePtr(TunnelTypeDatagram), Publish: BoolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tunnel.Close(); err != nil {
			t.Error(err)
		}
		if err := control.Close(); err != nil {
			t.Error(err)
		}
	})
	owner, ownerAddr := acceptDatagram(t, tunnel.(DatagramTunnel))
	const streamID = "01020304-0000-0000-0000-000000000000"
	ownerID := mustDatagramChannelID(t, streamID)
	callerID := mustDatagramChannelID(t, "f10203040000000000000001")
	done := make(chan struct{})
	defer close(done)
	dialer.enqueue(func(conn net.Conn) error {
		req, err := expectStreamReq(bufio.NewReader(conn), "dns", "token")
		if err == nil && !req.IndependentDatagramChannel {
			return errors.New("independent channel was not requested")
		}
		if err != nil {
			return err
		}
		response := &pb.StreamRsp{Payload: &pb.StreamRsp_StreamId{StreamId: streamID}}
		response.DatagramChannelId = callerID[:]
		if err := writePbMessage(bufio.NewWriter(conn), &pb.Message{Payload: &pb.Message_StreamRsp{StreamRsp: response}}); err != nil {
			return err
		}
		<-done
		return nil
	})
	caller, err := client.PacketDial(t.Context(), Addr{IdOrName: "dns"})
	if err != nil {
		t.Fatalf("self PacketDial: %v", err)
	}
	defer caller.Close()
	for _, test := range []struct {
		conn    net.PacketConn
		addr    net.Addr
		id      datagramChannelID
		payload string
	}{
		{caller, &Addr{IdOrName: "dns"}, callerID, "to-owner"},
		{owner, ownerAddr, ownerID, "to-caller"},
	} {
		if err := test.conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := test.conn.WriteTo([]byte(test.payload), test.addr); err != nil {
			t.Fatal(err)
		}
		sent := dialer.sentDatagram(t)
		if string(sent[:datagramChannelIDSize]) != string(test.id[:]) {
			t.Fatalf("wrong routing identifier: %x", sent[:datagramChannelIDSize])
		}
		dialer.receiveDatagram(t, test.id, []byte(test.payload))
		data := make([]byte, 32)
		n, _, err := test.conn.ReadFrom(data)
		if err != nil || string(data[:n]) != test.payload {
			t.Fatalf("ReadFrom = %q, %v", data[:n], err)
		}
	}
	if err := caller.Close(); err != nil {
		t.Fatal(err)
	}
	dialer.mu.Lock()
	remaining := len(dialer.channels)
	remainingOwner := dialer.channels[ownerID]
	dialer.mu.Unlock()
	if remaining != 1 || remainingOwner != owner {
		t.Fatalf("caller close affected owner: %d channels", remaining)
	}
}

func TestClientPacketDialRejectsInvalidNegotiatedChannel(t *testing.T) {
	const streamID = "010203040000000000000001"
	legacy := mustDatagramChannelID(t, streamID)
	for name, id := range map[string][]byte{"short": make([]byte, 11), "long": make([]byte, 13), "zero": make([]byte, 12), "owner": legacy[:]} {
		t.Run(name, func(t *testing.T) {
			dialer := newQueuedDatagramDialer(1)
			dialer.enqueue(func(conn net.Conn) error {
				if _, err := expectStreamReq(bufio.NewReader(conn), "dns", "token"); err != nil {
					return err
				}
				response := &pb.StreamRsp{Payload: &pb.StreamRsp_StreamId{StreamId: streamID}, DatagramChannelId: id}
				if err := writePbMessage(bufio.NewWriter(conn), &pb.Message{Payload: &pb.Message_StreamRsp{StreamRsp: response}}); err != nil {
					return err
				}
				_, err := conn.Read(make([]byte, 1))
				if !errors.Is(err, io.EOF) {
					return fmt.Errorf("setup connection remained open: %v", err)
				}
				return nil
			})
			client := newTestClientWithDatagramDialer(dialer)
			packet, err := client.PacketDial(t.Context(), Addr{IdOrName: "dns"})
			if err == nil || packet != nil {
				t.Fatalf("PacketDial=%v, %v", packet, err)
			}
			dialer.wait(t, 1)
			dialer.mu.Lock()
			defer dialer.mu.Unlock()
			if len(dialer.channels) != 0 {
				t.Fatal("invalid response retained a channel")
			}
		})
	}
}

func TestClientPacketDialPendingHandshakeCancellation(t *testing.T) {
	for _, closeClient := range []bool{false, true} {
		t.Run(fmt.Sprint(closeClient), func(t *testing.T) {
			dialer := newQueuedDatagramDialer(1)
			requested := make(chan struct{})
			dialer.enqueue(func(conn net.Conn) error {
				if _, err := expectStreamReq(bufio.NewReader(conn), "dns", "token"); err != nil {
					return err
				}
				close(requested)
				_, err := conn.Read(make([]byte, 1))
				if !errors.Is(err, io.EOF) {
					return fmt.Errorf("setup connection remained open: %v", err)
				}
				return nil
			})
			client := newTestClientWithDatagramDialer(dialer)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				packet, err := client.PacketDial(ctx, Addr{IdOrName: "dns"})
				if packet != nil {
					_ = packet.Close()
				}
				result <- err
			}()
			select {
			case <-requested:
			case <-time.After(time.Second):
				t.Fatal("handshake not started")
			}
			if closeClient {
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("pending handshake did not stop")
			}
			dialer.wait(t, 1)
		})
	}
}

func TestClientPacketDialIndependentChannelClosesByStreamID(t *testing.T) {
	const streamID = "010203040000000000000001"
	id := mustDatagramChannelID(t, "f10203040000000000000001")
	dialer := newQueuedDatagramDialer(1)
	sendClose, done := make(chan struct{}), make(chan struct{})
	defer close(done)
	dialer.enqueue(func(conn net.Conn) error {
		if _, err := expectStreamReq(bufio.NewReader(conn), "dns", "token"); err != nil {
			return err
		}
		writer := bufio.NewWriter(conn)
		response := &pb.StreamRsp{Payload: &pb.StreamRsp_StreamId{StreamId: streamID}, DatagramChannelId: id[:]}
		if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_StreamRsp{StreamRsp: response}}); err != nil {
			return err
		}
		select {
		case <-sendClose:
		case <-done:
			return nil
		}
		if err := writePbMessage(writer, &pb.Message{Payload: &pb.Message_DatagramChannelClose{DatagramChannelClose: &pb.DatagramChannelClose{StreamId: streamID}}}); err != nil {
			return err
		}
		<-done
		return nil
	})
	client := newTestClientWithDatagramDialer(dialer)
	packet, err := client.PacketDial(t.Context(), Addr{IdOrName: "dns"})
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	if err := packet.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	close(sendClose)
	if _, _, err := packet.ReadFrom(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("channel was not closed by logical stream ID: %v", err)
	}
	if err := packet.Close(); err != nil {
		t.Fatal(err)
	}
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	if len(dialer.channels) != 0 {
		t.Fatal("closed channel retained its independent routing ID")
	}
}

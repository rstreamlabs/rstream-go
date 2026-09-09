// See LICENSE file in the project root for license information.

package rtc

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stalledUDP struct {
	*net.UDPConn
	stream  net.Conn
	active  atomic.Int32
	overlap atomic.Bool
	entered chan struct{}
	once    sync.Once
}

func (c *stalledUDP) Write(data []byte) (int, error) {
	if c.active.Add(1) != 1 {
		c.overlap.Store(true)
	}
	defer c.active.Add(-1)
	c.once.Do(func() { close(c.entered) })
	return c.stream.Write(data)
}

func (c *stalledUDP) WriteTo(data []byte, _ net.Addr) (int, error) {
	return c.Write(data)
}

func (c *stalledUDP) WriteToUDP(data []byte, _ *net.UDPAddr) (int, error) {
	return c.Write(data)
}

func (c *stalledUDP) WriteToUDPAddrPort(data []byte, _ netip.AddrPort) (int, error) {
	return c.Write(data)
}

func (c *stalledUDP) WriteMsgUDP(data, _ []byte, _ *net.UDPAddr) (int, int, error) {
	n, err := c.Write(data)
	return n, 0, err
}

func (c *stalledUDP) SetWriteDeadline(deadline time.Time) error {
	return c.stream.SetWriteDeadline(deadline)
}

func (c *stalledUDP) Close() error { return c.stream.Close() }

func stalledPacketSocket(t *testing.T) (*boundedUDP, *stalledUDP, net.Conn) {
	t.Helper()
	writer, reader := net.Pipe()
	t.Cleanup(func() { _ = writer.Close(); _ = reader.Close() })
	stalled := &stalledUDP{stream: writer, entered: make(chan struct{})}
	return newBoundedUDP(stalled), stalled, reader
}

func TestPacketWritesCannotStallNegotiation(t *testing.T) {
	for _, method := range []string{"Write", "WriteTo", "WriteToUDP", "WriteMsgUDP", "WriteToUDPAddrPort", "WriteToAddrPort"} {
		t.Run(method, func(t *testing.T) {
			connection, _, _ := stalledPacketSocket(t)
			done := make(chan error, 1)
			go func() {
				var err error
				data := []byte("packet")
				switch method {
				case "Write":
					_, err = connection.Write(data)
				case "WriteTo":
					_, err = connection.WriteTo(data, &net.UDPAddr{})
				case "WriteToUDP":
					_, err = connection.WriteToUDP(data, &net.UDPAddr{})
				case "WriteMsgUDP":
					_, _, err = connection.WriteMsgUDP(data, nil, &net.UDPAddr{})
				case "WriteToUDPAddrPort":
					_, err = connection.WriteToUDPAddrPort(data, netip.AddrPort{})
				case "WriteToAddrPort":
					_, err = connection.WriteToAddrPort(data, netip.AddrPort{})
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("stalled UDP write returned %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("stalled UDP write blocked the ICE task loop")
			}
		})
	}
}

func TestPacketConcurrentBackpressureRecoveryAndClose(t *testing.T) {
	connection, stalled, reader := stalledPacketSocket(t)
	const writers = 12
	done := make(chan error, writers)
	for range writers {
		go func() { _, err := connection.Write([]byte("packet")); done <- err }()
	}
	for range writers {
		select {
		case err := <-done:
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("stalled write returned %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent writes exceeded their shared queue/send budget")
		}
	}
	if stalled.overlap.Load() || stalled.active.Load() != 0 || len(connection.writes) != 0 {
		t.Fatal("write ownership overlapped or remained after timeout")
	}
	read := make(chan error, 1)
	go func() { _, err := io.ReadFull(reader, make([]byte, 6)); read <- err }()
	if n, err := connection.Write([]byte("packet")); err != nil || n != 6 {
		t.Fatalf("socket failed to recover after backpressure: %d %v", n, err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	connection, stalled, _ = stalledPacketSocket(t)
	go func() { _, err := connection.Write([]byte("packet")); done <- err }()
	<-stalled.entered
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("closed socket write returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("socket close waited for write ownership")
	}
}

type immediateUDP struct{ *net.UDPConn }

func (*immediateUDP) SetWriteDeadline(time.Time) error { return nil }
func (*immediateUDP) Write(data []byte) (int, error)   { return len(data), nil }

func TestHealthyPacketWritesDoNotAllocate(t *testing.T) {
	connection := newBoundedUDP(&immediateUDP{})
	data := []byte("packet")
	allocations := testing.AllocsPerRun(1000, func() {
		if _, err := connection.Write(data); err != nil {
			t.Fatal(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("healthy packet write allocated: %v", allocations)
	}
}

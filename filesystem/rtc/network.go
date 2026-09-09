// See LICENSE file in the project root for license information.

package rtc

import (
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/pion/transport/v4"
	"github.com/pion/transport/v4/stdnet"
)

const packetWriteTimeout = 100 * time.Millisecond

type peerNetwork struct{ *stdnet.Net }

func (n *peerNetwork) ListenUDP(network string, address *net.UDPAddr) (transport.UDPConn, error) {
	connection, err := net.ListenUDP(network, address)
	if err != nil {
		return nil, err
	}
	return newBoundedUDP(connection), nil
}

func (n *peerNetwork) DialUDP(network string, local, remote *net.UDPAddr) (transport.UDPConn, error) {
	connection, err := net.DialUDP(network, local, remote)
	if err != nil {
		return nil, err
	}
	return newBoundedUDP(connection), nil
}

func (n *peerNetwork) ListenPacket(network, address string) (net.PacketConn, error) {
	connection, err := n.Net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	if udp, ok := connection.(*net.UDPConn); ok {
		return newBoundedUDP(udp), nil
	}
	return connection, nil
}

type udpSocket interface {
	transport.UDPConn
	ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error)
	WriteToUDPAddrPort([]byte, netip.AddrPort) (int, error)
}

// One writer owns the socket deadline. Queueing and sending share a fixed budget;
// a congested candidate must not stall ICE's task loop or other candidate pairs.
// There is no background worker, and Close never waits for write ownership.
type boundedUDP struct {
	udpSocket
	writes chan struct{}
}

func newBoundedUDP(connection udpSocket) *boundedUDP {
	return &boundedUDP{udpSocket: connection, writes: make(chan struct{}, 1)}
}

func (c *boundedUDP) write(send func() (int, error)) (int, error) {
	deadline := time.Now().Add(packetWriteTimeout)
	select {
	case c.writes <- struct{}{}:
	default:
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case c.writes <- struct{}{}:
		case <-timer.C:
			return 0, os.ErrDeadlineExceeded
		}
	}
	defer func() { <-c.writes }()
	if err := c.udpSocket.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	return send()
}

func (c *boundedUDP) Write(data []byte) (int, error) {
	return c.write(func() (int, error) { return c.udpSocket.Write(data) })
}

func (c *boundedUDP) WriteTo(data []byte, address net.Addr) (int, error) {
	return c.write(func() (int, error) { return c.udpSocket.WriteTo(data, address) })
}

func (c *boundedUDP) WriteToUDP(data []byte, address *net.UDPAddr) (int, error) {
	return c.write(func() (int, error) { return c.udpSocket.WriteToUDP(data, address) })
}

func (c *boundedUDP) WriteMsgUDP(data, oob []byte, address *net.UDPAddr) (n, oobn int, err error) {
	n, err = c.write(func() (int, error) {
		var count int
		count, oobn, err = c.udpSocket.WriteMsgUDP(data, oob, address)
		return count, err
	})
	return n, oobn, err
}

func (c *boundedUDP) WriteToUDPAddrPort(data []byte, address netip.AddrPort) (int, error) {
	return c.write(func() (int, error) { return c.udpSocket.WriteToUDPAddrPort(data, address) })
}

func (c *boundedUDP) ReadFromAddrPort(data []byte) (int, netip.AddrPort, error) {
	return c.ReadFromUDPAddrPort(data)
}

func (c *boundedUDP) WriteToAddrPort(data []byte, address netip.AddrPort) (int, error) {
	return c.WriteToUDPAddrPort(data, address)
}

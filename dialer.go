// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"net"
)

type Dialer interface {
	Dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error)
}

// DatagramProvider is implemented by transports that support sending and
// receiving raw datagrams alongside the stream-based control channel.
// QUICTransport implements this interface, enabling datagram tunnel connections
// to bypass the 4-byte framing overhead used by stream-based tunnels.
type DatagramProvider interface {
	SendDatagram(data []byte) error
	ReceiveDatagram(ctx context.Context) ([]byte, error)
}

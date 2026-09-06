// See LICENSE file in the project root for license information.

package webtty

import (
	"errors"
	"io"
	"net"
	"os"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

var errClientStdinClosed = errors.New("WebTTY peer closed input")

func (c *clientRuntime) writeStdinMessage(message *pb.Message) error {
	err := c.writeMessage(message)
	if err != nil && isClientStdinWriteClosed(err) {
		return errors.Join(errClientStdinClosed, err)
	}
	return err
}

func isPipeWriteClosed(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) || isBrokenPipeError(err)
}

func isClientStdinWriteClosed(err error) bool {
	if isPipeWriteClosed(err) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var quicErr *quic.StreamError
	if errors.As(err, &quicErr) {
		return quicErr.Remote && quicErr.ErrorCode == 0
	}
	var transportErr *webtransport.StreamError
	return errors.As(err, &transportErr) && transportErr.Remote && transportErr.ErrorCode == 0
}

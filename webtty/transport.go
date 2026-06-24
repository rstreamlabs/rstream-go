// See LICENSE file in the project root for license information.

package webtty

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/webtransport-go"
)

type WebTTYTransport string

const (
	WebTTYTransportWebSocket    WebTTYTransport = "websocket"
	WebTTYTransportPlain        WebTTYTransport = "plain"
	WebTTYTransportWebTransport WebTTYTransport = "webtransport"
)

type messageConn interface {
	Close() error
	ReadMessage() (int, []byte, error)
	SetReadLimit(int64)
	SetWriteDeadline(time.Time) error
	WriteControl(int, []byte, time.Time) error
	WriteMessage(int, []byte) error
}

type plainMessageConn struct {
	conn      net.Conn
	readLimit int64
}

func newPlainMessageConn(conn net.Conn) *plainMessageConn {
	return &plainMessageConn{conn: conn}
}

func (c *plainMessageConn) Close() error {
	return c.conn.Close()
}

func (c *plainMessageConn) ReadMessage() (int, []byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if c.readLimit > 0 && int64(size) > c.readLimit {
		return 0, nil, fmt.Errorf("plain WebTTY message size %d exceeds read limit %d", size, c.readLimit)
	}
	maxInt := int(^uint(0) >> 1)
	if uint64(size) > uint64(maxInt) {
		return 0, nil, fmt.Errorf("plain WebTTY message size %d exceeds platform limit", size)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return 0, nil, err
	}
	return websocket.BinaryMessage, payload, nil
}

func (c *plainMessageConn) SetReadLimit(limit int64) {
	c.readLimit = limit
}

func (c *plainMessageConn) SetWriteDeadline(deadline time.Time) error {
	return c.conn.SetWriteDeadline(deadline)
}

func (c *plainMessageConn) WriteControl(messageType int, _ []byte, deadline time.Time) error {
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if messageType == websocket.CloseMessage {
		return c.conn.Close()
	}
	return nil
}

func (c *plainMessageConn) WriteMessage(messageType int, payload []byte) error {
	switch messageType {
	case websocket.BinaryMessage:
		if uint64(len(payload)) > uint64(^uint32(0)) {
			return fmt.Errorf("plain WebTTY message size %d exceeds uint32 frame limit", len(payload))
		}
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
		if _, err := c.conn.Write(header[:]); err != nil {
			return err
		}
		_, err := c.conn.Write(payload)
		return err
	case websocket.CloseMessage:
		return c.conn.Close()
	default:
		return fmt.Errorf("plain WebTTY transport only supports binary messages, got type %d", messageType)
	}
}

type webTransportMessageConn struct {
	session   *webtransport.Session
	stream    *webtransport.Stream
	readLimit int64
	closeOnce sync.Once
}

func newWebTransportMessageConn(session *webtransport.Session, stream *webtransport.Stream) *webTransportMessageConn {
	return &webTransportMessageConn{session: session, stream: stream}
}

func (c *webTransportMessageConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.stream != nil {
			err = c.stream.Close()
		}
		if c.session != nil {
			if closeErr := c.session.CloseWithError(0, ""); err == nil {
				err = closeErr
			}
		}
	})
	return err
}

func (c *webTransportMessageConn) ReadMessage() (int, []byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(c.stream, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if c.readLimit > 0 && int64(size) > c.readLimit {
		return 0, nil, fmt.Errorf("WebTransport WebTTY message size %d exceeds read limit %d", size, c.readLimit)
	}
	maxInt := int(^uint(0) >> 1)
	if uint64(size) > uint64(maxInt) {
		return 0, nil, fmt.Errorf("WebTransport WebTTY message size %d exceeds platform limit", size)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(c.stream, payload); err != nil {
		return 0, nil, err
	}
	return websocket.BinaryMessage, payload, nil
}

func (c *webTransportMessageConn) SetReadLimit(limit int64) {
	c.readLimit = limit
}

func (c *webTransportMessageConn) SetWriteDeadline(deadline time.Time) error {
	return c.stream.SetWriteDeadline(deadline)
}

func (c *webTransportMessageConn) WriteControl(messageType int, _ []byte, deadline time.Time) error {
	if err := c.stream.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if messageType == websocket.CloseMessage {
		return c.stream.Close()
	}
	return nil
}

func (c *webTransportMessageConn) WriteMessage(messageType int, payload []byte) error {
	switch messageType {
	case websocket.BinaryMessage:
		if uint64(len(payload)) > uint64(^uint32(0)) {
			return fmt.Errorf("WebTransport WebTTY message size %d exceeds uint32 frame limit", len(payload))
		}
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
		if _, err := c.stream.Write(header[:]); err != nil {
			return err
		}
		_, err := c.stream.Write(payload)
		return err
	case websocket.CloseMessage:
		return c.Close()
	default:
		return fmt.Errorf("WebTransport WebTTY transport only supports binary messages, got type %d", messageType)
	}
}

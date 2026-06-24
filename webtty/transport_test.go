// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"net"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPlainMessageConnFramesPayloads(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	clientConn := newPlainMessageConn(client)
	serverConn := newPlainMessageConn(server)
	errCh := make(chan error, 1)
	go func() {
		errCh <- clientConn.WriteMessage(websocket.BinaryMessage, []byte("payload"))
	}()
	messageType, payload, err := serverConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if messageType != websocket.BinaryMessage || !bytes.Equal(payload, []byte("payload")) {
		t.Fatalf("ReadMessage() = type %d payload %q", messageType, string(payload))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
}

func TestPlainMessageConnReadLimit(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	clientConn := newPlainMessageConn(client)
	serverConn := newPlainMessageConn(server)
	serverConn.SetReadLimit(3)
	errCh := make(chan error, 1)
	go func() {
		errCh <- clientConn.WriteMessage(websocket.BinaryMessage, []byte("toolong"))
	}()
	if _, _, err := serverConn.ReadMessage(); err == nil {
		t.Fatalf("ReadMessage() expected read limit error")
	}
	_ = serverConn.Close()
	<-errCh
}

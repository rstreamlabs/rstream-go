// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestStdinEOFWriteFailurePreservesReadableResponse(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	deadline := time.Now().Add(3 * time.Second)
	if err := listener.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	session := &ClientSession{runtime: &clientRuntime{conn: newPlainMessageConn(client)}}
	errors := make(chan error, 1)
	(&clientRuntime{}).stdinSessionLoop(t.Context(), session, errors, func(context.Context, []byte) (int, error) { return 0, io.EOF })
	select {
	case err := <-errors:
		t.Fatalf("stdin closure interrupted the readable response: %v", err)
	default:
	}
	payload, err := proto.Marshal(&pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 7}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := newPlainMessageConn(server).WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatal(err)
	}
	_, received, err := session.runtime.conn.ReadMessage()
	if err != nil || string(received) != string(payload) {
		t.Fatalf("response = %x, error = %v", received, err)
	}
}

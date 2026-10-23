// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func runMonitoredTerminationDescendant(address string) {
	monitor, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		os.Exit(2)
	}
	if os.Getenv("RSTREAM_WEBTTY_DESCENDANT_QUIET") == "true" {
		_ = os.Stdout.Close()
		_ = os.Stderr.Close()
	}
	ready, err := net.DialTimeout("tcp", os.Getenv("RSTREAM_WEBTTY_PARENT_READY"), time.Second)
	if err != nil {
		os.Exit(3)
	}
	if _, err := ready.Write([]byte("r")); err != nil {
		os.Exit(4)
	}
	_ = ready.Close()
	_, _ = io.Copy(io.Discard, monitor)
	os.Exit(0)
}

func runMonitoredTerminationParent() {
	ready, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWebTTYTerminationProcess$")
	child.Env = append(os.Environ(), "RSTREAM_WEBTTY_TERMINATION_HELPER=descendant", "RSTREAM_WEBTTY_PARENT_READY="+ready.Addr().String())
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(3)
	}
	conn, err := ready.Accept()
	if err != nil {
		os.Exit(4)
	}
	var received [1]byte
	if _, err := io.ReadFull(conn, received[:]); err != nil {
		os.Exit(5)
	}
	_ = conn.Close()
	_ = ready.Close()
	fmt.Fprint(os.Stdout, "final parent output\n")
	os.Exit(23)
}

func TestNativeCompletionCleansOwnedDescendants(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed_output=%t", quiet), func(t *testing.T) {
			monitor, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer monitor.Close()
			if err := monitor.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			zero, grace := time.Duration(0), 200*time.Millisecond
			handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero, SessionCloseDeadline: &grace}))
			server := httptest.NewServer(handler)
			defer server.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := handler.Shutdown(ctx); err != nil {
					t.Errorf("shutdown: %v", err)
				}
			}()
			conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Open{Open: &pb.Open{Config: &pb.Config{
				CmdArgs: []string{os.Args[0], "-test.run=^TestWebTTYTerminationProcess$"},
				EnvVars: []*pb.Environment{
					{Key: "RSTREAM_WEBTTY_TERMINATION_HELPER", Value: "natural-parent"},
					{Key: "RSTREAM_WEBTTY_DESCENDANT_MONITOR", Value: monitor.Addr().String()},
					{Key: "RSTREAM_WEBTTY_DESCENDANT_QUIET", Value: fmt.Sprint(quiet)},
				},
				Options: &pb.Options{},
			}}}})
			child, err := monitor.Accept()
			if err != nil {
				t.Fatalf("descendant did not start: %v", err)
			}
			defer child.Close()
			_ = child.SetReadDeadline(time.Now().Add(5 * time.Second))
			var output bytes.Buffer
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("no native result after parent exit: %v, output=%q", err, output.String())
				}
				var message pb.Message
				if err := proto.Unmarshal(raw, &message); err != nil {
					t.Fatal(err)
				}
				if data := message.GetData(); data != nil && data.Type == pb.Data_TYPE_STDOUT {
					output.Write(data.GetData())
				}
				if message.GetError() != nil {
					t.Fatal(message.GetError().GetMsg())
				}
				if result := message.GetClose(); result != nil {
					if result.ReturnCode != 23 || output.String() != "final parent output\n" {
						t.Fatalf("native result changed: %d, output=%q", result.ReturnCode, output.String())
					}
					break
				}
			}
			var closed [1]byte
			if n, err := child.Read(closed[:]); n != 0 || err != io.EOF {
				t.Fatalf("descendant outlived confirmed completion: n=%d, error=%v", n, err)
			}
		})
	}
}

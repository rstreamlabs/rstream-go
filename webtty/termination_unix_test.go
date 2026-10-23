// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestNativeTerminateCleansDescendantsHoldingOutputPipes(t *testing.T) {
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
		EnvVars: []*pb.Environment{{Key: "RSTREAM_WEBTTY_TERMINATION_HELPER", Value: "parent"}},
		Options: &pb.Options{},
	}}}})
	var stdout bytes.Buffer
	childPID := 0
	defer func() {
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	}()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("descendant prevented confirmed completion: %v; output=%q", err, stdout.String())
		}
		var message pb.Message
		if err := proto.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if data := message.GetData(); data != nil && data.Type == pb.Data_TYPE_STDOUT {
			stdout.Write(data.GetData())
			if childPID == 0 && strings.Contains(stdout.String(), "\n") {
				line := strings.SplitN(stdout.String(), "\n", 2)[0]
				childPID, err = strconv.Atoi(strings.TrimPrefix(line, "descendant:"))
				if err != nil || childPID <= 0 {
					t.Fatalf("invalid descendant readiness: %q", line)
				}
				request := &pb.Message{Payload: &pb.Message_Terminate{Terminate: &pb.Terminate{}}}
				writeWebTTYMessage(t, conn, request)
				writeWebTTYMessage(t, conn, request)
			}
		}
		if message.GetError() != nil {
			t.Fatalf("descendant cleanup failed: %s", message.GetError().GetMsg())
		}
		if result := message.GetClose(); result != nil {
			if childPID == 0 || result.ReturnCode != 23 || !strings.HasSuffix(stdout.String(), "final parent output\n") {
				t.Fatalf("root result changed during descendant cleanup: %d, %q", result.ReturnCode, stdout.String())
			}
			return
		}
	}
}

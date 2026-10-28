// See LICENSE file in the project root for license information.

//go:build darwin || linux || freebsd || netbsd || openbsd

package webtty

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"golang.org/x/term"
)

func TestCaptureCommandTerminalInputGeometryAndRestoration(t *testing.T) {
	for _, cancelInput := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed completion", true: "canceled input"}[cancelInput], func(t *testing.T) {
			master, stdin, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer stdin.Close()
			if err := pty.Setsize(stdin, &pty.Winsize{Rows: 31, Cols: 97}); err != nil {
				t.Fatal(err)
			}
			initial, err := term.GetState(int(stdin.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				options := readWebTTYMessage(t, conn).GetOpen().GetConfig().GetOptions()
				if !options.GetInteractive() || !options.GetAllocateTty() {
					t.Error("capture lost the requested terminal modes")
					return
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
				size := readWebTTYMessage(t, conn).GetParameter().GetTerminalSize()
				if size.GetRow() != 31 || size.GetCol() != 97 {
					t.Errorf("terminal geometry = %v", size)
					return
				}
				if cancelInput {
					cancel()
					_, _, _ = conn.ReadMessage()
					return
				}
				if _, err := master.WriteString("terminal input"); err != nil {
					t.Error(err)
					return
				}
				var input string
				for len(input) < len("terminal input") {
					data := readWebTTYMessage(t, conn).GetData()
					if data.GetType() != pb.Data_TYPE_STDIN || len(data.GetData()) == 0 {
						t.Errorf("terminal input = %v", data)
						return
					}
					input += string(data.GetData())
				}
				if input != "terminal input" {
					t.Errorf("terminal input = %q", input)
					return
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("final terminal output")}}}})
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: -1}}})
			})
			defer server.Close()
			result, err := CaptureCommand(ctx, &ClientConfig{URL: testWebTTYURL(server.URL), Interactive: true, AllocateTTY: true, Stdin: stdin}, 0)
			if cancelInput {
				if !errors.Is(err, context.Canceled) || result.ExitCode != nil {
					t.Fatalf("canceled capture = %+v, error = %v", result, err)
				}
			} else if err != nil || result.ExitCode == nil || *result.ExitCode != -1 || result.Stdout != "final terminal output" || result.Stderr != "" {
				t.Fatalf("capture = %+v, error = %v", result, err)
			}
			state, err := term.GetState(int(stdin.Fd()))
			if err != nil || !reflect.DeepEqual(state, initial) {
				t.Fatalf("borrowed terminal was not restored: %v", err)
			}
		})
	}
}

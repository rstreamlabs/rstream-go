// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestWebTTYCaptureReportsOnlyNativeExitCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		code *int32
	}{
		{name: "transport loss"},
		{name: "success", code: new(int32(0))},
		{name: "nonzero", code: new(int32(37))},
		{name: "native negative", code: new(int32(-1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Error(err)
					return
				}
				messages := []*pb.Message{
					{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}},
					{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("retained output")}}}},
				}
				if test.code != nil {
					messages = append(messages, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: *test.code}}})
				}
				for _, message := range messages {
					data, err := proto.Marshal(message)
					if err == nil {
						err = conn.WriteMessage(websocket.BinaryMessage, data)
					}
					if err != nil {
						t.Error(err)
						return
					}
					if message.GetAck() != nil {
						if _, _, err := conn.ReadMessage(); err != nil {
							t.Error(err)
							return
						}
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, runErr := runWebTTYClientCapture(ctx, &webtty.ClientConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Stdin: strings.NewReader("")})
			if (runErr == nil) != (test.code != nil) {
				t.Fatalf("capture error = %v, native code = %v", runErr, test.code)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				ExitCode *int32 `json:"exit_code"`
				Stdout   string `json:"stdout"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if (decoded.ExitCode == nil) != (test.code == nil) || (test.code != nil && *decoded.ExitCode != *test.code) || decoded.Stdout != "retained output" {
				t.Fatalf("capture = %s; expected only the confirmed native result and retained output", encoded)
			}
		})
	}
}

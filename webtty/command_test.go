// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestCaptureCommandBoundsOutputAndPreservesNativeResult(t *testing.T) {
	for _, test := range []struct {
		name      string
		limit     int
		supported bool
		lost      bool
		code      *int
	}{
		{name: "overflow with confirmed stop", limit: 8, supported: true, code: new(37)},
		{name: "overflow unsupported", limit: 8},
		{name: "exact limit", limit: 12, supported: true, code: new(0)},
		{name: "transport loss", limit: 12, lost: true},
		{name: "negative native code", limit: 12, code: new(-1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
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
				ack := &pb.Ack{}
				if test.supported {
					ack.Capabilities = []pb.SessionCapability{pb.SessionCapability_SESSION_CAPABILITY_TERMINATE}
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: ack}})
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Error(err)
					return
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("stdout")}}}})
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDERR, Payload: &pb.Data_Data{Data: []byte("stderr")}}}})
				if test.lost {
					return
				}
				if test.limit < 12 {
					_, raw, err := conn.ReadMessage()
					if !test.supported {
						if err == nil {
							t.Error("unsupported peer received a command instead of disconnection")
						}
						return
					}
					var message pb.Message
					if err != nil || proto.Unmarshal(raw, &message) != nil || message.GetTerminate() == nil {
						t.Errorf("expected an explicit stop request, got %v, error %v", &message, err)
						return
					}
					requests.Add(1)
					writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("final output after stop")}}}})
				}
				writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: int32(*test.code)}}})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := CaptureCommand(ctx, &ClientConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http")}, test.limit)
			if (result.ExitCode == nil) != (test.code == nil) || (test.code != nil && *result.ExitCode != *test.code) {
				t.Fatalf("native result = %+v, error = %v, expected code = %v", result, err, test.code)
			}
			if result.Stdout != "stdout" || len(result.Stdout)+len(result.Stderr) != min(test.limit, 12) || result.OutputTruncated != (test.limit < 12) {
				t.Fatalf("retained output = %+v", result)
			}
			if errors.Is(err, ErrCommandOutputLimit) != (test.limit < 12) {
				t.Fatalf("output limit error = %v", err)
			}
			if test.limit < 12 && !test.supported && !errors.Is(err, ErrTerminationUnsupported) {
				t.Fatalf("unsupported termination error = %v", err)
			}
			wantRequests := int32(0)
			if test.limit < 12 && test.supported {
				wantRequests = 1
			}
			if requests.Load() != wantRequests {
				t.Fatalf("termination requests = %d, expected %d", requests.Load(), wantRequests)
			}
		})
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestMCPWebTTYSessionStopRequiresControlAndPreservesNativeResult(t *testing.T) {
	for _, scenario := range []struct {
		name                                                             string
		oldEngine, oldNative, denied, disconnected, cancelled, completed bool
		code                                                             *int
		wantError                                                        bool
	}{
		{name: "confirmed stop", code: new(23)},
		{name: "old engine", oldEngine: true, wantError: true},
		{name: "old native", oldNative: true, wantError: true},
		{name: "control denied", denied: true, wantError: true},
		{name: "transport lost", disconnected: true, wantError: true},
		{name: "cancelled confirmation", cancelled: true, wantError: true},
		{name: "already completed negative code", completed: true, code: new(-1)},
		{name: "already ended unknown", completed: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var attached, detached, requested, terminated atomic.Int32
			var granted atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing authenticated Engine request")
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/webtty/sessions/session-1":
					session := rstream.WebTTYSession{ID: "session-1", Status: rstream.WebTTYSessionStatusActive, EncryptionMode: rstream.WebTTYEncryptionModeManaged, Live: rstream.WebTTYSessionLive{Available: true, Attachable: true, HasUpstream: true}}
					if scenario.completed {
						session.Status = rstream.WebTTYSessionStatusClosed
						if scenario.code != nil {
							session.ExitCode = new(int32(*scenario.code))
						}
					}
					_ = json.NewEncoder(w).Encode(session)
				case "/api/webtty/capabilities":
					_ = json.NewEncoder(w).Encode(rstream.WebTTYCapabilities{ManagedProtocol: true, LiveAttach: true, ControlTransfer: true, ExplicitTermination: !scenario.oldEngine})
				case "/api/webtty/sessions/session-1/participants":
					attached.Add(1)
					_ = json.NewEncoder(w).Encode(rstream.WebTTYParticipant{ID: "reader-1", SessionID: "session-1", Role: rstream.WebTTYParticipantRoleSpectator, AttachGrant: []byte("fixture-grant")})
				case "/api/webtty/sessions/session-1/participants/reader-1":
					detached.Add(1)
					_ = json.NewEncoder(w).Encode(rstream.WebTTYParticipant{ID: "reader-1", SessionID: "session-1"})
				case "/api/webtty/sessions/session-1/control-requests":
					requested.Add(1)
					var request rstream.CreateWebTTYControlRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ParticipantID != "reader-1" {
						t.Errorf("control request = %#v, %v", request, err)
					}
					_ = json.NewEncoder(w).Encode(rstream.WebTTYControlRequest{ID: "request-1", Status: rstream.WebTTYControlRequestPending})
				case "/api/webtty/sessions/session-1/control-requests/request-1":
					if scenario.denied {
						http.Error(w, "Forbidden", http.StatusForbidden)
						return
					}
					var request rstream.ResolveWebTTYControlRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Action != "grant" || request.ApproverParticipantID != "" {
						t.Errorf("permission-based grant = %#v, %v", request, err)
					}
					granted.Store(true)
					_ = json.NewEncoder(w).Encode(rstream.WebTTYControlRequest{ID: "request-1", Status: rstream.WebTTYControlRequestGranted})
				case "/api/webtty/sessions/session-1/participants/reader-1/stream":
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					_, data, err := conn.ReadMessage()
					message := &pb.Message{}
					if err != nil || proto.Unmarshal(data, message) != nil || message.GetAttach().GetSessionId() != "session-1" || message.GetOpen() != nil {
						t.Errorf("stop must attach without opening a command: %v, %v", message, err)
						return
					}
					ack := &pb.Ack{ManagedSessionId: "session-1"}
					if !scenario.oldNative {
						ack.Capabilities = []pb.SessionCapability{pb.SessionCapability_SESSION_CAPABILITY_TERMINATE}
					}
					send := func(message *pb.Message) {
						data, err := proto.Marshal(message)
						if err == nil {
							err = conn.WriteMessage(websocket.BinaryMessage, data)
						}
						if err != nil {
							t.Error(err)
						}
					}
					send(&pb.Message{Payload: &pb.Message_Ack{Ack: ack}})
					_, data, err = conn.ReadMessage()
					if err != nil {
						return
					}
					message.Reset()
					if proto.Unmarshal(data, message) != nil || message.GetTerminate() == nil || !granted.Load() {
						t.Errorf("only granted control may send Terminate: %v", message)
						return
					}
					terminated.Add(1)
					if scenario.cancelled {
						cancel()
						_, _, _ = conn.ReadMessage()
						return
					}
					if scenario.disconnected {
						return
					}
					for index := 0; index < 100; index++ {
						send(&pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte(strings.Repeat("final output", 256))}}}})
					}
					send(&pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: int32(*scenario.code)}}})
				default:
					t.Errorf("unexpected Engine request %s %s", r.Method, r.URL.Path)
					http.Error(w, "Not Found", http.StatusNotFound)
				}
			}))
			defer server.Close()
			configDir := t.TempDir()
			certificatePath := filepath.Join(configDir, "ca.pem")
			if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(configDir, "config.yaml")
			configuration := fmt.Sprintf("version: 1\ndefaults:\n  context:\n    name: fixture\ncontexts:\n  - name: fixture\n    apiUrl: %s\n    engine: %s\n    transport:\n      tls:\n        caFile: %s\n", server.URL, strings.TrimPrefix(server.URL, "https://"), certificatePath)
			if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"RSTREAM_API_URL", "RSTREAM_CONTEXT", "RSTREAM_ENGINE", "RSTREAM_MTLS_CERT_FILE", "RSTREAM_MTLS_KEY_FILE", "RSTREAM_REGION", "RSTREAM_TUNNEL_TRANSPORT", "RSTREAM_QUIC_TRANSPORT", "RSTREAM_CONTROL_PLANE_HEADERS"} {
				t.Setenv(key, "")
			}
			t.Setenv("RSTREAM_CONFIG", configPath)
			t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "fixture-token")
			response, err := mcpWebTTYSessionStop(ctx, map[string]json.RawMessage{"session_id": json.RawMessage(`"session-1"`), "reason": json.RawMessage(`"test explicit stop"`)})
			if err != nil {
				t.Fatal(err)
			}
			var result webTTYSessionStopResult
			if err := json.Unmarshal([]byte(mcpResultText(t, response)), &result); err != nil {
				t.Fatal(err)
			}
			isError, _ := response["isError"].(bool)
			if isError != scenario.wantError || result.SessionID != "session-1" || (result.ExitCode == nil) != (scenario.code == nil) || (scenario.code != nil && *result.ExitCode != *scenario.code) {
				t.Fatalf("stop result = %#v, %v", result, response)
			}
			wantAttach, wantControl, wantTerminate := int32(1), int32(1), int32(1)
			if scenario.completed || scenario.oldEngine {
				wantAttach, wantControl, wantTerminate = 0, 0, 0
			}
			if scenario.oldNative {
				wantControl, wantTerminate = 0, 0
			}
			if scenario.denied {
				wantTerminate = 0
			}
			if attached.Load() != wantAttach || detached.Load() != wantAttach || requested.Load() != wantControl || terminated.Load() != wantTerminate {
				t.Fatalf("attach/detach/control/terminate = %d/%d/%d/%d", attached.Load(), detached.Load(), requested.Load(), terminated.Load())
			}
		})
	}
}

func TestWebTTYSessionStopRejectsInvalidWaitBeforeConnecting(t *testing.T) {
	command, _, err := webttySessionsCmd.Find([]string{"stop", "session-1"})
	if err != nil || command != webttySessionsStopCmd {
		t.Fatalf("stop command not registered: %v", err)
	}
	flag := command.Flags().Lookup("timeout")
	previous, changed := flag.Value.String(), flag.Changed
	defer func() {
		_ = flag.Value.Set(previous)
		flag.Changed = changed
	}()
	for _, value := range []string{"0", "-1s", "66s"} {
		if err := command.Flags().Set("timeout", value); err != nil {
			t.Fatal(err)
		}
		if err := command.RunE(command, []string{"session-1"}); err == nil || !strings.Contains(err.Error(), "--timeout") {
			t.Fatalf("invalid CLI wait %s: %v", value, err)
		}
	}
	for _, value := range []string{"0", "-1", "66", "1.5"} {
		_, err := mcpWebTTYSessionStop(t.Context(), map[string]json.RawMessage{"session_id": json.RawMessage(`"session-1"`), "timeout_seconds": json.RawMessage(value)})
		if err == nil || !strings.Contains(err.Error(), "timeout_seconds") {
			t.Fatalf("invalid MCP wait %s: %v", value, err)
		}
	}
}

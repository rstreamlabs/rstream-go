// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

func TestWebTTYSessionStartReturnsAcknowledgedIdentityAndDisconnects(t *testing.T) {
	for _, scenario := range []struct {
		name                                                                                string
		cli, oldEngine, missingIdentity, unpublished, durationTooLong, transportUnsupported bool
		wantConnections                                                                     int32
		wantError                                                                           bool
	}{
		{name: "MCP acknowledged", wantConnections: 1},
		{name: "CLI acknowledged", cli: true, wantConnections: 1},
		{name: "old engine", oldEngine: true, wantError: true},
		{name: "missing identity is not retried", missingIdentity: true, wantConnections: 1, wantError: true},
		{name: "unpublished server", unpublished: true, wantError: true},
		{name: "engine duration limit", durationTooLong: true, wantError: true},
		{name: "engine transport limit", transportUnsupported: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var connections, stdinEOF atomic.Int32
			readerDisconnected := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing authenticated Engine request")
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/tunnels":
					_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{TunnelProperties: rstream.TunnelProperties{
						ID: new("shell"), Publish: new(!scenario.unpublished), Host: new(r.Host), Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
						Labels: map[string]string{webtty.WebTTYApplicationProtocolKey: webtty.WebTTYApplicationProtocol, webtty.WebTTYTransportLabelKey: "websocket"},
					}, Status: "online"}})
				case "/api/webtty/capabilities":
					maximum := 60
					if scenario.durationTooLong {
						maximum = 1
					}
					transports := []rstream.WebTTYTransport{rstream.WebTTYTransportWebSocket}
					if scenario.transportUnsupported {
						transports = nil
					}
					_ = json.NewEncoder(w).Encode(rstream.WebTTYCapabilities{ManagedProtocol: true, DetachedExecution: !scenario.oldEngine, MaxExecutionSeconds: maximum, ExecutionTransports: transports})
				case "/":
					connections.Add(1)
					defer close(readerDisconnected)
					if r.URL.Query().Get("execution_seconds") != "30" || r.URL.Query().Get("session_mode") != "non-interactive" {
						t.Errorf("missing bounded managed execution parameters: %s", r.URL.RawQuery)
					}
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
					if err != nil || proto.Unmarshal(data, message) != nil || message.GetOpen() == nil {
						t.Errorf("expected one Open: %v, %v", message, err)
						return
					}
					if message.GetOpen().GetConfig().GetOptions().GetInteractive() || message.GetOpen().GetConfig().GetOptions().GetAllocateTty() || strings.Join(message.GetOpen().GetConfig().GetCmdArgs(), " ") != "fixture command" {
						t.Errorf("unexpected detached command: %v", message)
					}
					identity := "session-confirmed"
					if scenario.missingIdentity {
						identity = ""
					}
					ack, err := proto.Marshal(&pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{ManagedSessionId: identity}}})
					if err != nil || conn.WriteMessage(websocket.BinaryMessage, ack) != nil {
						t.Errorf("failed to send Ack: %v", err)
						return
					}
					for {
						_, data, err := conn.ReadMessage()
						if err != nil {
							return
						}
						message := &pb.Message{}
						if err := proto.Unmarshal(data, message); err != nil {
							t.Error(err)
							return
						}
						if message.GetData().GetType() == pb.Data_TYPE_STDIN && message.GetData().GetEos() != nil {
							stdinEOF.Add(1)
						} else if message.GetHeartbeat() == nil {
							t.Errorf("start sent unexpected input, termination or another Open: %v", message)
						}
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			configureWebTTYStartFixture(t, server)
			var result webTTYSessionStartResult
			var isError bool
			var operationError error
			if scenario.cli {
				command := &cobra.Command{RunE: webttySessionsStartCmd.RunE}
				addWebTTYClientFlags(command, "json")
				command.Flags().Int("execution-seconds", 0, "")
				command.SetContext(ctx)
				command.SetIn(strings.NewReader("must not be forwarded"))
				output := &bytes.Buffer{}
				command.SetOut(output)
				if err := command.ParseFlags([]string{"--url=rstrm://shell", "--execution-seconds=30"}); err != nil {
					t.Fatal(err)
				}
				err := command.RunE(command, []string{"fixture", "command"})
				operationError = err
				isError = err != nil
				if err := json.Unmarshal(output.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
			} else {
				response, err := handleMCPToolCall(ctx, json.RawMessage(`{"name":"rstream_webtty_session_start","arguments":{"url":"rstrm://shell","command":["fixture","command"],"execution_seconds":30}}`))
				operationError = err
				isError = err != nil
				if err == nil {
					isError, _ = response["isError"].(bool)
					if err := json.Unmarshal([]byte(mcpResultText(t, response)), &result); err != nil {
						t.Fatal(err)
					}
				}
			}
			if connections.Load() > 0 {
				select {
				case <-readerDisconnected:
				case <-ctx.Done():
					t.Fatal("reader transport retained after start returned")
				}
			}
			if isError != scenario.wantError || connections.Load() != scenario.wantConnections {
				t.Fatalf("result=%#v error=%v (%v) connections=%d", result, isError, operationError, connections.Load())
			}
			if !isError && (result.SessionID != "session-confirmed" || stdinEOF.Load() != 1) {
				t.Fatalf("result=%#v stdin EOF=%d", result, stdinEOF.Load())
			}
			if isError && result.SessionID != "" {
				t.Fatalf("unconfirmed identity: %#v", result)
			}
		})
	}
}

func configureWebTTYStartFixture(t *testing.T, server *httptest.Server) {
	t.Helper()
	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "engine-ca.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.yaml")
	configuration := fmt.Sprintf("version: 1\ndefaults:\n  context:\n    name: fixture\ncontexts:\n  - name: fixture\n    apiUrl: %s\n    engine: %s\n    transport:\n      tls:\n        caFile: %s\n", server.URL, strings.TrimPrefix(server.URL, "https://"), certificatePath)
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"RSTREAM_API_URL", "RSTREAM_CONTEXT", "RSTREAM_ENGINE", "RSTREAM_MTLS_CERT_FILE", "RSTREAM_MTLS_KEY_FILE", "RSTREAM_REGION", "RSTREAM_TUNNEL_TRANSPORT", "RSTREAM_QUIC_TRANSPORT", "RSTREAM_CONTROL_PLANE_HEADERS"} {
		t.Setenv(key, "")
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "fixture-token")
}

func TestWebTTYSessionStartRejectsInvalidInputBeforeConnecting(t *testing.T) {
	command, _, err := webttySessionsCmd.Find([]string{"start"})
	if err != nil || command != webttySessionsStartCmd {
		t.Fatalf("start command not registered: %v", err)
	}
	for _, flags := range [][]string{
		{"--url=rstrm://shell"},
		{"--url=rstrm://shell", "--execution-seconds=86401"},
		{"--url=rstrm://shell", "--execution-seconds=-1"},
		{"--url=wss://direct.invalid", "--execution-seconds=30"},
		{"--url=rstrm://shell", "--execution-seconds=30", "--tty"},
		{"--url=rstrm://shell", "--execution-seconds=30", "--interactive"},
		{"--url=rstrm://shell", "--execution-seconds=30", "--no-discovery", "--transport=websocket"},
		{"--url=rstrm://shell", "--execution-seconds=30", "--output=text"},
	} {
		cmd := &cobra.Command{RunE: command.RunE}
		addWebTTYClientFlags(cmd, "json")
		cmd.Flags().Int("execution-seconds", 0, "")
		cmd.SetContext(t.Context())
		if err := cmd.ParseFlags(flags); err != nil {
			t.Fatal(err)
		}
		if err := cmd.RunE(cmd, []string{"fixture"}); err == nil {
			t.Fatalf("accepted %v", flags)
		}
	}
	for _, seconds := range []string{"null", "0", "-1", "86401", "1.5"} {
		_, err := handleMCPToolCall(t.Context(), json.RawMessage(fmt.Sprintf(`{"name":"rstream_webtty_session_start","arguments":{"url":"rstrm://shell","command":["fixture"],"execution_seconds":%s}}`, seconds)))
		if err == nil || !strings.Contains(err.Error(), "execution_seconds") {
			t.Fatalf("accepted MCP duration %s: %v", seconds, err)
		}
	}
}

func TestWebTTYSessionStartUsesPublishedWebTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	packets, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packets.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("unauthenticated metadata request")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tunnels":
			_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{TunnelProperties: rstream.TunnelProperties{
				ID: new("shell"), Publish: new(true), Host: new(packets.LocalAddr().String()), Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
				Labels: map[string]string{webtty.WebTTYApplicationProtocolKey: webtty.WebTTYApplicationProtocol, webtty.WebTTYTransportLabelKey: "webtransport"},
			}, Status: "online"}})
		case "/api/webtty/capabilities":
			_ = json.NewEncoder(w).Encode(rstream.WebTTYCapabilities{ManagedProtocol: true, DetachedExecution: true, MaxExecutionSeconds: 60, ExecutionTransports: []rstream.WebTTYTransport{rstream.WebTTYTransportWebTransport}})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	configureWebTTYStartFixture(t, api)
	mux := http.NewServeMux()
	tlsConfig := api.TLS.Clone()
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	transport := &webtransport.Server{H3: &http3.Server{Handler: mux, TLSConfig: tlsConfig, EnableDatagrams: true}, CheckOrigin: func(*http.Request) bool { return true }}
	webtransport.ConfigureHTTP3Server(transport.H3)
	readerDone := make(chan struct{})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		defer close(readerDone)
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.URL.Query().Get("execution_seconds") != "30" || r.URL.Query().Get("session_mode") != "non-interactive" {
			t.Error("missing managed WebTransport authorization or budget")
		}
		session, err := transport.Upgrade(w, r)
		if err != nil {
			t.Error(err)
			return
		}
		defer session.CloseWithError(0, "")
		stream, err := session.AcceptStream(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		_ = stream.SetDeadline(time.Now().Add(4 * time.Second))
		message, err := readWebTTYStartFrame(stream)
		if err != nil || message.GetOpen() == nil {
			t.Errorf("expected Open: %v, %v", message, err)
			return
		}
		ack, err := proto.Marshal(&pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{ManagedSessionId: "webtransport-session"}}})
		if err != nil {
			t.Error(err)
			return
		}
		frame := binary.BigEndian.AppendUint32(nil, uint32(len(ack)))
		if _, err := stream.Write(append(frame, ack...)); err != nil {
			t.Error(err)
			return
		}
		message, err = readWebTTYStartFrame(stream)
		if err != nil || message.GetData().GetType() != pb.Data_TYPE_STDIN || message.GetData().GetEos() == nil {
			t.Errorf("expected empty stdin: %v, %v", message, err)
			return
		}
		if message, err := readWebTTYStartFrame(stream); err == nil {
			t.Errorf("expected reader disconnect without another request: %v", message)
		}
	})
	serverDone := make(chan error, 1)
	go func() { serverDone <- transport.Serve(packets) }()
	defer func() {
		_ = transport.Close()
		_ = packets.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("WebTransport listener retained")
		}
	}()
	command := &cobra.Command{RunE: webttySessionsStartCmd.RunE}
	addWebTTYClientFlags(command, "json")
	command.Flags().Int("execution-seconds", 0, "")
	command.SetContext(ctx)
	output := &bytes.Buffer{}
	command.SetOut(output)
	if err := command.ParseFlags([]string{"--url=rstrm://shell", "--execution-seconds=30"}); err != nil {
		t.Fatal(err)
	}
	if err := command.RunE(command, []string{"fixture"}); err != nil {
		t.Fatal(err)
	}
	var result webTTYSessionStartResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "webtransport-session" {
		t.Fatalf("start result = %#v", result)
	}
	select {
	case <-readerDone:
	case <-ctx.Done():
		t.Fatal("WebTransport reader retained")
	}
}

func readWebTTYStartFrame(reader io.Reader) (*pb.Message, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > 16384 {
		return nil, fmt.Errorf("fixture frame too large: %d", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	message := &pb.Message{}
	return message, proto.Unmarshal(data, message)
}

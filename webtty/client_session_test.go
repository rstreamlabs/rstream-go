// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"google.golang.org/protobuf/proto"
)

func TestResolveSessionConfigDefaults(t *testing.T) {
	cfg, err := resolveSessionConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "ws://127.0.0.1:8080" {
		t.Fatalf("unexpected default URL: %q", cfg.URL)
	}
	if cfg.MaxMessageSize == nil || *cfg.MaxMessageSize != defaultMaxMessageSize {
		t.Fatalf("unexpected max message size: %v", cfg.MaxMessageSize)
	}
	if cfg.ReadBufferSize == nil || *cfg.ReadBufferSize != defaultReadBufferSize {
		t.Fatalf("unexpected read buffer size: %v", cfg.ReadBufferSize)
	}
	if cfg.WriteBufferSize == nil || *cfg.WriteBufferSize != defaultWriteBufferSize {
		t.Fatalf("unexpected write buffer size: %v", cfg.WriteBufferSize)
	}
	if cfg.OpenDeadline == nil || *cfg.OpenDeadline != defaultClientOpenDeadline {
		t.Fatalf("unexpected open deadline: %v", cfg.OpenDeadline)
	}
	if cfg.CloseDeadline == nil || *cfg.CloseDeadline != defaultClientCloseDeadline {
		t.Fatalf("unexpected close deadline: %v", cfg.CloseDeadline)
	}
	if cfg.HeartbeatInterval == nil || *cfg.HeartbeatInterval != defaultHeartbeatInterval {
		t.Fatalf("unexpected heartbeat interval: %v", cfg.HeartbeatInterval)
	}
	if cfg.Logger == nil {
		t.Fatalf("expected default logger")
	}
}

func TestClientRuntimeCloseInterruptsBlockedWrite(t *testing.T) {
	conn := newBlockingSessionMessageConn()
	closeDeadline := 50 * time.Millisecond
	runtime := &clientRuntime{conn: conn, cfg: &ClientConfig{CloseDeadline: &closeDeadline}}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- runtime.writeMessage(&pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
	}()
	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("WebTTY client write did not start")
	}
	closeDone := make(chan struct{})
	go func() {
		runtime.closeConn()
		close(closeDone)
	}()
	completed := false
	select {
	case <-closeDone:
		completed = true
	case <-time.After(100 * time.Millisecond):
	}
	_ = conn.Close()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("WebTTY client close remained blocked after transport close")
	}
	select {
	case err := <-writeDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked write error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked WebTTY client write did not return")
	}
	if !completed {
		t.Fatal("WebTTY client close did not interrupt the blocked write")
	}
}

func TestClientSessionConfigConversionsCopySlices(t *testing.T) {
	workdir := "/tmp"
	username := "demo"
	cfg := (&ClientConfig{
		URL:         "ws://example.test",
		EnvVars:     []string{"A=1"},
		Workdir:     &workdir,
		Username:    &username,
		CmdArgs:     []string{"sh", "-lc", "true"},
		Interactive: true,
		AllocateTTY: true,
		Attach: &AttachConfig{
			SessionID:     "session-1",
			ParticipantID: "participant-1",
			AttachGrant:   []byte("grant"),
			Capabilities:  []AttachCapability{AttachCapabilityReadStream},
		},
	}).sessionConfig()
	cfg.EnvVars[0] = "A=2"
	cfg.CmdArgs[0] = "bash"
	cfg.Attach.AttachGrant[0] = 'G'
	cfg.Attach.Capabilities[0] = AttachCapabilityRequestControl
	if got := (&ClientConfig{EnvVars: []string{"A=1"}, CmdArgs: []string{"sh"}}).sessionConfig(); got.EnvVars[0] != "A=1" || got.CmdArgs[0] != "sh" {
		t.Fatalf("sessionConfig should copy slices")
	}
	clientCfg := cfg.clientConfig()
	clientCfg.EnvVars[0] = "A=3"
	clientCfg.CmdArgs[0] = "zsh"
	clientCfg.Attach.AttachGrant[0] = 'R'
	clientCfg.Attach.Capabilities[0] = AttachCapabilityReceiveControl
	if cfg.EnvVars[0] != "A=2" || cfg.CmdArgs[0] != "bash" {
		t.Fatalf("clientConfig should copy slices")
	}
	if string(cfg.Attach.AttachGrant) != "Grant" || cfg.Attach.Capabilities[0] != AttachCapabilityRequestControl {
		t.Fatalf("clientConfig should deep-copy attach slices: %#v", cfg.Attach)
	}
	if clientCfg.Workdir != &workdir || clientCfg.Username != &username {
		t.Fatalf("pointer fields should be preserved")
	}
	if (*ClientConfig)(nil).sessionConfig() != nil {
		t.Fatalf("nil client config should convert to nil session config")
	}
	if (*SessionConfig)(nil).clientConfig() != nil {
		t.Fatalf("nil session config should convert to nil client config")
	}
}

func TestCloneTLSConfigWithWebTTYDefaults(t *testing.T) {
	cfg := cloneTLSConfigWithWebTTYDefaults(nil)
	if cfg == nil || cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("nil TLS config default = %#v, want TLS 1.3 minimum", cfg)
	}
	custom := &tls.Config{ServerName: "terminal.example", MinVersion: tls.VersionTLS12}
	cloned := cloneTLSConfigWithWebTTYDefaults(custom)
	if cloned == custom {
		t.Fatalf("expected TLS config to be cloned")
	}
	if cloned.ServerName != "terminal.example" || cloned.MinVersion != tls.VersionTLS13 {
		t.Fatalf("custom TLS config defaults not applied: %#v", cloned)
	}
	if custom.ServerName != "terminal.example" || custom.MinVersion != tls.VersionTLS12 {
		t.Fatalf("source TLS config was mutated")
	}
}

func TestWebTransportClientQUICConfig(t *testing.T) {
	direct := webTransportClientQUICConfig(false)
	if direct.InitialPacketSize != 0 || direct.DisablePathMTUDiscovery {
		t.Fatalf("direct config unexpectedly constrains MTU: %#v", direct)
	}
	tunneled := webTransportClientQUICConfig(true)
	if tunneled.InitialPacketSize != tunneledWebTransportInitialPacketSize || !tunneled.DisablePathMTUDiscovery {
		t.Fatalf("tunneled config does not constrain MTU: %#v", tunneled)
	}
	if !tunneled.EnableDatagrams || !tunneled.EnableStreamResetPartialDelivery {
		t.Fatalf("tunneled config lost WebTransport features: %#v", tunneled)
	}
}

func TestDecodeClientSessionEvent(t *testing.T) {
	tests := []struct {
		name    string
		data    *pb.Data
		want    ClientSessionEvent
		wantOK  bool
		wantErr bool
	}{
		{
			name:   "stdout data",
			data:   &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("out")}},
			want:   ClientSessionEvent{Stream: ClientSessionStdout, Data: []byte("out")},
			wantOK: true,
		},
		{
			name:   "stderr data",
			data:   &pb.Data{Type: pb.Data_TYPE_STDERR, Payload: &pb.Data_Data{Data: []byte("err")}},
			want:   ClientSessionEvent{Stream: ClientSessionStderr, Data: []byte("err")},
			wantOK: true,
		},
		{
			name:   "eos is ignored",
			data:   &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Eos{Eos: &pb.EndOfStream{}}},
			wantOK: false,
		},
		{name: "nil data", wantErr: true},
		{name: "unexpected stream", data: &pb.Data{Type: pb.Data_TYPE_STDIN, Payload: &pb.Data_Data{Data: []byte("in")}}, wantErr: true},
		{name: "unexpected payload", data: &pb.Data{Type: pb.Data_TYPE_STDOUT}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := decodeClientSessionEvent(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestClientSessionPayloadCryptoSendsEncryptedInput(t *testing.T) {
	received := make(chan *pb.Message, 2)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		OpenDeadline: durationPtr(time.Second),
		PayloadCrypto: &PayloadCrypto{
			EncryptStdin: func(_ context.Context, payload []byte) (*EncryptedPayload, error) {
				return &EncryptedPayload{
					Ciphertext:      append([]byte("enc:"), payload...),
					PlaintextLength: uint32(len(payload)),
					PayloadCrypto: &PayloadCryptoMetadata{
						PayloadSuite: PayloadCipherSuiteAES256GCM,
						PayloadKeyID: []byte("payload-key"),
					},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if open := receiveMessage(t, received).GetOpen(); len(open.Capabilities) == 0 || open.Capabilities[0] != pb.OpenCapability_OPEN_CAPABILITY_ENCRYPTED_PAYLOAD {
		t.Fatalf("expected encrypted payload capability, got %#v", open)
	}
	if err := session.SendText("hello"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	stdin := receiveMessage(t, received).GetData().GetEncryptedData()
	if stdin == nil {
		t.Fatalf("expected encrypted stdin payload")
	}
	if string(stdin.Ciphertext) != "enc:hello" || stdin.PlaintextLength != 5 {
		t.Fatalf("unexpected encrypted payload: %#v", stdin)
	}
	if stdin.PayloadCrypto.GetPayloadSuite() != pb.PayloadCipherSuite_PAYLOAD_CIPHER_SUITE_AES_256_GCM || !bytes.Equal(stdin.PayloadCrypto.GetPayloadKeyId(), []byte("payload-key")) {
		t.Fatalf("unexpected payload crypto metadata: %#v", stdin.PayloadCrypto)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestClientSessionPayloadCryptoDecryptsEncryptedEvents(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{
			Type: pb.Data_TYPE_STDOUT,
			Payload: &pb.Data_EncryptedData{EncryptedData: &pb.EncryptedPayload{
				Ciphertext:      []byte("out"),
				PlaintextLength: 3,
			}},
		}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		OpenDeadline: durationPtr(time.Second),
		PayloadCrypto: &PayloadCrypto{
			DecryptStdout: func(_ context.Context, payload *EncryptedPayload) ([]byte, error) {
				return append([]byte("dec:"), payload.Ciphertext...), nil
			},
		},
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	event := receiveClientSessionEvent(t, session.Events())
	if event.Stream != ClientSessionStdout || string(event.Data) != "dec:out" {
		t.Fatalf("unexpected event: %#v", event)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestClientSessionPayloadCryptoRejectsEncryptedOutputWithoutHook(t *testing.T) {
	data := &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_EncryptedData{EncryptedData: &pb.EncryptedPayload{Ciphertext: []byte("out")}}}
	if _, _, err := decodeClientSessionEvent(data); err == nil || !strings.Contains(err.Error(), "decrypt hook") {
		t.Fatalf("expected missing decrypt hook error, got %v", err)
	}
}

func TestOpenClientSessionRequiresServerHelloWhenServerIdentityExpected(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatalf("client sent an open message before receiving a signed server hello")
		}
	})
	defer server.Close()
	_, err = OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		OpenDeadline:           durationPtr(time.Second),
		ExpectedServerIdentity: &serverPublic,
	})
	if err == nil || !strings.Contains(err.Error(), "expected WebTTY server hello") {
		t.Fatalf("OpenClientSession() error = %v, want expected server hello error", err)
	}
}

func TestOpenClientSessionReturnsProtocolErrorBeforeServerHello(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_ProtocolError{ProtocolError: &pb.ProtocolError{Code: pb.ProtocolErrorCode_PROTOCOL_ERROR_CODE_CLIENT_PROOF_REQUIRED}}})
	})
	defer server.Close()
	_, err = OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		OpenDeadline:           durationPtr(time.Second),
		ExpectedServerIdentity: &serverPublic,
	})
	if err == nil || !strings.Contains(err.Error(), "client proof") {
		t.Fatalf("OpenClientSession() error = %v, want protocol client proof error", err)
	}
}

func TestOpenClientSessionRejectsUnsignedServerHelloBeforeOpen(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		hello := signedServerHelloForTest(t, serverIdentity, WebTTYTransportWebSocket, "workspace-1", "project-1", "server-1", "session-1", AuthRequirementClientProof)
		hello.ServerProof.Signature[0] ^= 0xff
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_ServerHello{ServerHello: hello}})
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatalf("client sent an open message after an invalid server hello")
		}
	})
	defer server.Close()
	_, err = OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		OpenDeadline:           durationPtr(time.Second),
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		CmdArgs:                []string{"whoami"},
	})
	if err == nil || !strings.Contains(err.Error(), "WebTTY server proof") {
		t.Fatalf("OpenClientSession() error = %v, want server proof error", err)
	}
}

func TestOpenClientSessionRejectsClientProofRequirementWithoutIdentityBeforeOpen(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		hello := signedServerHelloForTest(t, serverIdentity, WebTTYTransportWebSocket, "workspace-1", "project-1", "server-1", "session-1", AuthRequirementClientProof)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_ServerHello{ServerHello: hello}})
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatalf("client sent an open message without a client endpoint identity")
		}
	})
	defer server.Close()
	_, err = OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		OpenDeadline:           durationPtr(time.Second),
		ExpectedServerIdentity: &serverPublic,
		CmdArgs:                []string{"whoami"},
	})
	if err == nil || !strings.Contains(err.Error(), "client endpoint identity") {
		t.Fatalf("OpenClientSession() error = %v, want client endpoint identity error", err)
	}
}

func TestOpenClientSessionSendsClientProofAfterSignedServerHello(t *testing.T) {
	serverIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(server) error = %v", err)
	}
	clientIdentity, err := GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(client) error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	credential := []byte("workspace-managed-client-credential")
	received := make(chan *pb.Open, 1)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		hello := signedServerHelloForTest(t, serverIdentity, WebTTYTransportWebSocket, "workspace-1", "project-1", "server-1", "session-1", AuthRequirementClientProof)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_ServerHello{ServerHello: hello}})
		open := readWebTTYMessage(t, conn).GetOpen()
		received <- open
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:                    testWebTTYURL(server.URL),
		OpenDeadline:           durationPtr(time.Second),
		EndpointIdentity:       clientIdentity,
		ExpectedServerIdentity: &serverPublic,
		ClientCredential:       credential,
		CmdArgs:                []string{"whoami"},
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	var open *pb.Open
	select {
	case open = <-received:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for open message")
	}
	if open == nil || open.ClientProof == nil {
		t.Fatalf("client proof is missing from open message: %#v", open)
	}
	if !bytes.Equal(open.ClientProof.SigningKeyId, clientIdentity.Signing.KeyID) {
		t.Fatalf("client proof signing key id mismatch")
	}
	if open.ClientProof.Credential == nil || !bytes.Equal(open.ClientProof.Credential.Value, credential) {
		t.Fatalf("client proof credential mismatch: %#v", open.ClientProof.Credential)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestClientSessionWaitClosedChannel(t *testing.T) {
	session := &ClientSession{resultCh: make(chan clientSessionResult)}
	close(session.resultCh)
	exitCode, err := session.Wait()
	if exitCode != -1 || !errors.Is(err, io.EOF) {
		t.Fatalf("got exit=%d err=%v", exitCode, err)
	}
}

func TestClientSessionCloseStoresLocalResult(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != -1 {
		t.Fatalf("Wait() after Close() = %d, %v", exitCode, err)
	}
}

func TestClientSessionCloseReleasesParentContextWatcher(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()
	baseline := runtime.NumGoroutine()
	const sessions = 32
	for i := 0; i < sessions; i++ {
		session, err := OpenClientSession(context.Background(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
		if err != nil {
			t.Fatalf("OpenClientSession() error = %v", err)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, err := session.Wait(); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline+8 && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline+8 {
		t.Fatalf("goroutines after closing sessions = %d, baseline = %d", got, baseline)
	}
}

func TestClientSessionCloseDoesNotRequireDrainingEvents(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		for i := 0; i < 256; i++ {
			raw, err := proto.Marshal(&pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{
				Type:    pb.Data_TYPE_STDOUT,
				Payload: &pb.Data_Data{Data: []byte("output")},
			}}})
			if err != nil {
				t.Errorf("marshal protobuf message: %v", err)
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		OpenDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for len(session.events) < cap(session.events) && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := len(session.events); got != cap(session.events) {
		t.Fatalf("buffered events = %d, want %d", got, cap(session.events))
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	waitDone := make(chan clientSessionResult, 1)
	go func() {
		exitCode, err := session.Wait()
		waitDone <- clientSessionResult{exitCode: exitCode, err: err}
	}()
	select {
	case result := <-waitDone:
		if result.exitCode != -1 || result.err != nil {
			t.Fatalf("Wait() after Close() = %d, %v", result.exitCode, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait() remained blocked while output events were not consumed")
	}
}

func TestClientSessionRunRejectsMalformedMessages(t *testing.T) {
	cases := []clientEvent{
		{msg: nil},
		{msg: &pb.Message{Payload: &pb.Message_Close{}}},
		{msg: &pb.Message{Payload: &pb.Message_Error{}}},
	}
	for i, event := range cases {
		session := newBareClientSession(t)
		readEvents := make(chan clientEvent, 1)
		loopErrCh := make(chan error, 1)
		readEvents <- event
		go session.run(t.Context(), readEvents, loopErrCh)
		exitCode, err := session.Wait()
		if exitCode != -1 || err == nil {
			t.Fatalf("case %d: Wait() = %d, %v; want error", i, exitCode, err)
		}
	}
}

func TestClientSessionStringFolding(t *testing.T) {
	if !stringsEqualFoldTrimmed(" Dev ", "dev") {
		t.Fatalf("trimmed fold comparison failed")
	}
	if got := stringsTrimSpaceFold(" Dev "); got != "dev" {
		t.Fatalf("unexpected folded string: %q", got)
	}
}

func TestOpenClientSessionSendsInputEOFResizeAndWaitsForClose(t *testing.T) {
	received := make(chan *pb.Message, 4)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		msg := readWebTTYMessage(t, conn)
		received <- msg
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		for i := 0; i < 3; i++ {
			received <- readWebTTYMessage(t, conn)
		}
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 7}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	open := receiveMessage(t, received).GetOpen()
	if open == nil || open.Config == nil {
		t.Fatalf("expected open message, got %#v", open)
	}
	if err := session.SendText("hello"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	if err := session.Resize(24, 80); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	stdin := receiveMessage(t, received).GetData()
	if stdin.GetType() != pb.Data_TYPE_STDIN || string(stdin.GetData()) != "hello" {
		t.Fatalf("unexpected stdin message: %#v", stdin)
	}
	eof := receiveMessage(t, received).GetData()
	if eof.GetEos() == nil {
		t.Fatalf("expected stdin EOF message: %#v", eof)
	}
	size := receiveMessage(t, received).GetParameter().GetTerminalSize()
	if size.GetRow() != 24 || size.GetCol() != 80 {
		t.Fatalf("unexpected terminal size: %#v", size)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 7 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionSendsAttachMessage(t *testing.T) {
	received := make(chan *pb.Message, 1)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("live")}}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		OpenDeadline: durationPtr(time.Second),
		Attach: &AttachConfig{
			SessionID:     "session-1",
			ParticipantID: "participant-1",
			AttachGrant:   []byte("grant"),
			RequestedRole: AttachRoleSpectator,
			Transport:     WebTTYTransportWebSocket,
			Capabilities:  []AttachCapability{AttachCapabilityReadStream, AttachCapabilityRequestControl},
			DeviceID:      "device-1",
		},
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	attach := receiveMessage(t, received).GetAttach()
	if attach == nil {
		t.Fatalf("expected attach message")
	}
	if attach.SessionId != "session-1" || attach.ParticipantId != "participant-1" || !bytes.Equal(attach.AttachGrant, []byte("grant")) {
		t.Fatalf("attach identity fields not sent: %#v", attach)
	}
	if attach.RequestedRole != pb.AttachRole_ATTACH_ROLE_SPECTATOR ||
		attach.Transport != pb.AttachTransport_ATTACH_TRANSPORT_WEBSOCKET ||
		attach.GetDeviceId().GetValue() != "device-1" {
		t.Fatalf("attach metadata not sent: %#v", attach)
	}
	event := receiveClientSessionEvent(t, session.Events())
	if event.Stream != ClientSessionStdout || string(event.Data) != "live" {
		t.Fatalf("unexpected attached event: %#v", event)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionReturnsAttachServerError(t *testing.T) {
	received := make(chan *pb.Message, 1)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: "workspace-managed WebTTY client device is not trusted for this server"}}})
	})
	defer server.Close()
	_, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		OpenDeadline: durationPtr(time.Second),
		Attach: &AttachConfig{
			SessionID:     "session-1",
			ParticipantID: "participant-1",
			AttachGrant:   []byte("grant"),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "workspace-managed WebTTY client device is not trusted") {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if attach := receiveMessage(t, received).GetAttach(); attach == nil {
		t.Fatalf("expected attach message")
	}
}

func TestOpenClientSessionReturnsOpenServerError(t *testing.T) {
	received := make(chan *pb.Message, 1)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: "WebTTY client proof is required"}}})
	})
	defer server.Close()
	_, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		OpenDeadline: durationPtr(time.Second),
		CmdArgs:      []string{"whoami"},
	})
	if err == nil || !strings.Contains(err.Error(), "WebTTY client proof is required") {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if open := receiveMessage(t, received).GetOpen(); open == nil {
		t.Fatalf("expected open message")
	}
}

func TestOpenClientSessionReceivesEvents(t *testing.T) {
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("out")}}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Heartbeat{Heartbeat: &pb.Heartbeat{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 3}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	select {
	case event := <-session.Events():
		if event.Stream != ClientSessionStdout || string(event.Data) != "out" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for stdout event")
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 3 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionSendsBearerToken(t *testing.T) {
	authToken := "  secret-token  "
	receivedAuth := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth <- r.Header.Get("Authorization")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	}))
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{
		URL:          testWebTTYURL(server.URL),
		AuthToken:    &authToken,
		OpenDeadline: durationPtr(time.Second),
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	select {
	case got := <-receivedAuth:
		if got != "Bearer secret-token" {
			t.Fatalf("Authorization header = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Authorization header")
	}
}

func TestOpenClientSessionSendsHeartbeat(t *testing.T) {
	heartbeat := 10 * time.Millisecond
	received := make(chan *pb.Message, 2)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		received <- readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), SendHeartbeat: true, HeartbeatInterval: &heartbeat, OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if receiveMessage(t, received).GetOpen() == nil {
		t.Fatalf("expected open message")
	}
	if receiveMessage(t, received).GetHeartbeat() == nil {
		t.Fatalf("expected heartbeat message")
	}
	exitCode, err := session.Wait()
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
}

func TestOpenClientSessionEnforcesReadLimit(t *testing.T) {
	maxSize := int64(8)
	server := newClientSessionTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		_ = readWebTTYMessage(t, conn)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}})
		large := strings.Repeat("x", 64)
		writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: large}}})
	})
	defer server.Close()
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), MaxMessageSize: &maxSize, OpenDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	exitCode, err := session.Wait()
	if exitCode != -1 || err == nil {
		t.Fatalf("Wait() = %d, %v; want read limit error", exitCode, err)
	}
}

func TestOpenClientSessionRequiresCustomDialerForRstreamURL(t *testing.T) {
	_, err := OpenClientSession(t.Context(), &SessionConfig{URL: "rstrm://shell"})
	if err == nil || !strings.Contains(err.Error(), "requires a custom dialer") {
		t.Fatalf("expected custom dialer error, got %v", err)
	}
}

func newClientSessionTestServer(t *testing.T, handle func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		handle(conn)
	}))
}

func readWebTTYMessage(t *testing.T, conn *websocket.Conn) *pb.Message {
	t.Helper()
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type = %d", messageType)
	}
	msg := &pb.Message{}
	if err := proto.Unmarshal(raw, msg); err != nil {
		t.Fatalf("decode protobuf message: %v", err)
	}
	return msg
}

func writeWebTTYMessage(t *testing.T, conn *websocket.Conn, msg *pb.Message) {
	t.Helper()
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal protobuf message: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatalf("write websocket message: %v", err)
	}
}

func receiveMessage(t *testing.T, ch <-chan *pb.Message) *pb.Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for message")
		return nil
	}
}

func receiveClientSessionEvent(t *testing.T, ch <-chan ClientSessionEvent) ClientSessionEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for client session event")
		return ClientSessionEvent{}
	}
}

func testWebTTYURL(raw string) string {
	return "ws" + strings.TrimPrefix(raw, "http")
}

func durationPtr(value time.Duration) *time.Duration {
	return &value
}

func newBareClientSession(t *testing.T) *ClientSession {
	t.Helper()
	_, cancel := context.WithCancel(t.Context())
	return &ClientSession{
		loopCancel: cancel,
		doneRead:   make(chan struct{}),
		done:       make(chan struct{}),
		events:     make(chan ClientSessionEvent, 1),
		resultCh:   make(chan clientSessionResult, 1),
	}
}

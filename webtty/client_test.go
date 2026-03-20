// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestNormalizeWebTTYURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default scheme", input: "127.0.0.1:8080", want: "ws://127.0.0.1:8080/"},
		{name: "ws scheme", input: "ws://localhost:8080/path", want: "ws://localhost:8080/path"},
		{name: "wss scheme", input: "wss://example.com", want: "wss://example.com/"},
		{name: "rstrm scheme", input: "rstrm://shell", want: "ws://shell/"},
		{name: "invalid scheme", input: "http://localhost", wantErr: true},
		{name: "missing host", input: "ws:///path", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeWebTTYURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeWebTTYURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected normalized url: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestResolveWebTTYEndpoint(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		wantURL            string
		wantRequiresCustom bool
		wantErr            bool
	}{
		{name: "ws endpoint", input: "ws://localhost:8080/path", wantURL: "ws://localhost:8080/path", wantRequiresCustom: false},
		{name: "wss endpoint", input: "wss://example.com", wantURL: "wss://example.com/", wantRequiresCustom: false},
		{name: "rstrm endpoint", input: "rstrm://shell", wantURL: "ws://shell/", wantRequiresCustom: true},
		{name: "rstrm path", input: "rstrm://shell/session?mode=ro", wantURL: "ws://shell/session?mode=ro", wantRequiresCustom: true},
		{name: "invalid endpoint", input: "http://localhost", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWebTTYEndpoint(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWebTTYEndpoint returned error: %v", err)
			}
			if got.URL != tt.wantURL {
				t.Fatalf("unexpected normalized url: got %q want %q", got.URL, tt.wantURL)
			}
			if got.RequiresCustomDial != tt.wantRequiresCustom {
				t.Fatalf("unexpected custom dial requirement: got %t want %t", got.RequiresCustomDial, tt.wantRequiresCustom)
			}
		})
	}
}

func TestParseClientEnvVars(t *testing.T) {
	t.Setenv("RSTREAM_TEST_ENV", "from-env")
	values, err := parseClientEnvVars([]string{"A=1", "RSTREAM_TEST_ENV", "EMPTY="})
	if err != nil {
		t.Fatalf("parseClientEnvVars returned error: %v", err)
	}
	if len(values) != 3 {
		t.Fatalf("unexpected env var count: got %d want 3", len(values))
	}
	if values[0].Key != "A" || values[0].Value != "1" {
		t.Fatalf("unexpected first env var: %#v", values[0])
	}
	if values[1].Key != "RSTREAM_TEST_ENV" || values[1].Value != "from-env" {
		t.Fatalf("unexpected second env var: %#v", values[1])
	}
	if values[2].Key != "EMPTY" || values[2].Value != "" {
		t.Fatalf("unexpected third env var: %#v", values[2])
	}
}

func TestParseClientUsername(t *testing.T) {
	idValue := "42"
	id, err := parseClientUsername(&idValue)
	if err != nil {
		t.Fatalf("parseClientUsername(id) returned error: %v", err)
	}
	if id.GetId() != 42 {
		t.Fatalf("unexpected numeric id: got %d want 42", id.GetId())
	}
	nameValue := "alice"
	name, err := parseClientUsername(&nameValue)
	if err != nil {
		t.Fatalf("parseClientUsername(name) returned error: %v", err)
	}
	if name.GetName() != "alice" {
		t.Fatalf("unexpected username: got %q want %q", name.GetName(), "alice")
	}
}

func TestResolveClientConfigDefaults(t *testing.T) {
	cfg, err := resolveClientConfig(nil)
	if err != nil {
		t.Fatalf("resolveClientConfig returned error: %v", err)
	}
	if got, want := cfg.URL, "ws://127.0.0.1:8080"; got != want {
		t.Fatalf("unexpected default url: got %q want %q", got, want)
	}
	if got, want := *cfg.OpenDeadline, 5*time.Second; got != want {
		t.Fatalf("unexpected open deadline: got %s want %s", got, want)
	}
	if got, want := *cfg.CloseDeadline, 5*time.Second; got != want {
		t.Fatalf("unexpected close deadline: got %s want %s", got, want)
	}
	if got, want := *cfg.HeartbeatInterval, 5*time.Second; got != want {
		t.Fatalf("unexpected heartbeat interval: got %s want %s", got, want)
	}
}

func TestHandleOpenMessage(t *testing.T) {
	runtime := &clientRuntime{}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}}); err != nil {
		t.Fatalf("handleOpenMessage(ack) returned error: %v", err)
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Error{Error: &pb.Error{Msg: "server error"}}}); err == nil {
		t.Fatalf("handleOpenMessage(error) returned nil")
	}
	if err := runtime.handleOpenMessage(&pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 0}}}); err == nil {
		t.Fatalf("handleOpenMessage(close) returned nil")
	}
}

func TestHandleSessionMessage(t *testing.T) {
	var stdout bytes.Buffer
	runtime := &clientRuntime{cfg: &ClientConfig{Stdout: &stdout, Stderr: &bytes.Buffer{}}}
	exitCode, done, err := runtime.handleSessionMessage(&pb.Message{Payload: &pb.Message_Data{Data: &pb.Data{Type: pb.Data_TYPE_STDOUT, Payload: &pb.Data_Data{Data: []byte("ok")}}}})
	if err != nil {
		t.Fatalf("handleSessionMessage(data) returned error: %v", err)
	}
	if done {
		t.Fatalf("handleSessionMessage(data) unexpectedly finished the session")
	}
	if exitCode != -1 {
		t.Fatalf("unexpected exit code for data: got %d want -1", exitCode)
	}
	if got := stdout.String(); got != "ok" {
		t.Fatalf("unexpected stdout payload: got %q want %q", got, "ok")
	}
	exitCode, done, err = runtime.handleSessionMessage(&pb.Message{Payload: &pb.Message_Close{Close: &pb.Close{ReturnCode: 7}}})
	if err != nil {
		t.Fatalf("handleSessionMessage(close) returned error: %v", err)
	}
	if !done {
		t.Fatalf("handleSessionMessage(close) did not finish the session")
	}
	if exitCode != 7 {
		t.Fatalf("unexpected exit code for close: got %d want 7", exitCode)
	}
	if _, done, err := runtime.handleSessionMessage(&pb.Message{Payload: &pb.Message_Ack{Ack: &pb.Ack{}}}); err == nil || !done {
		t.Fatalf("handleSessionMessage(ack) should fail and finish the session")
	}
}

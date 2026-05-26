// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
)

func TestResolveServerConfigDefaultsAndHandlerDrain(t *testing.T) {
	cfg := resolveServerConfig(nil)
	if cfg.MaxMessageSize == nil || *cfg.MaxMessageSize != defaultMaxMessageSize {
		t.Fatalf("unexpected max message size")
	}
	if cfg.EnvVars == nil || len(*cfg.EnvVars) != 0 {
		t.Fatalf("expected empty env var map")
	}
	if cfg.SessionOpenDeadline == nil || *cfg.SessionOpenDeadline != defaultSessionOpenDeadline {
		t.Fatalf("unexpected open deadline")
	}
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{}))
	sessionObj := &session{}
	if !handler.registerSession(sessionObj) {
		t.Fatalf("expected registration before drain")
	}
	if got := handler.snapshotSessions(); len(got) != 1 || got[0] != sessionObj {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
	handler.unregisterSession(sessionObj)
	if got := handler.snapshotSessions(); len(got) != 0 {
		t.Fatalf("expected empty snapshot after unregister")
	}
	handler.BeginDrain()
	if handler.registerSession(&session{}) {
		t.Fatalf("registration should fail while draining")
	}
	if err := handler.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown without sessions: %v", err)
	}
}

func TestHandlerServeHTTPRejectsWhileDraining(t *testing.T) {
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{}))
	handler.BeginDrain()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/webtty", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("ServeHTTP() status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestWebTTYHandlerExecutesProcessAndStreamsOutput(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), CmdArgs: []string{"/bin/sh", "-c", "printf stdout; printf stderr >&2; exit 6"}, OpenDeadline: durationPtr(time.Second), CloseDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 6 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stdout != "stdout" || stderr != "stderr" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestWebTTYHandlerDrainsNonTTYOutputBeforeWait(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), CmdArgs: []string{"/bin/sh", "-c", "i=0; while [ $i -lt 8192 ]; do printf 0123456789abcdef; i=$((i+1)); done"}, OpenDeadline: durationPtr(time.Second), CloseDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	if stderr != "" {
		t.Fatalf("stderr=%q, want empty", stderr)
	}
	want := strings.Repeat("0123456789abcdef", 8192)
	if stdout != want {
		t.Fatalf("stdout length=%d, want %d", len(stdout), len(want))
	}
}

func TestWebTTYHandlerPassesStdinWorkdirAndEnvironment(t *testing.T) {
	zero := time.Duration(0)
	workdir := t.TempDir()
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	session, err := OpenClientSession(t.Context(), &SessionConfig{URL: testWebTTYURL(server.URL), EnvVars: []string{"CUSTOM=value"}, Workdir: &workdir, CmdArgs: []string{"/bin/sh", "-c", "read line; printf \"%s|%s|%s\" \"$line\" \"$CUSTOM\" \"$(pwd)\""}, OpenDeadline: durationPtr(time.Second), CloseDeadline: durationPtr(time.Second)})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("typed\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := session.SendEOF(); err != nil {
		t.Fatalf("SendEOF() error = %v", err)
	}
	stdout, _, exitCode, err := collectClientSessionOutput(t, session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v", exitCode, err)
	}
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if stdout != "typed|value|"+resolvedWorkdir {
		t.Fatalf("stdout=%q, want typed|value|%s", stdout, resolvedWorkdir)
	}
}

func TestRunClientInteractivePipeSession(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	var stdout strings.Builder
	var stderr strings.Builder
	exitCode, err := RunClient(t.Context(), &ClientConfig{URL: testWebTTYURL(server.URL), Interactive: true, Stdin: strings.NewReader("typed\n"), Stdout: &stdout, Stderr: &stderr, CmdArgs: []string{"/bin/sh", "-c", "read line; printf \"%s\" \"$line\"; printf err >&2; exit 5"}, OpenDeadline: durationPtr(time.Second), CloseDeadline: durationPtr(time.Second)})
	if err != nil || exitCode != 5 {
		t.Fatalf("RunClient() = %d, %v", exitCode, err)
	}
	if stdout.String() != "typed" || stderr.String() != "err" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestWebTTYHandlerReportsOpenConfigErrors(t *testing.T) {
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	writeWebTTYMessage(t, conn, &pb.Message{Payload: &pb.Message_Open{Open: &pb.Open{}}})
	msg := readWebTTYMessage(t, conn)
	if msg.GetError() == nil || !strings.Contains(msg.GetError().Msg, "missing open config") {
		t.Fatalf("unexpected error message: %#v", msg)
	}
}

func TestWebTTYHandlerOpenTimeoutClosesIdleConnection(t *testing.T) {
	openDeadline := 20 * time.Millisecond
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{SessionOpenDeadline: &openDeadline}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected idle connection to be closed")
	}
}

func TestSessionRejectsMalformedMessages(t *testing.T) {
	session := &session{}
	cases := []*pb.Message{
		nil,
		{Payload: &pb.Message_Data{}},
		{Payload: &pb.Message_Error{}},
		{Payload: &pb.Message_Parameter{}},
		{Payload: &pb.Message_Parameter{Parameter: &pb.Parameter{Parameter: &pb.Parameter_TerminalSize{}}}},
	}
	for i, msg := range cases {
		if err := session.handleMessage(msg); err == nil {
			t.Fatalf("case %d: expected malformed message error", i)
		}
	}
	if err := session.handleData(nil); err == nil {
		t.Fatalf("expected nil data error")
	}
	if err := session.doResize(nil); err == nil {
		t.Fatalf("expected nil terminal size error")
	}
}

func TestWebTTYHandlerRequiresBearerToken(t *testing.T) {
	token := "test-token"
	handler := NewWebTTYHandler(&ServerConfig{AuthToken: &token})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	if _, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), nil); err == nil {
		t.Fatalf("expected unauthenticated websocket dial to fail")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), header)
	if err != nil {
		t.Fatalf("authenticated websocket dial failed: %v", err)
	}
	conn.Close()
}

func TestWebTTYHandlerRejectsCrossOrigin(t *testing.T) {
	allow := true
	handler := NewWebTTYHandler(&ServerConfig{AllowUnauthenticated: &allow})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	header := http.Header{}
	header.Set("Origin", "https://evil.example")
	if _, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), header); err == nil {
		t.Fatalf("expected cross-origin websocket dial to fail")
	}
}

func TestWebTTYHandlerAllowsExplicitWildcardOrigin(t *testing.T) {
	allow := true
	handler := NewWebTTYHandler(&ServerConfig{
		AllowUnauthenticated: &allow,
		AllowedOrigins:       []string{"*"},
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(context.Background())
	header := http.Header{}
	header.Set("Origin", "http://localhost:3000")
	conn, _, err := websocket.DefaultDialer.Dial(testWebTTYURL(server.URL), header)
	if err != nil {
		t.Fatalf("expected wildcard origin websocket dial to pass: %v", err)
	}
	conn.Close()
}

func TestWebTTYOriginRejectsSameHostWhenUnauthenticated(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/webtty", nil)
	request.Host = "attacker.example"
	request.Header.Set("Origin", "http://attacker.example")
	if webTTYOriginAllowed(request, nil, false) {
		t.Fatalf("same-host origin should not be enough when unauthenticated mode is enabled")
	}
	if !webTTYOriginAllowed(request, nil, true) {
		t.Fatalf("same-host origin should remain allowed for authenticated handlers")
	}
	if !webTTYOriginAllowed(request, []string{"http://attacker.example"}, false) {
		t.Fatalf("explicit allowed origin should be honored")
	}
}

func TestWebTTYOriginNormalizesDefaultPorts(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://terminal.example/webtty", nil)
	request.Host = "terminal.example"
	request.Header.Set("Origin", "https://terminal.example:443")
	if !webTTYOriginAllowed(request, nil, true) {
		t.Fatalf("same-host origin with default HTTPS port should be allowed")
	}
	if !webTTYOriginAllowed(request, []string{"https://terminal.example"}, false) {
		t.Fatalf("explicit allowed origin should allow default HTTPS port")
	}
	request.Header.Set("Origin", "https://terminal.example:444")
	if webTTYOriginAllowed(request, nil, true) {
		t.Fatalf("same-host origin with non-default port should be rejected")
	}
}

func testServerConfig(cfg ServerConfig) *ServerConfig {
	allow := true
	cfg.AllowUnauthenticated = &allow
	return &cfg
}

func collectClientSessionOutput(t *testing.T, session *ClientSession) (string, string, int, error) {
	t.Helper()
	resultCh := make(chan clientSessionResult, 1)
	go func() {
		exitCode, err := session.Wait()
		resultCh <- clientSessionResult{exitCode: exitCode, err: err}
	}()
	events := session.Events()
	var stdout strings.Builder
	var stderr strings.Builder
	result := clientSessionResult{exitCode: -1}
	haveResult := false
	for events != nil || !haveResult {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Stream == ClientSessionStderr {
				stderr.Write(event.Data)
			} else {
				stdout.Write(event.Data)
			}
		case result = <-resultCh:
			haveResult = true
			resultCh = nil
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for webtty session output")
		}
	}
	return stdout.String(), stderr.String(), result.exitCode, result.err
}

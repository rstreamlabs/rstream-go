// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/fileserver"
)

func TestFilesPasswordInputs(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		want        string
		fails       bool
	}{
		{"public", "", nil, "", false},
		{"stdin", " secret \r\n", []string{"--password-file", "-"}, " secret ", false},
		{"empty", "\n", []string{"--password-file", "-"}, "", true},
		{"multiline", "one\ntwo", []string{"--password-file", "-"}, "", true},
		{"too long", strings.Repeat("x", 4097), []string{"--password-file", "-"}, "", true},
		{"nonterminal", "secret", []string{"--password"}, "", true},
		{"empty path", "", []string{"--password-file="}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newFilesCmd()
			command.SetIn(strings.NewReader(tc.input))
			if err := command.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			got, err := filesPassword(command, "rstream")
			if (err != nil) != tc.fails || got != tc.want {
				t.Fatalf("password mismatch or unexpected error: %v", err)
			}
		})
	}
	filename := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(filename, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newFilesCmd()
	if err := command.ParseFlags([]string{"--password-file", filename}); err != nil {
		t.Fatal(err)
	}
	if got, err := filesPassword(command, "rstream"); got != "from-file" || err != nil {
		t.Fatalf("file input failed: %v", err)
	}
}

func TestFilesAuthenticationPolicy(t *testing.T) {
	command := newFilesCmd()
	if getBoolPtr(command, "rstream-auth") != nil || getBoolPtr(command, "token-auth") != nil {
		t.Fatal("defaults must inherit project policy")
	}
	if err := command.ParseFlags([]string{"--rstream-auth=false"}); err != nil {
		t.Fatal(err)
	}
	if value := getBoolPtr(command, "rstream-auth"); value == nil || *value {
		t.Fatal("explicit false lost")
	}
	for _, tc := range []struct {
		token, account, password bool
		want                     string
		fails                    bool
	}{
		{false, false, false, "public", false},
		{false, false, true, "password", false},
		{true, false, false, "token", false},
		{false, true, false, "rstream", false},
		{true, false, true, "", true},
		{false, true, true, "", true},
	} {
		access, err := filesAccess(rstream.TunnelProperties{TokenAuth: &tc.token, RstreamAuth: &tc.account}, tc.password)
		if access != tc.want || (err != nil) != tc.fails {
			t.Fatalf("policy: %q %v", access, err)
		}
	}
}

func TestFilesHTTPStopsActiveRequestsAndReusesHandler(t *testing.T) {
	for range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		finished := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- serveFilesHTTP(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(finished)
			}))
		}()
		clientDone := make(chan struct{})
		go func() {
			defer close(clientDone)
			client := &http.Client{Timeout: 3 * time.Second}
			response, err := client.Get("http://" + listener.Addr().String())
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("request did not start")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown blocked")
		}
		select {
		case <-finished:
		default:
			t.Fatal("returned while handler still running")
		}
		<-clientDone
	}
}

func TestFilesTransferOptions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		write, noUI bool
		limit       int64
		failure     string
	}{
		{name: "defaults"},
		{name: "write", args: []string{"--read-write"}, write: true, limit: fileserver.DefaultMaxUploadSize},
		{name: "headless write", args: []string{"--read-write", "--no-ui", "--max-upload-size=1024"}, write: true, noUI: true, limit: 1024},
		{name: "headless rtc", args: []string{"--backend=webrtc", "--no-ui"}, noUI: true},
		{name: "explicit false", args: []string{"--read-write=false", "--no-ui=false"}},
		{name: "silent terminal", args: []string{"--output=none"}},
		{name: "unsupported writes", args: []string{"--backend=webrtc", "--read-write"}, failure: "WebRTC is read-only"},
		{name: "readonly limit", args: []string{"--max-upload-size=1024"}, failure: "requires read-write"},
		{name: "rtc limit", args: []string{"--backend=webrtc", "--max-upload-size=1024"}, failure: "requires read-write"},
		{name: "zero limit", args: []string{"--read-write", "--max-upload-size=0"}, failure: "greater than zero"},
		{name: "negative limit", args: []string{"--read-write", "--max-upload-size=-1"}, failure: "greater than zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newFilesCmd()
			command.SetContext(t.Context())
			if err := command.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			if tc.failure != "" {
				if err := runFiles(command, []string{t.TempDir()}); err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("validation must precede credentials and network: %v", err)
				}
				return
			}
			cfg, err := filesConfig(command, t.TempDir())
			if err != nil || cfg.ReadWrite != tc.write || (cfg.UI == nil) != tc.noUI || cfg.MaxUploadSize != tc.limit {
				t.Fatalf("options: %+v %v", cfg, err)
			}
			s, err := fileserver.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			response := httptest.NewRecorder()
			s.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
			if tc.noUI && response.Code != 404 || !tc.noUI && response.Code != 200 {
				t.Fatalf("browser visibility: %d", response.Code)
			}
		})
	}
}

func TestFilesStatusReportsWriteCapability(t *testing.T) {
	for _, write := range []bool{false, true} {
		var output bytes.Buffer
		ctx := &forwardCtx{Out: &output}
		status := forwardStatus{Files: &fileserver.Info{Backend: "webdav", Capabilities: fileserver.Capabilities{Write: write}}}
		ctx.renderStatusText(status)
		want := "read-only"
		if write {
			want = "read-write"
		}
		if !strings.Contains(output.String(), "mode       : "+want) {
			t.Fatalf("text status: %s", output.String())
		}
		found := false
		for _, row := range forwardUIStatusRows(status, true) {
			if row[0] == "mode" {
				found = row[1] == want
			}
		}
		if !found {
			t.Fatal("interactive status has the wrong write mode")
		}
	}
}

func TestFilesReadWriteServiceSurvivesInterruptedUploadAndReconnect(t *testing.T) {
	root := t.TempDir()
	service, err := fileserver.New(fileserver.Config{Root: root, ReadWrite: true, MaxUploadSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for attempt := range 2 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started, finished := make(chan struct{}, 1), make(chan struct{}, 1)
		done := make(chan error, 1)
		go func() {
			done <- serveFilesHTTP(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					started <- struct{}{}
					defer func() { finished <- struct{}{} }()
				}
				service.ServeHTTP(w, r)
			}))
		}()
		connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		body := "12"
		if attempt == 1 {
			body = "1234"
		}
		if _, err := io.WriteString(connection, "PUT /fs/file HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nConnection: close\r\n\r\n"+body); err != nil {
			t.Fatal(err)
		}
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("upload did not start")
		}
		if attempt == 1 {
			response, err := io.ReadAll(connection)
			if err != nil || !bytes.Contains(response, []byte("201 Created")) {
				t.Fatalf("upload after reconnect: %q %v", response, err)
			}
		}
		_ = connection.Close()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("upload did not release its handler")
		}
		if data, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(data) != body {
			t.Fatalf("upload contents: %q %v", data, err)
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("HTTP service did not stop")
		}
	}
	response := httptest.NewRecorder()
	service.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != 404 || !service.Info().Capabilities.Write || service.Info().MaxUploadSize != 8 {
		t.Fatal("reconnection changed write options or exposed the UI")
	}
}

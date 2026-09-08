// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
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
			got, err := filesPassword(command)
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
	if got, err := filesPassword(command); got != "from-file" || err != nil {
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

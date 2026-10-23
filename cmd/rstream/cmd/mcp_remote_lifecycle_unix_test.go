// See LICENSE file in the project root for license information.

//go:build linux || darwin

package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestRemoteExposeForwarderProcess(t *testing.T) {
	address := os.Getenv("RSTREAM_TEST_REMOTE_EXPOSE_MONITOR")
	if address == "" {
		return
	}
	if slices.Contains(os.Args, "--new-process-group") {
		if err := newForwardProcessGroup(); err != nil {
			os.Exit(3)
		}
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		os.Exit(2)
	}
	defer conn.Close()
	fmt.Println(`{"status":"online","tunnel_id":"test-forwarder"}`)
	_, _ = io.Copy(conn, conn)
	os.Exit(0)
}

func TestRemoteExposeForwarderSurvivesLaunchCommand(t *testing.T) {
	monitor, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	if err := monitor.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	launcher := filepath.Join(dir, "rstream")
	script := "#!/bin/sh\nexec " + shellSingleQuote(os.Args[0]) + " -test.run='^TestRemoteExposeForwarderProcess$' -- \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	zero := time.Duration(0)
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{HeartbeatInterval: &zero, AllowUnauthenticated: new(true)})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()
	expose := remoteExposeArgs{ID: "lifecycle-test", RstreamCmd: launcher, Host: "127.0.0.1", Port: "8765", Timeout: 3}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := runWebTTYClientCapture(ctx, &webtty.ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Stdin: strings.NewReader(""),
		CmdArgs: []string{"/bin/sh", "-c", remoteExposeShellScript(expose)},
		EnvVars: []string{"RSTREAM_TEST_REMOTE_EXPOSE_MONITOR=" + monitor.Addr().String(), "RSTREAM_REMOTE_EXPOSE_DIR=" + dir},
	})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, remoteExposeStatusLine+"online") {
		t.Fatalf("launch result = %+v, error = %v", result, err)
	}
	forwarder, err := monitor.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer forwarder.Close()
	_ = forwarder.SetDeadline(time.Now().Add(time.Second))
	if _, err := forwarder.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	var reply [5]byte
	if _, err := io.ReadFull(forwarder, reply[:]); err != nil || string(reply[:]) != "alive" {
		t.Fatalf("forwarder was lost after its launch command completed: reply = %q, error = %v", reply, err)
	}
}

// See LICENSE file in the project root for license information.

package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReexecShutdownStopsChild(t *testing.T) {
	if os.Getenv("RSTREAM_TEST_REEXEC_HELPER") == "1" {
		if strings.Contains(os.Getenv("GODEBUG"), "http2xconnect=1") {
			fmt.Println(os.Getpid())
			time.Sleep(30 * time.Second)
			return
		}
		reexec("http2xconnect=1")
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix process signals")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReexecShutdownStopsChild$")
	cmd.Env = append(os.Environ(), "RSTREAM_TEST_REEXEC_HELPER=1", "GODEBUG=")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	childPID := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			childPID <- scanner.Text()
		}
	}()
	var pid int
	select {
	case value := <-childPID:
		pid, err = strconv.Atoi(value)
		if err != nil || pid <= 0 || pid == cmd.Process.Pid {
			t.Fatalf("invalid child PID %q", value)
		}
	case <-ctx.Done():
		t.Fatal("re-executed child did not start")
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Kill() })
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() // Signal-driven exit status is not the lifecycle contract.
	if ctx.Err() != nil {
		t.Fatal("parent did not terminate after SIGTERM")
	}
	if err := child.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("re-executed child survived its parent")
	}
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "no origin", host: "viewer.example.com", want: true},
		{name: "matching origin", host: "viewer.example.com", origin: "https://viewer.example.com", want: true},
		{name: "matching origin case insensitive", host: "VIEWER.example.com", origin: "https://viewer.EXAMPLE.com", want: true},
		{name: "different origin", host: "viewer.example.com", origin: "https://other.example.com", want: false},
		{name: "invalid origin", host: "viewer.example.com", origin: "://invalid", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("CONNECT", "https://"+tt.host+"/ws", nil)
			req.Host = tt.host
			req.Header.Set("Origin", tt.origin)
			if got := sameOrigin(req); got != tt.want {
				t.Fatalf("sameOrigin() = %t, want %t", got, tt.want)
			}
		})
	}
}

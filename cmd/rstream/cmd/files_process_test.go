//go:build !rstream_fips

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/fileserver"
	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestFilesystemCLIProcess(t *testing.T) {
	if os.Getenv("RSTREAM_FILES_PROCESS_HELPER") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		os.Exit(2)
	}
	os.Args = append([]string{"rstream"}, os.Args[separator+1:]...)
	ctx := context.Background()
	if os.Getenv("RSTREAM_FILES_PROCESS_DEADLINE") == "1" {
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		ctx = bounded
	}
	ExecuteContext(ctx)
	os.Exit(0)
}

func filesystemProcess(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestFilesystemCLIProcess$", "--"}, args...)...)
	command.Env = append(os.Environ(), "RSTREAM_FILES_PROCESS_HELPER=1")
	command.WaitDelay = 3 * time.Second
	return command
}

type filesystemRPCResponse struct {
	ID     int             `json:"id"`
	Error  *mcpError       `json:"error"`
	Result json.RawMessage `json:"result"`
}

type filesystemRPC struct {
	input  *json.Encoder
	output *json.Decoder
	nextID int
}

func newFilesystemRPC(t *testing.T) *filesystemRPC {
	t.Helper()
	command := filesystemProcess(t, "mcp", "serve")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("MCP process: %v: %s", err, stderr.String())
		}
	})
	client := &filesystemRPC{input: json.NewEncoder(input), output: json.NewDecoder(output)}
	client.call(t, "initialize", map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "filesystem-regression", "version": "1"}})
	client.notify(t, "notifications/initialized", map[string]any{})
	return client
}

func (c *filesystemRPC) send(t *testing.T, method string, params any) int {
	t.Helper()
	c.nextID++
	if err := c.input.Encode(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
	return c.nextID
}

func (c *filesystemRPC) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	id := c.send(t, method, params)
	var response filesystemRPCResponse
	if err := c.output.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ID != id || response.Error != nil || len(response.Result) == 0 {
		t.Fatalf("MCP response to %s: %+v", method, response)
	}
	return response.Result
}

func (c *filesystemRPC) notify(t *testing.T, method string, params any) {
	t.Helper()
	if err := c.input.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
}

func filesystemToolResult(t *testing.T, result json.RawMessage, wantError bool) {
	t.Helper()
	var value struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &value); err != nil {
		t.Fatal(err)
	}
	if value.IsError != wantError {
		t.Fatalf("MCP tool error = %v, want %v: %s", value.IsError, wantError, result)
	}
}

func assertFilesystemDownload(t *testing.T, path string, payload []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(payload) || sha256.Sum256(actual) != sha256.Sum256(payload) {
		t.Fatalf("download size or SHA-256 mismatch: %d bytes", len(actual))
	}
}

func TestFilesystemCLIAndMCPBackendMatrix(t *testing.T) {
	clearRstreamTestEnv(t)
	for _, mode := range []string{"files", "webtty"} {
		for _, backend := range []string{"webdav", "webrtc"} {
			t.Run(mode+"/"+backend, func(t *testing.T) {
				root := t.TempDir()
				payload := bytes.Repeat([]byte("file-transfer\x00\xff"), 120000)
				name := "résumé #?% &.bin"
				if runtime.GOOS == "windows" {
					name = "résumé #% &.bin"
				}
				if err := os.WriteFile(filepath.Join(root, name), payload, 0o600); err != nil {
					t.Fatal(err)
				}
				var handler http.Handler
				if mode == "files" {
					service, err := fileserver.New(fileserver.Config{Root: root, Backend: backend})
					if err != nil {
						t.Fatal(err)
					}
					defer service.Close()
					handler = service
				} else {
					service, err := webtty.NewFileSystemHandler(&webtty.FileSystemConfig{Root: root, Backend: backend})
					if err != nil {
						t.Fatal(err)
					}
					defer service.(io.Closer).Close()
					handler = service
				}
				started := make(chan struct{}, 2)
				canceled := make(chan struct{}, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("rstream.token") != "fixture-reader" {
						http.Error(w, "Authentication required", http.StatusUnauthorized)
						return
					}
					if r.URL.Query().Get("fixture-cancel") == "1" {
						started <- struct{}{}
						<-r.Context().Done()
						canceled <- struct{}{}
						return
					}
					if backend == "webrtc" && !strings.HasSuffix(r.URL.Path, rtc.Endpoint) {
						http.Error(w, "Direct HTTP data forbidden", http.StatusTeapot)
						return
					}
					handler.ServeHTTP(w, r)
				}))
				defer server.Close()
				url := server.URL + "?rstream.token=fixture-reader"
				writable := mode == "webtty" && backend == "webdav"
				t.Run("CLI", func(t *testing.T) {
					destination := filepath.Join(t.TempDir(), name)
					command := filesystemProcess(t, "webtty", "fs", "--url", url, "download", "/"+name, destination)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("CLI download: %v: %s", err, output)
					}
					assertFilesystemDownload(t, destination, payload)
					command = filesystemProcess(t, "webtty", "fs", "--url", url, "upload", destination, "/cli-upload.bin")
					output, err := command.CombinedOutput()
					if (err == nil) != writable {
						t.Fatalf("CLI upload writable=%v: %v: %s", writable, err, output)
					}
					if writable {
						assertFilesystemDownload(t, filepath.Join(root, "cli-upload.bin"), payload)
					} else if !bytes.Contains(output, []byte("403")) && !bytes.Contains(output, []byte("read-only")) {
						t.Fatalf("CLI did not report a read-only refusal: %s", output)
					}
					command = filesystemProcess(t, "webtty", "fs", "--url", server.URL, "read", "/"+name)
					if output, err := command.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("401")) {
						t.Fatalf("CLI authentication rejection: %v: %s", err, output)
					}
					command = filesystemProcess(t, "webtty", "fs", "--url", url+"&fixture-cancel=1", "read", "/"+name)
					command.Env = append(command.Env, "RSTREAM_FILES_PROCESS_DEADLINE=1")
					if output, err := command.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("context deadline exceeded")) {
						t.Fatalf("CLI cancellation: %v: %s", err, output)
					}
					awaitFilesystemSignal(t, started)
					awaitFilesystemSignal(t, canceled)
				})
				t.Run("MCP", func(t *testing.T) {
					client := newFilesystemRPC(t)
					call := func(tool string, args map[string]string) json.RawMessage {
						return client.call(t, "tools/call", map[string]any{"name": tool, "arguments": args})
					}
					destination := filepath.Join(t.TempDir(), name)
					filesystemToolResult(t, call("rstream_webtty_fs_download", map[string]string{"url": url, "path": "/" + name, "local_path": destination}), false)
					assertFilesystemDownload(t, destination, payload)
					writeResult := call("rstream_webtty_fs_write", map[string]string{"url": url, "path": "/mcp-upload.bin", "content": base64.StdEncoding.EncodeToString(payload), "encoding": "base64"})
					filesystemToolResult(t, writeResult, !writable)
					if !writable && !bytes.Contains(writeResult, []byte("403")) && !bytes.Contains(writeResult, []byte("read-only")) {
						t.Fatalf("MCP did not report a read-only refusal: %s", writeResult)
					}
					if writable {
						assertFilesystemDownload(t, filepath.Join(root, "mcp-upload.bin"), payload)
					}
					result := call("rstream_webtty_fs_read", map[string]string{"url": server.URL, "path": "/" + name})
					filesystemToolResult(t, result, true)
					if !bytes.Contains(result, []byte("401")) {
						t.Fatalf("MCP authentication rejection: %s", result)
					}
					id := client.send(t, "tools/call", map[string]any{"name": "rstream_webtty_fs_read", "arguments": map[string]string{"url": url + "&fixture-cancel=1", "path": "/" + name}})
					awaitFilesystemSignal(t, started)
					client.notify(t, "notifications/cancelled", map[string]any{"requestId": id, "reason": "test cancellation"})
					awaitFilesystemSignal(t, canceled)
					filesystemToolResult(t, call("rstream_webtty_fs_list", map[string]string{"url": url, "path": "/"}), false)
				})
				assertFilesystemDownload(t, filepath.Join(root, name), payload)
			})
		}
	}
}

func awaitFilesystemSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("filesystem request did not start or cancel within the deadline")
	}
}

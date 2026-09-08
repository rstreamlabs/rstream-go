// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/fileserver"
	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

func TestFilesystemWebRTCThroughCLIAndMCP(t *testing.T) {
	for _, mode := range []string{"files", "webtty"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			payload := bytes.Repeat([]byte("shared transport\x00"), 100000)
			name := "résumé #?% &.bin"
			if err := os.WriteFile(filepath.Join(root, name), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			var handler http.Handler
			if mode == "files" {
				service, err := fileserver.New(fileserver.Config{Root: root, Backend: "webrtc"})
				if err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				handler = service
			} else {
				service, err := webtty.NewFileSystemHandler(&webtty.FileSystemConfig{Root: root, Backend: "webrtc"})
				if err != nil {
					t.Fatal(err)
				}
				defer service.(io.Closer).Close()
				handler = service
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("rstream.token") != "fixture-reader" {
					http.Error(w, "Authentication required", http.StatusUnauthorized)
					return
				}
				if !strings.HasSuffix(r.URL.Path, rtc.Endpoint) {
					http.Error(w, "Direct HTTP data is disabled", http.StatusTeapot)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			url := server.URL + "?rstream.token=fixture-reader"
			command := &cobra.Command{}
			command.SetContext(ctx)
			command.Flags().String("url", url, "")
			command.Flags().String("fs-path", "", "")
			command.Flags().String("auth-token-file", "", "")
			client, err := newWebTTYFSClient(command)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			entries, err := client.list(ctx, "/")
			if err != nil || len(entries) != 2 {
				t.Fatalf("CLI listing: entries=%v error=%v", entries, err)
			}
			var output bytes.Buffer
			if err := client.read(ctx, "/"+name, &output); err != nil || !bytes.Equal(output.Bytes(), payload) {
				t.Fatalf("CLI download mismatch: %v", err)
			}
			if err := client.write(ctx, "/new", strings.NewReader("denied")); err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("CLI write accepted: %v", err)
			}
			args := make(map[string]json.RawMessage)
			for key, value := range map[string]string{"url": url, "path": "/" + name, "local_path": filepath.Join(t.TempDir(), name), "content": "denied"} {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				args[key] = encoded
			}
			if _, err := mcpWebTTYFSDownload(ctx, args); err != nil {
				t.Fatal(err)
			}
			var destination string
			if err := json.Unmarshal(args["local_path"], &destination); err != nil {
				t.Fatal(err)
			}
			downloaded, err := os.ReadFile(destination)
			if err != nil || !bytes.Equal(downloaded, payload) {
				t.Fatalf("MCP download mismatch: %v", err)
			}
			for _, operation := range []func(context.Context, map[string]json.RawMessage) (map[string]any, error){mcpWebTTYFSWrite, mcpWebTTYFSMkdir, mcpWebTTYFSDelete} {
				if _, err := operation(ctx, args); err == nil || !strings.Contains(err.Error(), "read-only") {
					t.Fatalf("MCP mutation accepted: %v", err)
				}
			}
			unchanged, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || !bytes.Equal(unchanged, payload) {
				t.Fatalf("source changed: %v", err)
			}
		})
	}
}

func TestFilesystemWebRTCPreparedContextAvoidsControlPlane(t *testing.T) {
	clearRstreamTestEnv(t)
	var calls atomic.Int32
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "control plane forbidden", http.StatusForbidden)
	}))
	defer control.Close()
	token := "fixture." + base64.RawURLEncoding.EncodeToString([]byte(`{"type":"pat","token_endpoint":"restricted-device"}`)) + ".signature"
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "device"}},
		Contexts: []config.Context{{Name: "device", APIURL: control.URL, Engine: "project.engine.example.test:443", ProjectEndpoint: "project", TURNDomain: "relay.example.test", TURNRealm: "realm.example.test", TURNPort: 3478, TURNSPort: 5349, Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{Kind: config.TokenStorageInline, Value: token}}}}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	command := newFilesCmd()
	command.SetContext(t.Context())
	server, err := filesystemRTCConfig(command, "webrtc")
	if err != nil {
		t.Fatal(err)
	}
	if server.ICE == nil {
		t.Fatal("missing ICE provider")
	}
	for range 3 {
		servers, err := server.ICE(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(servers) != 2 || !strings.Contains(servers[0].Username, ":pat:project:restricted-device") || servers[0].Credential == "" || len(servers[1].URLs) != 1 || servers[1].URLs[0] != "stun:relay.example.test:3478" {
			t.Fatal("prepared context did not derive the expected local ICE configuration")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("control plane calls = %d, want zero", calls.Load())
	}
}

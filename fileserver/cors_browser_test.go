//go:build browser

// See LICENSE file in the project root for license information.

package fileserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/httpcors"
)

func TestCORSBrowser(t *testing.T) {
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>CORS test</title>"))
	}))
	defer dashboard.Close()
	untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>Untrusted origin</title>"))
	}))
	defer untrusted.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{Root: root, ReadWrite: true, Password: "browser-test-only", CORS: &httpcors.Config{AllowedOrigins: []string{dashboard.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server := httptest.NewServer(service)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "../test/e2e/files-cors-browser.mjs", server.URL, dashboard.URL, untrusted.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser CORS: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
	data, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(data) != "hello world" {
		t.Fatal("rejected browser request changed the file")
	}
	data, err = os.ReadFile(filepath.Join(root, "uploaded.txt"))
	if err != nil || string(data) != "browser upload" {
		t.Fatal("browser upload was not saved")
	}
}

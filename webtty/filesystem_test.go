// See LICENSE file in the project root for license information.

package webtty

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSystemHandlerServesWebDAVRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	handler, err := NewFileSystemHandler(&FileSystemConfig{Root: root})
	if err != nil {
		t.Fatalf("NewFileSystemHandler returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, WebTTYDefaultFSPath+"/hello.txt", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET returned status %d", response.Code)
	}
	if got := response.Body.String(); got != "hello" {
		t.Fatalf("GET returned body %q want hello", got)
	}
}

func TestFileSystemHandlerReadWriteModeAllowsPUT(t *testing.T) {
	root := t.TempDir()
	handler, err := NewFileSystemHandler(&FileSystemConfig{Root: root})
	if err != nil {
		t.Fatalf("NewFileSystemHandler returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, WebTTYDefaultFSPath+"/created.txt", strings.NewReader("created"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT returned status %d", response.Code)
	}
	got, err := os.ReadFile(filepath.Join(root, "created.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != "created" {
		t.Fatalf("unexpected file content: got %q want created", string(got))
	}
}

func TestFileSystemHandlerRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("Symlink unavailable: %v", err)
	}
	handler, err := NewFileSystemHandler(&FileSystemConfig{Root: root})
	if err != nil {
		t.Fatalf("NewFileSystemHandler returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, WebTTYDefaultFSPath+"/link.txt", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("GET followed symlink outside root: status=%d body=%q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPut, WebTTYDefaultFSPath+"/link.txt", strings.NewReader("changed"))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code >= 200 && response.Code < 300 {
		t.Fatalf("PUT followed symlink outside root: status=%d", response.Code)
	}
	got, err := os.ReadFile(secret)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("outside file changed: %q", string(got))
	}
}

func TestFileSystemHandlerUsesResolvedInternalSymlinkParent(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("Symlink unavailable: %v", err)
	}
	handler, err := NewFileSystemHandler(&FileSystemConfig{Root: root})
	if err != nil {
		t.Fatalf("NewFileSystemHandler returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, WebTTYDefaultFSPath+"/linkdir/created.txt", strings.NewReader("created"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT returned status %d", response.Code)
	}
	got, err := os.ReadFile(filepath.Join(targetDir, "created.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != "created" {
		t.Fatalf("unexpected file content: got %q want created", string(got))
	}
}

func TestFileSystemHandlerReadOnlyModeRejectsWrites(t *testing.T) {
	root := t.TempDir()
	handler, err := NewFileSystemHandler(&FileSystemConfig{Root: root, ReadOnly: true})
	if err != nil {
		t.Fatalf("NewFileSystemHandler returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, WebTTYDefaultFSPath+"/created.txt", strings.NewReader("created"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("PUT returned status %d want %d", response.Code, http.StatusForbidden)
	}
}

func TestFileSystemHandlerMaxUploadSize(t *testing.T) {
	root := t.TempDir()
	maxSize := int64(3)
	handler, err := NewFileSystemHandler(&FileSystemConfig{Root: root, MaxUploadSize: &maxSize})
	if err != nil {
		t.Fatalf("NewFileSystemHandler returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, WebTTYDefaultFSPath+"/big.txt", strings.NewReader("large"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT returned status %d want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestBearerAuthHandler(t *testing.T) {
	token := "secret"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	handler := NewBearerAuthHandler(next, &token, false)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request returned status %d want %d", response.Code, http.StatusUnauthorized)
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated request returned status %d want %d", response.Code, http.StatusOK)
	}
}

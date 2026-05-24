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

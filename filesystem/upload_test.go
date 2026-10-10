// See LICENSE file in the project root for license information.

package filesystem

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebDAVUnknownLengthUploadLimit(t *testing.T) {
	local, _ := testLocal(t, t.TempDir(), Policy{})
	handler, err := NewWebDAV(local, WebDAVConfig{MaxUploadSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/fs/file", strings.NewReader("too large"))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "size limit") {
		t.Fatalf("unbounded upload response: %d %q", response.Code, response.Body.String())
	}
}

func TestWebDAVWriteCannotFollowFilteredSymlink(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "private/secret", "original")
	if err := os.Symlink(filepath.Join("private", "secret"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, handler := testLocal(t, root, Policy{Exclude: []string{"private"}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/fs/alias", strings.NewReader("changed")))
	if response.Code < 400 {
		t.Fatalf("filtered symlink upload accepted: %d", response.Code)
	}
	data, err := os.ReadFile(filepath.Join(root, "private", "secret"))
	if err != nil || string(data) != "original" {
		t.Fatalf("filtered target changed: %q %v", data, err)
	}
}

func TestWebDAVDirectoryMutationPreservesFilteredChildren(t *testing.T) {
	for _, method := range []string{http.MethodDelete, "MOVE", "COPY"} {
		t.Run(method, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "folder/.secret", "original")
			writeFixture(t, root, "source/file", "source")
			_, handler := testLocal(t, root, Policy{HideHidden: true})
			target, destination := "/fs/folder", "/fs/moved"
			if method == "COPY" {
				target, destination = "/fs/source", "/fs/folder"
			}
			request := httptest.NewRequest(method, target, nil)
			request.Header.Set("Destination", destination)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code < 400 {
				t.Fatalf("mutation of filtered subtree accepted: %d", response.Code)
			}
			data, err := os.ReadFile(filepath.Join(root, "folder", ".secret"))
			if err != nil || string(data) != "original" {
				t.Fatalf("filtered target changed: %q %v", data, err)
			}
		})
	}
}

func TestWebDAVMoveCannotHideAnExcludedDestinationChild(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "source/file", "original")
	_, handler := testLocal(t, root, Policy{Exclude: []string{"destination/file"}})
	request := httptest.NewRequest("MOVE", "/fs/source", nil)
	request.Header.Set("Destination", "/fs/destination")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 400 {
		t.Fatalf("moved an excluded destination child: %d", response.Code)
	}
	if data, err := os.ReadFile(filepath.Join(root, "source", "file")); err != nil || string(data) != "original" {
		t.Fatalf("source changed: %q %v", data, err)
	}
}

func TestFilteredDirectoryMutationDepthBound(t *testing.T) {
	root := t.TempDir()
	name := "folder/" + strings.Repeat("child/", 65) + "file"
	writeFixture(t, root, name, "original")
	_, handler := testLocal(t, root, Policy{HideHidden: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("DELETE", "/fs/folder", nil))
	if response.Code < 400 {
		t.Fatalf("unbounded subtree mutation: %d", response.Code)
	}
	if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != "original" {
		t.Fatalf("deep source changed: %q %v", data, err)
	}
}

func TestWebDAVRejectsPartialPUT(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	request := httptest.NewRequest(http.MethodPut, "/fs/file", strings.NewReader("partial"))
	request.Header.Set("Content-Range", "bytes 1-7/8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("partial PUT accepted: %d", response.Code)
	}
	if data, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(data) != "original" {
		t.Fatalf("partial PUT changed target: %q %v", data, err)
	}
}

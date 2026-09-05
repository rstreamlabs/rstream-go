// See LICENSE file in the project root for license information.

package filesystem

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testLocal(t *testing.T, root string, policy Policy) (*Local, http.Handler) {
	t.Helper()
	local, err := Open(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	handler, err := NewWebDAV(local, WebDAVConfig{ReadOnly: policy.ReadOnly, Download: true, BoundedDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	return local, handler
}

func writeFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	target := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadRangesAndConditions(t *testing.T) {
	root := t.TempDir()
	name := "résumé #?% space .txt"
	if runtime.GOOS == "windows" {
		name = "résumé #% space .txt"
	}
	writeFixture(t, root, name, "0123456789")
	_, handler := testLocal(t, root, Policy{ReadOnly: true})
	target := "/fs/" + url.PathEscape(name)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest("GET", target, nil))
	if first.Code != 200 || first.Body.String() != "0123456789" || !strings.HasPrefix(first.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download: %d %s", first.Code, first.Body.String())
	}
	for _, tt := range []struct {
		method, header, value string
		status                int
		body                  string
	}{
		{"GET", "Range", "bytes=4-", 206, "456789"},
		{"HEAD", "", "", 200, ""},
		{"GET", "If-None-Match", first.Header().Get("ETag"), 304, ""},
		{"GET", "Range", "bytes=99-", 416, "invalid range: failed to overlap\n"},
	} {
		t.Run(tt.header+tt.method+tt.value, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, target, nil)
			if tt.header != "" {
				request.Header.Set(tt.header, tt.value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.status || response.Body.String() != tt.body {
				t.Fatalf("%d %q", response.Code, response.Body.String())
			}
		})
	}
	request := httptest.NewRequest("GET", target, nil)
	request.Header.Set("Range", "bytes=4-")
	request.Header.Set("If-Range", "\"stale\"")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.Len() != 10 {
		t.Fatal("stale If-Range must restart the complete file")
	}
}

func TestExposurePolicyAcrossListingDownloadAndArchive(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"visible.txt", ".env", ".git/config", "private/key.txt", "secret.log"} {
		writeFixture(t, root, name, name)
	}
	local, handler := testLocal(t, root, Policy{ReadOnly: true, HideHidden: true, Exclude: []string{"private", "*.log"}})
	for _, name := range []string{".env", ".git/config", "private/key.txt", "secret.log", "../outside"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/fs/"+name, nil))
		if response.Code == 200 {
			t.Fatalf("exposed %q", name)
		}
	}
	request := httptest.NewRequest("PROPFIND", "/fs/", nil)
	request.Header.Set("Depth", "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 207 || !strings.Contains(response.Body.String(), "visible.txt") || strings.Contains(response.Body.String(), "secret.log") || strings.Contains(response.Body.String(), ".env") {
		t.Fatalf("listing: %d %s", response.Code, response.Body.String())
	}
	var data bytes.Buffer
	if err := local.WriteZIP(t.Context(), &data, "/"); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "visible.txt" {
		t.Fatalf("archive exposed excluded entries: %v %v", archive, err)
	}
}

func TestSingleFileAndReadOnlyMethods(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "one.txt", "one")
	writeFixture(t, root, "secret.txt", "secret")
	if excluded, err := Open(filepath.Join(root, "secret.txt"), Policy{AllowFile: true, Exclude: []string{"secret*"}}); err == nil {
		_ = excluded.Close()
		t.Fatal("explicit exclusion was ignored for a single-file share")
	}
	local, handler := testLocal(t, filepath.Join(root, "one.txt"), Policy{AllowFile: true, ReadOnly: true})
	file, err := local.OpenFile(t.Context(), "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	items, err := file.Readdir(0)
	if err != nil || len(items) != 1 || items[0].Name() != "one.txt" {
		t.Fatalf("single file listing: %v %v", items, err)
	}
	for _, method := range []string{"PUT", "POST", "DELETE", "MOVE", "COPY", "MKCOL", "LOCK", "UNLOCK", "PROPPATCH", "PATCH"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/fs/one.txt", nil))
		if response.Code != 403 {
			t.Fatalf("%s: %d", method, response.Code)
		}
	}
	if _, err := local.OpenFile(t.Context(), "secret.txt", os.O_RDONLY, 0); err == nil {
		t.Fatal("single file exposed sibling")
	}
}

func TestSymlinksAndBoundedListing(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFixture(t, root, "target/a.txt", "inside")
	writeFixture(t, outside, "secret.txt", "outside")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	local, handler := testLocal(t, root, Policy{ReadOnly: true, MaxEntries: 1})
	if _, err := local.OpenFile(t.Context(), "escape", os.O_RDONLY, 0); err == nil {
		t.Fatal("symlink escaped")
	}
	file, err := local.OpenFile(t.Context(), "inside/a.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "inside" {
		t.Fatalf("%q %v", data, err)
	}
	directory, err := local.OpenFile(t.Context(), "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, err := directory.Readdir(0); err != ErrListingLimit {
		t.Fatalf("unbounded listing: %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("PROPFIND", "/fs/", nil))
	if response.Code != 403 {
		t.Fatal("unbounded PROPFIND accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := local.WriteZIP(ctx, io.Discard, "/"); err != context.Canceled {
		t.Fatalf("archive ignored cancellation: %v", err)
	}
}

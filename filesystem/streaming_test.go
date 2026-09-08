// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLargeSparseDownloadAndZIPMemoryBound(t *testing.T) {
	if testing.Short() {
		t.Skip("large sparse streaming qualification")
	}
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(4<<30) + 17
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("tail"), size-4); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	local, handler := testLocal(t, root, Policy{ReadOnly: true})
	request := httptest.NewRequest("GET", "/fs/large.bin", nil)
	request.Header.Set("Range", "bytes=-4")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 206 || response.Body.String() != "tail" || response.Header().Get("Content-Range") != "bytes 4294967309-4294967312/4294967313" {
		t.Fatalf("large range: %d %v", response.Code, response.Header())
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	output := &countingWriter{}
	if err := local.WriteZIP(t.Context(), output, "/"); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if output.bytes <= size {
		t.Fatal("ZIP was truncated")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Fatalf("archive allocated %d bytes for one streamed file", allocated)
	}
}

type countingWriter struct{ bytes int64 }

func (w *countingWriter) Write(p []byte) (int, error) { w.bytes += int64(len(p)); return len(p), nil }

func TestConcurrentSymlinkReplacementCannotEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFixture(t, root, "inside", "safe")
	writeFixture(t, outside, "secret", "secret")
	link := filepath.Join(root, "link")
	if err := os.Symlink("inside", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	local, _ := testLocal(t, root, Policy{ReadOnly: true})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			for _, target := range []string{filepath.Join(outside, "secret"), "inside"} {
				_ = os.Remove(link)
				_ = os.Symlink(target, link)
			}
		}
	}()
	defer func() { cancel(); <-done }()
	for range 500 {
		file, err := local.OpenFile(t.Context(), "link", os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		contents, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil || string(contents) != "safe" {
			t.Fatalf("unsafe read: %q %v", contents, err)
		}
	}
}

func TestListingLimitIsAnHTTPErrorAndExclusionsIgnoreCase(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "one", "1")
	writeFixture(t, root, "SECRET", "2")
	_, handler := testLocal(t, root, Policy{ReadOnly: true, MaxEntries: 1, Exclude: []string{"secret"}})
	request := httptest.NewRequest("PROPFIND", "/fs/", nil)
	request.Header.Set("Depth", "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 507 || !strings.Contains(response.Body.String(), "1-entry") {
		t.Fatalf("listing limit: %d %s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/fs/SECRET", nil))
	if response.Code != 404 {
		t.Fatalf("case exclusion: %d", response.Code)
	}
}

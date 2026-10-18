// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fileRequest(handler http.Handler, method, name, body, condition, revision string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/fs/"+name, strings.NewReader(body))
	if condition != "" {
		request.Header.Set(condition, revision)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestGuardedMutationProtocolRefusesUnconditionalOrStaleActions(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	for _, action := range []string{"put", "delete"} {
		for _, condition := range []string{"", `"stale"`} {
			r := httptest.NewRequest("PATCH", "/fs/file", strings.NewReader("changed"))
			r.Header.Set("File-System-Action", action)
			r.Header.Set("If-Match", condition)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			expected := 412
			if condition == "" {
				expected = 400
			}
			if w.Code != expected {
				t.Fatalf("%s %q: %d", action, condition, w.Code)
			}
		}
	}
	if read := fileRequest(handler, "GET", "file", "", "", ""); read.Body.String() != "original" {
		t.Fatal("guarded protocol changed a conflicting resource")
	}
}

func TestReservedUploadNamesAreRejectedRegardlessOfCase(t *testing.T) {
	local, _ := testLocal(t, t.TempDir(), Policy{})
	for _, name := range []string{receiptDirectory, ".RSTREAM-UPLOAD-RECEIPTS/record.json", "nested/.RsTrEaM-UpLoAd-Temporary"} {
		if _, err := local.lexical(name); err == nil {
			t.Fatalf("private upload metadata path admitted: %s", name)
		}
	}
}

func TestConditionalMutationsPreserveConflictingContent(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "file", "original")
			_, handler := testLocal(t, root, Policy{})
			response := fileRequest(handler, method, "file", "replacement", "If-Match", `"stale"`)
			if response.Code != http.StatusPreconditionFailed {
				t.Fatalf("stale mutation: status %d", response.Code)
			}
			data, err := os.ReadFile(filepath.Join(root, "file"))
			if err != nil || string(data) != "original" {
				t.Fatalf("conflicting mutation changed file: %q %v", data, err)
			}
		})
	}
}

func TestConditionalCreateReplaceAndDelete(t *testing.T) {
	_, handler := testLocal(t, t.TempDir(), Policy{})
	created := fileRequest(handler, "PUT", "file", "first", "If-None-Match", "*")
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d", created.Code)
	}
	if response := fileRequest(handler, "PUT", "file", "second", "If-None-Match", "*"); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("create-only overwrote existing file: %d", response.Code)
	}
	read := fileRequest(handler, "GET", "file", "", "", "")
	revision := read.Header().Get("ETag")
	if revision == "" {
		t.Fatal("missing revision")
	}
	if response := fileRequest(handler, "PUT", "file", "second", "If-Match", revision); response.Code != http.StatusCreated {
		t.Fatalf("replace: %d", response.Code)
	}
	if response := fileRequest(handler, "DELETE", "file", "", "If-Match", revision); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("old revision accepted: %d", response.Code)
	}
	read = fileRequest(handler, "GET", "file", "", "", "")
	if response := fileRequest(handler, "DELETE", "file", "", "If-Match", read.Header().Get("ETag")); response.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", response.Code)
	}
	if response := fileRequest(handler, "PUT", "missing", "content", "If-Match", "*"); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("replace-only created a file: %d", response.Code)
	}
}

func TestConditionalConcurrentWritersHaveOneWinner(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	local, first := testLocal(t, root, Policy{})
	second, err := NewWebDAV(local, WebDAVConfig{})
	if err != nil {
		t.Fatal(err)
	}
	revision := fileRequest(first, "GET", "file", "", "", "").Header().Get("ETag")
	start := make(chan struct{})
	results := make(chan int, 8)
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			<-start
			handler := first
			if i%2 != 0 {
				handler = second
			}
			results <- fileRequest(handler, "PUT", "file", fmt.Sprintf("writer-%d", i), "If-Match", revision).Code
		})
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for status := range results {
		if status == http.StatusCreated {
			winners++
		} else if status != http.StatusPreconditionFailed {
			t.Fatalf("unexpected conflict status: %d", status)
		}
	}
	if winners != 1 {
		t.Fatalf("%d writers committed the same revision", winners)
	}
}

func TestInterruptedUploadPreservesExistingContent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	request := httptest.NewRequest("PUT", "/fs/file", io.MultiReader(strings.NewReader("partial"), failingUpload{}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 400 {
		t.Fatalf("interrupted upload accepted: %d", response.Code)
	}
	data, err := os.ReadFile(filepath.Join(root, "file"))
	if err != nil || string(data) != "original" {
		t.Fatalf("interrupted upload changed file: %q %v", data, err)
	}
}

type failingUpload struct{}

func (failingUpload) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestUploadVerifiesDigestAndLengthBeforePublishing(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		contents string
		digest   string
		length   int64
		status   int
	}{
		{"valid", "verified", "verified", 8, http.StatusCreated},
		{"digest", "corrupt!", "verified", 8, http.StatusUnprocessableEntity},
		{"length", "short", "short", 10, http.StatusBadRequest},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "file", "original")
			_, handler := testLocal(t, root, Policy{})
			digest := sha256.Sum256([]byte(scenario.digest))
			request := httptest.NewRequest("PUT", "/fs/file", strings.NewReader(scenario.contents))
			request.ContentLength = scenario.length
			request.Header.Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != scenario.status {
				t.Fatalf("upload status %d: %s", response.Code, response.Body.String())
			}
			want := "original"
			if scenario.status == http.StatusCreated {
				want = scenario.contents
			}
			data, err := os.ReadFile(filepath.Join(root, "file"))
			if err != nil || string(data) != want {
				t.Fatalf("published contents: %q %v", data, err)
			}
			entries, err := os.ReadDir(root)
			count := 0
			for _, entry := range entries {
				if entry.Name() != mutationLockName {
					count++
				}
			}
			if err != nil || count != 1 {
				t.Fatalf("staging file leaked: %v %v", entries, err)
			}
		})
	}
}

func TestUploadKeepsPreviousVersionReadableAndCancelsQueuedMutation(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("PUT", "/fs/file", reader))
		done <- response
	}()
	if _, err := io.WriteString(writer, "partial"); err != nil {
		t.Fatal(err)
	}
	read := fileRequest(handler, "GET", "file", "", "", "")
	if read.Code != http.StatusOK || read.Body.String() != "original" {
		t.Fatalf("reader saw uncommitted contents: %d %q", read.Code, read.Body.String())
	}
	list := httptest.NewRequest("PROPFIND", "/fs/", nil)
	list.Header.Set("Depth", "1")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if strings.Contains(listed.Body.String(), uploadPrefix) {
		t.Fatal("listing exposed an in-flight upload")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("DELETE", "/fs/file", nil).WithContext(ctx)
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, request)
	if refused.Code != http.StatusRequestTimeout {
		t.Fatalf("queued cancelled mutation: %d", refused.Code)
	}
	if err := writer.CloseWithError(io.ErrUnexpectedEOF); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response.Code < 400 {
			t.Fatal("interrupted upload accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("upload did not release its request")
	}
	if response := fileRequest(handler, "PUT", "file", "next", "", ""); response.Code != http.StatusCreated {
		t.Fatalf("mutation admission was leaked: %d", response.Code)
	}
}

func TestConditionSyntaxAndComparison(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	revision := fileRequest(handler, "GET", "file", "", "", "").Header().Get("ETag")
	for _, scenario := range []struct {
		condition, value string
		status           int
	}{
		{"If-Match", "W/" + revision, 412},
		{"If-None-Match", "W/" + revision, 412},
		{"If-Match", `"different,tag", ` + revision, 201},
		{"If-Match", `"unterminated`, 400},
		{"If-Match", revision + ",", 400},
		{"If-Match", "*, " + revision, 400},
	} {
		response := fileRequest(handler, "PUT", "file", "original", scenario.condition, scenario.value)
		if response.Code != scenario.status {
			t.Fatalf("%s %q: %d", scenario.condition, scenario.value, response.Code)
		}
	}
}

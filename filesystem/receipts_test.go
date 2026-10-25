// See LICENSE file in the project root for license information.
//go:build !windows

package filesystem

import (
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
	"strconv"
	"strings"
	"testing"
	"time"
)

const testReceiptID = "upload-recovery-attempt-0001"

func durableRequest(id, contents string) *http.Request {
	r := httptest.NewRequest("PUT", "/fs/file", strings.NewReader(contents))
	r.Method = http.MethodPatch
	r.Header.Set("File-System-Action", "put")
	digest := sha256.Sum256([]byte(contents))
	r.Header.Set("Upload-Id", id)
	r.Header.Set("Upload-Length", strconv.Itoa(len(contents)))
	r.Header.Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":")
	r.Header.Set("If-None-Match", "*")
	return r
}

func readReceipt(t *testing.T, handler http.Handler, id string) UploadReceipt {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/fs/file?upload-receipt="+id, nil))
	if w.Code != 200 {
		t.Fatalf("receipt lookup: %d %s", w.Code, w.Body.String())
	}
	var receipt UploadReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestUploadReceiptSurvivesReplacementAndReopening(t *testing.T) {
	root := t.TempDir()
	_, handler := testLocal(t, root, Policy{})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	first := readReceipt(t, handler, testReceiptID)
	if w.Code != 200 || first.State != "committed" || first.Status != 201 || first.Revision == "" {
		t.Fatalf("upload result: %d %+v", w.Code, first)
	}
	if changed := fileRequest(handler, "PUT", "file", "second", "If-Match", first.Revision); changed.Code != 201 {
		t.Fatalf("replacement: %d", changed.Code)
	}
	_, reopened := testLocal(t, root, Policy{})
	if got := readReceipt(t, reopened, testReceiptID); got != first {
		t.Fatalf("historical receipt changed: %+v", got)
	}
	w = httptest.NewRecorder()
	reopened.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	if w.Code != 200 {
		t.Fatalf("identical retry: %d", w.Code)
	}
	if read := fileRequest(reopened, "GET", "file", "", "", ""); read.Body.String() != "second" {
		t.Fatal("retry silently replayed the earlier upload")
	}
	w = httptest.NewRecorder()
	reopened.ServeHTTP(w, durableRequest(testReceiptID, "changed"))
	if w.Code != 409 {
		t.Fatalf("changed parameters reused ID: %d", w.Code)
	}
	_, restricted := testLocal(t, root, Policy{Exclude: []string{"file"}})
	w = httptest.NewRecorder()
	restricted.ServeHTTP(w, httptest.NewRequest("GET", "/fs/file?upload-receipt="+testReceiptID, nil))
	if w.Code != 404 {
		t.Fatalf("revoked path leaked receipt: %d", w.Code)
	}
}

func TestUploadReceiptRejectsCorruptBodyAndKeepsOriginal(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "original")
	_, handler := testLocal(t, root, Policy{})
	r := durableRequest(testReceiptID, "expected")
	r.Header.Del("If-None-Match")
	r.Header.Set("If-Match", fileRequest(handler, "GET", "file", "", "", "").Header().Get("ETag"))
	r.Body = io.NopCloser(strings.NewReader("modified"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if receipt := readReceipt(t, handler, testReceiptID); receipt.State != "rejected" || receipt.Status != 422 {
		t.Fatalf("corrupt result: %+v", receipt)
	}
	if read := fileRequest(handler, "GET", "file", "", "", ""); read.Body.String() != "original" {
		t.Fatal("corrupt content was published")
	}
}

func TestReceiptMetadataIsPrivateAndDoesNotConsumeListingQuota(t *testing.T) {
	_, handler := testLocal(t, t.TempDir(), Policy{MaxEntries: 1})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	r := httptest.NewRequest("PROPFIND", "/fs/", nil)
	r.Header.Set("Depth", "1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 207 || strings.Contains(w.Body.String(), uploadPrefix) {
		t.Fatalf("metadata affected listing: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/fs/"+filepath.ToSlash(receiptName(testReceiptID)), nil))
	if w.Code != 404 {
		t.Fatalf("private journal accessible: %d", w.Code)
	}
}

type exitBeforeResponse struct{ header http.Header }

func (w exitBeforeResponse) Header() http.Header         { return w.header }
func (w exitBeforeResponse) WriteHeader(int)             { os.Exit(0) }
func (w exitBeforeResponse) Write(p []byte) (int, error) { os.Exit(0); return len(p), nil }

func TestReceiptLostResponseWorker(t *testing.T) {
	root := os.Getenv("RSTREAM_RECEIPT_ROOT")
	if root == "" {
		return
	}
	_, handler := testLocal(t, root, Policy{})
	r := durableRequest(testReceiptID, "first")
	if os.Getenv("RSTREAM_RECEIPT_INTERRUPT") == "1" {
		r.Body = &exitDuringUpload{}
	}
	handler.ServeHTTP(exitBeforeResponse{http.Header{}}, r)
	t.Fatal("worker did not exit at the response boundary")
}

type exitDuringUpload struct{ sent bool }

func (b *exitDuringUpload) Read(p []byte) (int, error) {
	if b.sent {
		os.Exit(0)
	}
	b.sent = true
	return copy(p, "fi"), nil
}
func (*exitDuringUpload) Close() error { return nil }

func TestInterruptedUploadIsRecoveredWithoutReplay(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReceiptLostResponseWorker$")
	child.Env = append(os.Environ(), "RSTREAM_RECEIPT_ROOT="+root, "RSTREAM_RECEIPT_INTERRUPT=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("worker: %v %s", err, output)
	}
	_, handler := testLocal(t, root, Policy{})
	if receipt := readReceipt(t, handler, testReceiptID); receipt.State != "interrupted" {
		t.Fatalf("interrupted result: %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(root, "file")); !os.IsNotExist(err) {
		t.Fatal("interrupted upload was published")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	if w.Code != 200 {
		t.Fatalf("interrupted identity lookup: %d", w.Code)
	}
	if receipt := readReceipt(t, handler, testReceiptID); receipt.State != "interrupted" {
		t.Fatal("retry silently restarted the upload")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != receiptDirectory {
			t.Fatalf("partial staging remains: %s", entry.Name())
		}
	}
}

func TestReceiptIsDurableBeforeAnyResponse(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReceiptLostResponseWorker$")
	child.Env = append(os.Environ(), "RSTREAM_RECEIPT_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("worker: %v %s", err, output)
	}
	_, handler := testLocal(t, root, Policy{})
	if receipt := readReceipt(t, handler, testReceiptID); receipt.State != "committed" {
		t.Fatalf("lost response: %+v", receipt)
	}
}

// Build durable crash-boundary fixtures, then use the real public lookup and
// mutation paths to recover them. No recovery branch invokes the PUT again.
func TestPreparedReceiptCrashBoundaries(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run("external="+strconv.FormatBool(external), func(t *testing.T) {
			policy := Policy{}
			for _, boundary := range []string{"before-rename", "after-rename", "ambiguous"} {
				t.Run(boundary, func(t *testing.T) {
					root := t.TempDir()
					if external {
						policy.StateDir = t.TempDir()
					}
					local, handler := testLocal(t, root, policy)
					r := durableRequest(testReceiptID, "first")
					body, err := newUploadBody(r)
					if err != nil {
						t.Fatal(err)
					}
					record, err := local.newReceipt(r, "file", body)
					if err != nil {
						t.Fatal(err)
					}
					if err := local.beginReceipt(record); err != nil {
						t.Fatal(err)
					}
					record.State, record.Temporary = "prepared", uploadPrefix+"crash-fixture"
					writeFixture(t, root, record.Temporary, "first")
					info, err := os.Stat(filepath.Join(root, record.Temporary))
					if err != nil {
						t.Fatal(err)
					}
					record.FileIdentity, err = uploadFileIdentity(info)
					if err != nil {
						t.Fatal(err)
					}
					if err := local.writeReceiptFile(pendingReceipt, record); err != nil {
						t.Fatal(err)
					}
					if boundary != "before-rename" {
						if err := local.root.Rename(record.Temporary, record.Target); err != nil {
							t.Fatal(err)
						}
					}
					if boundary == "ambiguous" {
						writeFixture(t, root, record.Target, "other")
					}
					// A later mutation must preserve the old outcome before replacing it.
					if result := fileRequest(handler, "PUT", "file", "later", "", ""); result.Code != 201 {
						t.Fatalf("later write: %d", result.Code)
					}
					expected := map[string]string{"before-rename": "interrupted", "after-rename": "committed", "ambiguous": "uncertain"}[boundary]
					if receipt := readReceipt(t, handler, testReceiptID); receipt.State != expected {
						t.Fatalf("%s: %+v", boundary, receipt)
					}
					if read := fileRequest(handler, "GET", "file", "", "", ""); read.Body.String() != "later" {
						t.Fatal("recovery replayed an earlier write")
					}
					if _, err := os.Stat(filepath.Join(root, record.Temporary)); !os.IsNotExist(err) {
						t.Fatalf("staging file remains: %v", err)
					}
				})
			}

		})
	}
}

func TestReceiptCapacityPreservesPriorResultsAndNeverEvicts(t *testing.T) {
	root := t.TempDir()
	_, handler := testLocal(t, root, Policy{})
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, durableRequest(testReceiptID, "first"))
	first := readReceipt(t, handler, testReceiptID)
	if created.Code != 200 || first.State != "committed" {
		t.Fatal("initial upload did not commit")
	}
	// Fill the actual journal to its documented bound without 4096 fsyncs.
	for i := 1; i < receiptLimit; i++ {
		name := filepath.Join(root, receiptDirectory, "retained-"+strconv.Itoa(i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, reopened := testLocal(t, root, Policy{})
	if changed := fileRequest(reopened, "PUT", "file", "second", "If-Match", first.Revision); changed.Code != 201 {
		t.Fatalf("ordinary guarded replacement at capacity: %d", changed.Code)
	}
	newAttempt := httptest.NewRecorder()
	reopened.ServeHTTP(newAttempt, durableRequest("new-upload-at-capacity", "third"))
	if newAttempt.Code != http.StatusInsufficientStorage {
		t.Fatalf("new durable attempt at capacity: %d", newAttempt.Code)
	}
	if got := readReceipt(t, reopened, testReceiptID); got != first {
		t.Fatalf("capacity changed retained evidence: %+v", got)
	}
	repeated := httptest.NewRecorder()
	reopened.ServeHTTP(repeated, durableRequest(testReceiptID, "first"))
	if repeated.Code != 200 {
		t.Fatalf("recovering existing identity at capacity: %d", repeated.Code)
	}
	if read := fileRequest(reopened, "GET", "file", "", "", ""); read.Body.String() != "second" {
		t.Fatal("capacity evicted an identity and allowed replay")
	}
	entries, err := os.ReadDir(filepath.Join(root, receiptDirectory))
	if err != nil || len(entries) != receiptLimit {
		t.Fatalf("journal was purged or exceeded its bound: %d %v", len(entries), err)
	}
}

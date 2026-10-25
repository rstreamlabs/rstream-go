// See LICENSE file in the project root for license information.
//go:build !windows

package filesystem

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestExternalJournalWithReadOnlyExportRoot(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	writeFixture(t, root, "writable/probe", "writable")
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	if err := os.WriteFile(filepath.Join(root, "probe"), nil, 0o600); !os.IsPermission(err) {
		t.Skipf("requires an unprivileged user unable to write the export root: %v", err)
	}
	_, old := testLocal(t, root, Policy{})
	request := durableRequest(testReceiptID, strings.Repeat("x", 32<<10))
	request.URL.Path = "/fs/writable/file"
	w := httptest.NewRecorder()
	old.ServeHTTP(w, request)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "journal is not writable") {
		t.Fatalf("default journal diagnostic: %d %s", w.Code, w.Body.String())
	}
	_, handler := testLocal(t, root, Policy{StateDir: state})
	request = durableRequest(testReceiptID, strings.Repeat("x", 32<<10))
	request.URL.Path = "/fs/writable/file"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var receipt UploadReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("upload: %d %s: %v", w.Code, w.Body.String(), err)
	}
	data, err := os.ReadFile(filepath.Join(root, "writable/file"))
	digest := sha256.Sum256(data)
	if err != nil || w.Code != 200 || receipt.State != "committed" || len(data) != 32<<10 || receipt.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("upload: %+v size=%d error=%v", receipt, len(data), err)
	}
	for _, name := range []string{receiptDirectory, pendingReceipt} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("state written to export root: %s %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(state, stateDirectory, receiptName(testReceiptID))); err != nil {
		t.Fatal(err)
	}
}

func TestExternalJournalAdmissionAndReopening(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	_, handler := testLocal(t, root, Policy{StateDir: state})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	first := readReceipt(t, handler, testReceiptID)
	if w.Code != 200 || first.State != "committed" {
		t.Fatalf("first upload: %+v", first)
	}
	for _, scenario := range []struct {
		name, match, none, body string
		status                  int
	}{
		{name: "existing", none: "*", status: 412},
		{name: "stale", match: `"stale"`, status: 412},
		{name: "digest", match: first.Revision, body: "wrong", status: 422},
		{name: "interrupted", match: first.Revision, body: "short", status: 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			id := "upload-scenario-" + scenario.name
			r := durableRequest(id, "other")
			r.Header.Set("If-Match", scenario.match)
			r.Header.Set("If-None-Match", scenario.none)
			if scenario.body != "" {
				r.Body = io.NopCloser(strings.NewReader(scenario.body))
			}
			if scenario.name == "interrupted" {
				r.Body = io.NopCloser(io.MultiReader(strings.NewReader("par"), failingUpload{}))
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			result := readReceipt(t, handler, id)
			if w.Code != 200 || result.State != "rejected" || result.Status != scenario.status {
				t.Fatalf("rejection: %d %+v", w.Code, result)
			}
			if data, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(data) != "first" {
				t.Fatalf("failed upload changed file: %q %v", data, err)
			}
		})
	}
	_, reopened := testLocal(t, root, Policy{StateDir: state})
	if got := readReceipt(t, reopened, testReceiptID); got != first {
		t.Fatalf("receipt lost after reopening: %+v", got)
	}
	if response := fileRequest(reopened, "PUT", "file", "replacement", "If-Match", first.Revision); response.Code != 201 {
		t.Fatalf("replacement: %d", response.Code)
	}
	w = httptest.NewRecorder()
	reopened.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	if w.Code != 200 || readReceipt(t, reopened, testReceiptID) != first {
		t.Fatal("same identity was replayed")
	}
	w = httptest.NewRecorder()
	reopened.ServeHTTP(w, durableRequest(testReceiptID, "other"))
	if w.Code != 409 {
		t.Fatalf("changed identity parameters: %d", w.Code)
	}
	if data, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(data) != "replacement" {
		t.Fatalf("replayed content: %q %v", data, err)
	}
	_, reader := testLocal(t, root, Policy{StateDir: state, ReadOnly: true})
	if got := readReceipt(t, reader, testReceiptID); got != first {
		t.Fatal("read-only owner lost receipt")
	}
}

func TestExternalJournalOwnership(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	_, handler := testLocal(t, root, Policy{StateDir: state})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, durableRequest(testReceiptID, "first"))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if local, err := Open(t.TempDir(), Policy{StateDir: state}); err == nil {
		local.Close()
		t.Fatal("different exports shared a journal")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	_, aliased := testLocal(t, alias, Policy{StateDir: state})
	if got := readReceipt(t, aliased, testReceiptID); got.State != "committed" {
		t.Fatalf("alias lost receipt: %+v", got)
	}
	_, different := testLocal(t, t.TempDir(), Policy{StateDir: t.TempDir()})
	w = httptest.NewRecorder()
	different.ServeHTTP(w, durableRequest(testReceiptID, "different export"))
	if w.Code != 200 || readReceipt(t, different, testReceiptID).SHA256 == readReceipt(t, handler, testReceiptID).SHA256 {
		t.Fatal("distinct exports collided on an upload identity")
	}
}

func TestExternalJournalRejectsInvalidState(t *testing.T) {
	for _, scenario := range []string{"missing", "file", "symlink", "public", "unbound", "corrupt-owner", "legacy", "readonly-missing"} {
		t.Run(scenario, func(t *testing.T) {
			root, state := t.TempDir(), t.TempDir()
			private := filepath.Join(state, stateDirectory)
			policy := Policy{StateDir: state}
			switch scenario {
			case "missing":
				policy.StateDir = filepath.Join(state, "absent")
			case "file":
				writeFixture(t, state, stateDirectory, "file")
			case "symlink":
				if err := os.Symlink(root, private); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Mkdir(private, 0o755); err != nil {
					t.Fatal(err)
				}
			case "unbound":
				if err := os.Mkdir(private, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, private, pendingReceipt, "unbound")
			case "corrupt-owner":
				if err := os.Mkdir(private, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, private, journalOwnerName, "invalid")
			case "legacy":
				_, handler := testLocal(t, root, Policy{})
				handler.ServeHTTP(httptest.NewRecorder(), durableRequest(testReceiptID, "first"))
			case "readonly-missing":
				policy.ReadOnly = true
			}
			local, err := Open(root, policy)
			if err == nil {
				local.Close()
				t.Fatal("invalid state accepted")
			}
			if !strings.Contains(err.Error(), "prepare upload state directory") {
				t.Fatalf("missing configuration diagnostic: %v", err)
			}
		})
	}
}

func TestExternalJournalIsPrivateAcrossExports(t *testing.T) {
	parent := t.TempDir()
	root, state := filepath.Join(parent, "data"), filepath.Join(parent, "state")
	for _, dir := range []string{root, state} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, handler := testLocal(t, root, Policy{StateDir: state})
	handler.ServeHTTP(httptest.NewRecorder(), durableRequest(testReceiptID, "first"))
	private := filepath.Join(state, stateDirectory)
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	for _, exported := range []string{private, filepath.Join(private, receiptDirectory), filepath.Join(private, receiptName(testReceiptID)), alias} {
		if local, err := Open(exported, Policy{AllowFile: true}); err == nil {
			local.Close()
			t.Fatalf("private state exported directly: %s", exported)
		}
	}
	local, outer := testLocal(t, parent, Policy{})
	for _, target := range []string{"state/" + stateDirectory + "/" + journalOwnerName, "alias/" + journalOwnerName} {
		for _, method := range []string{"GET", "PUT", "DELETE", "PROPFIND"} {
			response := fileRequest(outer, method, target, "corrupt", "", "")
			if response.Code < 400 {
				t.Fatalf("%s exposed private state: %d", method, response.Code)
			}
		}
	}
	for _, method := range []string{"DELETE", "MOVE"} {
		r := httptest.NewRequest(method, "/fs/state", nil)
		r.Header.Set("Destination", "/fs/moved")
		w := httptest.NewRecorder()
		outer.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatalf("%s removed private state through its ancestor", method)
		}
	}
	r := httptest.NewRequest("PROPFIND", "/fs/state", nil)
	r.Header.Set("Depth", "1")
	w := httptest.NewRecorder()
	outer.ServeHTTP(w, r)
	if w.Code != 207 || strings.Contains(w.Body.String(), stateDirectory) {
		t.Fatalf("listing leaked state: %s", w.Body.String())
	}
	var output bytes.Buffer
	if err := local.WriteZIP(t.Context(), &output, "/state"); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil || len(archive.File) != 0 {
		t.Fatalf("archive leaked state: %v", err)
	}
	if readReceipt(t, handler, testReceiptID).State != "committed" {
		t.Fatal("private state changed")
	}
}

func TestExternalJournalCapacityAndPermissions(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	_, handler := testLocal(t, root, Policy{StateDir: state})
	handler.ServeHTTP(httptest.NewRecorder(), durableRequest(testReceiptID, "first"))
	private := filepath.Join(state, stateDirectory)
	for i := 1; i < receiptLimit; i++ {
		writeFixture(t, private, filepath.Join(receiptDirectory, strconv.Itoa(i)), "")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, durableRequest("full-external-journal", "other"))
	if w.Code != 507 || !strings.Contains(w.Body.String(), "capacity") {
		t.Fatalf("capacity: %d %s", w.Code, w.Body.String())
	}
	if readReceipt(t, handler, testReceiptID).State != "committed" {
		t.Fatal("capacity lost receipt")
	}
	for _, err := range []error{syscall.ENOSPC, syscall.EDQUOT, errReceiptCapacity} {
		if code, _ := journalError(fmt.Errorf("journal: %w", err)); code != 507 {
			t.Fatalf("capacity error %v: %d", err, code)
		}
	}
	if code, _ := journalError(io.ErrUnexpectedEOF); code != 503 {
		t.Fatalf("unavailable journal: %d", code)
	}
	if err := os.Chmod(private, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(private, 0o700) })
	if err := os.WriteFile(filepath.Join(private, "probe"), nil, 0o600); !os.IsPermission(err) {
		t.Skip("requires unprivileged user")
	}
	if local, err := Open(root, Policy{StateDir: state}); err == nil || !errors.Is(err, os.ErrPermission) {
		if local != nil {
			local.Close()
		}
		t.Fatalf("unwritable journal accepted at startup: %v", err)
	}
}

func TestExternalJournalWorker(t *testing.T) {
	root := os.Getenv("RSTREAM_EXTERNAL_JOURNAL_ROOT")
	if root == "" {
		return
	}
	_, handler := testLocal(t, root, Policy{StateDir: os.Getenv("RSTREAM_EXTERNAL_JOURNAL_STATE")})
	request := durableRequest(testReceiptID, "first")
	switch os.Getenv("RSTREAM_EXTERNAL_JOURNAL_MODE") {
	case "interrupted":
		request.Body = &exitDuringUpload{}
	case "concurrent":
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		if w.Code != 200 {
			t.Fatalf("concurrent upload: %d %s", w.Code, w.Body.String())
		}
		return
	}
	handler.ServeHTTP(exitBeforeResponse{http.Header{}}, request)
	t.Fatal("worker did not exit")
}

func TestExternalJournalPermissionFailureBeforeContent(t *testing.T) {
	for _, subdir := range []string{".", receiptDirectory} {
		t.Run(subdir, func(t *testing.T) {
			root, state := t.TempDir(), t.TempDir()
			_, handler := testLocal(t, root, Policy{StateDir: state})
			handler.ServeHTTP(httptest.NewRecorder(), durableRequest(testReceiptID, "first"))
			directory := filepath.Join(state, stateDirectory, subdir)
			if err := os.Chmod(directory, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
			if err := os.WriteFile(filepath.Join(directory, "probe"), nil, 0o600); !os.IsPermission(err) {
				t.Skip("requires unprivileged user")
			}
			r := durableRequest("unwritable-journal-new-upload", "other")
			r.URL.Path = "/fs/new-file"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 503 || !strings.Contains(w.Body.String(), "journal is not writable") {
				t.Fatalf("admission: %d %s", w.Code, w.Body.String())
			}
			if _, err := os.Stat(filepath.Join(root, "new-file")); !os.IsNotExist(err) {
				t.Fatalf("content modified without journal: %v", err)
			}
			if readReceipt(t, handler, testReceiptID).State != "committed" {
				t.Fatal("prior receipt lost")
			}
		})
	}
}

type inspectStagingReader struct {
	*strings.Reader
	inspect func()
	once    sync.Once
}

func (r *inspectStagingReader) Read(p []byte) (int, error) {
	r.once.Do(r.inspect)
	return r.Reader.Read(p)
}

func TestExternalJournalAcrossFilesystems(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses the Linux /dev/shm mount")
	}
	state, err := os.MkdirTemp("/dev/shm", "rstream-journal-test-")
	if err != nil {
		t.Skipf("separate writable mount unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	root := t.TempDir()
	dataInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	stateInfo, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if dataInfo.Sys().(*syscall.Stat_t).Dev == stateInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("requires distinct filesystems")
	}
	local, handler := testLocal(t, root, Policy{StateDir: state})
	request := durableRequest(testReceiptID, "first")
	inspected := false
	request.Body = io.NopCloser(&inspectStagingReader{Reader: strings.NewReader("first"), inspect: func() {
		inspected = true
		record, err := local.readReceiptFile(pendingReceipt)
		if err != nil {
			t.Fatal(err)
		}
		if record.Temporary == "" {
			t.Fatal("upload has no staging file")
		}
		info, err := local.root.Stat(record.Temporary)
		if err != nil {
			t.Fatal(err)
		}
		if info.Sys().(*syscall.Stat_t).Dev != dataInfo.Sys().(*syscall.Stat_t).Dev {
			t.Fatal("content staging moved onto journal filesystem")
		}
	}})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if !inspected || w.Code != 200 || readReceipt(t, handler, testReceiptID).State != "committed" {
		t.Fatalf("cross-filesystem upload: %d %s", w.Code, w.Body.String())
	}
	_, reopened := testLocal(t, root, Policy{StateDir: state})
	if readReceipt(t, reopened, testReceiptID).State != "committed" {
		t.Fatal("cross-filesystem receipt lost")
	}
}

func TestExternalJournalProcessesAndCrashRecovery(t *testing.T) {
	for _, mode := range []string{"interrupted", "lost-response", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			root, state := t.TempDir(), t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			count := 1
			if mode == "concurrent" {
				count = 4
			}
			var wg sync.WaitGroup
			for range count {
				wg.Go(func() {
					child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExternalJournalWorker$")
					child.Env = append(os.Environ(), "RSTREAM_EXTERNAL_JOURNAL_ROOT="+root, "RSTREAM_EXTERNAL_JOURNAL_STATE="+state, "RSTREAM_EXTERNAL_JOURNAL_MODE="+mode)
					if output, err := child.CombinedOutput(); err != nil {
						t.Errorf("worker: %v %s", err, output)
					}
				})
			}
			wg.Wait()
			_, reopened := testLocal(t, root, Policy{StateDir: state})
			receipt := readReceipt(t, reopened, testReceiptID)
			expected := "committed"
			if mode == "interrupted" {
				expected = "interrupted"
			}
			if receipt.State != expected {
				t.Fatalf("recovery: %+v", receipt)
			}
			w := httptest.NewRecorder()
			reopened.ServeHTTP(w, durableRequest(testReceiptID, "first"))
			if w.Code != 200 || readReceipt(t, reopened, testReceiptID) != receipt {
				t.Fatal("recovery replayed upload")
			}
			if mode == "interrupted" {
				if _, err := os.Stat(filepath.Join(root, "file")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial upload published: %v", err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), uploadPrefix) {
					t.Fatalf("staging remains: %s", entry.Name())
				}
			}
		})
	}
}

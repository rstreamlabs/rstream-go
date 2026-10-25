// See LICENSE file in the project root for license information.

package filesystem

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// The Linux CI container deliberately exports its actual read-only system root,
// with no privileges/capabilities and only /tmp and /dev/shm writable.
func TestExternalJournalReadOnlySystemRoot(t *testing.T) {
	if os.Getenv("RSTREAM_TEST_READONLY_ROOT") != "1" {
		t.Skip("requires the restricted Linux CI container")
	}
	if os.Geteuid() == 0 {
		t.Fatal("must run as an unprivileged user")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Flags&unix.ST_RDONLY == 0 {
		t.Fatal("system root must be mounted read-only")
	}
	state := t.TempDir()
	target := filepath.Join(t.TempDir(), "file")
	contents := strings.Repeat("x", 32<<10)
	_, handler := testLocal(t, "/", Policy{StateDir: state})
	request := durableRequest(testReceiptID, contents)
	request.URL.Path = "/fs" + target
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var receipt UploadReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != 200 || receipt.State != "committed" {
		t.Fatalf("upload: %d %s %v", w.Code, w.Body.String(), err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != contents {
		t.Fatalf("upload content: size=%d error=%v", len(data), err)
	}
	_, reopened := testLocal(t, "/", Policy{StateDir: state})
	w = httptest.NewRecorder()
	reopened.ServeHTTP(w, httptest.NewRequest("GET", "/fs"+target+"?upload-receipt="+testReceiptID, nil))
	var recovered UploadReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &recovered); err != nil || w.Code != 200 || recovered != receipt {
		t.Fatalf("receipt after reopening: %d %s %v", w.Code, w.Body.String(), err)
	}
}

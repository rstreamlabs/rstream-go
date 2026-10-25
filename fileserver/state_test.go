// See LICENSE file in the project root for license information.
//go:build !rstream_fips

package fileserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/filesystem"
)

func TestUploadStateConfiguration(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	if err := (Config{Root: root, StateDir: state, Backend: "webrtc"}).Validate(); err == nil {
		t.Fatal("WebRTC accepted upload state")
	}
	if runtime.GOOS == "windows" {
		if s, err := New(Config{Root: root, StateDir: state, ReadWrite: true}); err == nil {
			s.Close()
			t.Fatal("unsupported platform accepted external state")
		}
		return
	}
	if s, err := New(Config{Root: root, StateDir: filepath.Join(state, "missing"), ReadWrite: true}); err == nil {
		s.Close()
		t.Fatal("server advertised uploads with an unavailable configured journal")
	}
	s := newTestShare(t, Config{Root: root, StateDir: state, ReadWrite: true})
	r := httptest.NewRequest("PATCH", "/fs/file", strings.NewReader("first"))
	digest := sha256.Sum256([]byte("first"))
	r.Header.Set("File-System-Action", "put")
	r.Header.Set("If-None-Match", "*")
	r.Header.Set("Upload-Id", "fileserver-external-state-0001")
	r.Header.Set("Upload-Length", "5")
	r.Header.Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var receipt filesystem.UploadReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != 200 || receipt.State != "committed" {
		t.Fatalf("upload: %d %s %v", w.Code, w.Body.String(), err)
	}
	reopened := newTestShare(t, Config{Root: root, StateDir: state})
	w = shareRequest(reopened, "GET", "/fs/file?upload-receipt="+receipt.ID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), receipt.SHA256) {
		t.Fatalf("receipt after reopen: %d %s", w.Code, w.Body.String())
	}
	info := shareRequest(s, "GET", InfoPath, "")
	if !s.Info().Capabilities.UploadReceipts || strings.Contains(info.Body.String(), state) {
		t.Fatalf("discovery: %s", info.Body.String())
	}
}

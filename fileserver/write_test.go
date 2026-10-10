// See LICENSE file in the project root for license information.

package fileserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
)

func newTestShare(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func shareRequest(s *Server, method, target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	return response
}

func TestReadWriteAndHeadlessShare(t *testing.T) {
	for _, backend := range []string{"webdav", "webrtc"} {
		for _, writable := range []bool{false, true} {
			if backend == "webrtc" && writable {
				continue
			}
			for _, ui := range []bool{false, true} {
				t.Run(backend+"/write="+strconv.FormatBool(writable)+"/ui="+strconv.FormatBool(ui), func(t *testing.T) {
					cfg := Config{Root: t.TempDir(), Backend: backend, ReadWrite: writable}
					if ui {
						cfg.UI = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "browser") })
					}
					if err := os.WriteFile(filepath.Join(cfg.Root, "file"), []byte("0123456789"), 0o600); err != nil {
						t.Fatal(err)
					}
					s := newTestShare(t, cfg)
					response := shareRequest(s, http.MethodGet, "/", "")
					if ui && response.Code != 200 || !ui && response.Code != 404 {
						t.Fatalf("UI: %d", response.Code)
					}
					response = shareRequest(s, http.MethodGet, InfoPath, "")
					var info Info
					if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
						t.Fatal(err)
					}
					if info.Capabilities.Write != writable || info.Backend != backend || !info.Capabilities.Resume || !info.Capabilities.Archive || (info.MaxUploadSize > 0) != writable {
						t.Fatalf("capabilities: %+v", info)
					}
					for _, target := range []string{ArchivePath, "/fs/file", FSPath + rtc.Endpoint} {
						if response := shareRequest(s, http.MethodGet, target, ""); response.Code != 200 {
							t.Fatalf("endpoint %s: %d %s", target, response.Code, response.Body.String())
						}
					}
					response = shareRequest(s, http.MethodPut, "/fs/new", "uploaded")
					if writable && response.Code != 201 || !writable && response.Code != 403 {
						t.Fatalf("PUT: %d %s", response.Code, response.Body.String())
					}
					request := httptest.NewRequest(http.MethodGet, "/fs/file", nil)
					request.Header.Set("Range", "bytes=4-")
					response = httptest.NewRecorder()
					s.ServeHTTP(response, request)
					if response.Code != 206 || response.Body.String() != "456789" {
						t.Fatalf("resume: %d %s", response.Code, response.Body.String())
					}
				})
			}
		}
	}
}

func TestInvalidWriteConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{Backend: "webrtc", ReadWrite: true},
		{Backend: "other"},
		{ReadWrite: true, MaxUploadSize: -1},
		{MaxUploadSize: 1024},
	} {
		cfg.Root = t.TempDir()
		if s, err := New(cfg); err == nil {
			_ = s.Close()
			t.Fatalf("invalid configuration accepted: %+v", cfg)
		}
	}
}

func TestWebDAVShareMutationsAndAuthentication(t *testing.T) {
	root := t.TempDir()
	s := newTestShare(t, Config{Root: root, ReadWrite: true, Password: "fixture-password"})
	for _, operation := range []struct{ method, path, body, destination string }{
		{"MKCOL", "/fs/folder", "", ""},
		{"PUT", "/fs/folder/file", "uploaded", ""},
		{"COPY", "/fs/folder/file", "", "/fs/folder/copy"},
		{"MOVE", "/fs/folder/copy", "", "/fs/moved"},
		{"DELETE", "/fs/folder", "", ""},
	} {
		for _, authenticated := range []bool{false, true} {
			request := httptest.NewRequest(operation.method, operation.path, strings.NewReader(operation.body))
			request.Header.Set("Destination", operation.destination)
			if authenticated {
				request.SetBasicAuth("rstream", "fixture-password")
			}
			response := httptest.NewRecorder()
			s.ServeHTTP(response, request)
			if !authenticated && response.Code != 401 || authenticated && (response.Code < 200 || response.Code >= 300) {
				t.Fatalf("%s authenticated=%v: %d %s", operation.method, authenticated, response.Code, response.Body.String())
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "moved")); err != nil || string(data) != "uploaded" {
		t.Fatalf("move: %q %v", data, err)
	}
}

func TestUploadLimitsAndInterruption(t *testing.T) {
	for _, mode := range []string{"known-size", "unknown-size", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "file"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			s := newTestShare(t, Config{Root: root, ReadWrite: true, MaxUploadSize: 4})
			request := httptest.NewRequest(http.MethodPut, "/fs/file", strings.NewReader("12345"))
			wantStatus, wantContents := 413, "original"
			if mode == "unknown-size" {
				request.ContentLength = -1
				wantContents = "1234"
			}
			if mode == "interrupted" {
				request.ContentLength = -1
				request.Body = io.NopCloser(io.MultiReader(strings.NewReader("12"), failedUpload{}))
				wantStatus, wantContents = 405, "12"
			}
			response := httptest.NewRecorder()
			s.ServeHTTP(response, request)
			if response.Code != wantStatus {
				t.Fatalf("upload: %d %s", response.Code, response.Body.String())
			}
			if data, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(data) != wantContents {
				t.Fatalf("partial upload: %q %v", data, err)
			}
			if response := shareRequest(s, "PUT", "/fs/file", "1234"); response.Code != 201 {
				t.Fatalf("retry: %d %s", response.Code, response.Body.String())
			}
			if response := shareRequest(s, "GET", "/fs/file", ""); response.Body.String() != "1234" {
				t.Fatalf("retry contents: %q", response.Body.String())
			}
		})
	}
}

type failedUpload struct{}

func (failedUpload) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSingleFileWriteDoesNotExposeSiblings(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"shared", "sibling"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestShare(t, Config{Root: filepath.Join(root, "shared"), ReadWrite: true})
	if response := shareRequest(s, "PUT", "/fs/shared", "changed"); response.Code != 201 {
		t.Fatalf("shared upload: %d", response.Code)
	}
	for _, name := range []string{"sibling", "new"} {
		if response := shareRequest(s, "PUT", "/fs/"+name, "changed"); response.Code < 400 {
			t.Fatalf("wrote sibling %s: %d", name, response.Code)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "sibling")); err != nil || string(data) != "original" {
		t.Fatalf("sibling changed: %q %v", data, err)
	}
}

func TestWriteHonorsOSPermissions(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("Unix permissions require an unprivileged Unix user")
	}
	root := t.TempDir()
	file := filepath.Join(root, "locked")
	if err := os.WriteFile(file, []byte("original"), 0o400); err != nil {
		t.Fatal(err)
	}
	s := newTestShare(t, Config{Root: root, ReadWrite: true})
	if response := shareRequest(s, "PUT", "/fs/locked", "changed"); response.Code < 400 {
		t.Fatalf("overwrote read-only file: %d", response.Code)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0o700)
	if response := shareRequest(s, "PUT", "/fs/new", "new"); response.Code < 400 {
		t.Fatalf("wrote read-only directory: %d", response.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected upload: %v", err)
	}
}

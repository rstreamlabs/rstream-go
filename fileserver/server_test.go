// See LICENSE file in the project root for license information.

package fileserver

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordProtectsEverySurface(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Root: root, Password: "test-only-share-password", UI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("browser")) })})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, target := range []string{"/", InfoPath, ArchivePath, "/fs/hello.txt"} {
		t.Run(target, func(t *testing.T) {
			for _, authenticated := range []bool{false, true} {
				request := httptest.NewRequest("GET", target, nil)
				if authenticated {
					request.SetBasicAuth("rstream", "test-only-share-password")
				}
				response := httptest.NewRecorder()
				s.ServeHTTP(response, request)
				if !authenticated && (response.Code != 401 || response.Header().Get("WWW-Authenticate") == "") {
					t.Fatalf("surface bypassed auth: %d", response.Code)
				}
				if authenticated && response.Code != 200 {
					t.Fatalf("authorized surface: %d %s", response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestShareMetadataAndZIP(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetAccess("rstream")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", InfoPath, nil))
	var info Info
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Access != "rstream" || info.Capabilities.Write || info.Capabilities.E2EE || !info.Capabilities.Archive || strings.Contains(response.Body.String(), root) {
		t.Fatalf("metadata: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", ArchivePath, nil))
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil || len(archive.File) != 1 || archive.File[0].Name != "empty/" {
		t.Fatalf("archive: %v %v", archive, err)
	}
	response = httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("HEAD", ArchivePath, nil))
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatal("HEAD must not generate an archive")
	}
}

func TestArchiveConcurrencyLimit(t *testing.T) {
	s, err := New(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.archives <- struct{}{}
	s.archives <- struct{}{}
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", ArchivePath, nil))
	if response.Code != 429 || response.Header().Get("Retry-After") == "" {
		t.Fatal("archive admission exceeded its resource bound")
	}
}

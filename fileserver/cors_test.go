// See LICENSE file in the project root for license information.

package fileserver

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/httpcors"
)

func TestCORSHeadlessAuthenticatedWebDAV(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Root: root, ReadWrite: true, Password: "test-password", CORS: &httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, target := range []string{InfoPath, FSPath + "/", FSPath + "/hello.txt", ArchivePath, "/missing"} {
		for _, method := range []string{"GET", "HEAD", "PUT", "OPTIONS"} {
			req := httptest.NewRequest(method, target, strings.NewReader("replacement"))
			req.Header.Set("Origin", "https://dashboard.example")
			res := httptest.NewRecorder()
			server.ServeHTTP(res, req)
			if res.Code != 401 || res.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example" {
				t.Fatalf("%s %s: authentication/CORS = %d %v", method, target, res.Code, res.Header())
			}
		}
	}
	for _, method := range []string{"PROPFIND", "PUT", "GET"} {
		req := httptest.NewRequest("OPTIONS", FSPath+"/hello.txt", nil)
		req.Header.Set("Origin", "https://dashboard.example")
		req.Header.Set("Access-Control-Request-Method", method)
		req.Header.Set("Access-Control-Request-Headers", "authorization, depth, range, if-range, if-match, if-none-match, content-digest")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != 204 {
			t.Fatalf("preflight %s: %d %s", method, res.Code, res.Body.String())
		}
	}
	for _, tc := range []struct {
		method, target, body string
		status               int
	}{
		{"HEAD", InfoPath, "", 200},
		{"GET", InfoPath, "", 200},
		{"PROPFIND", FSPath + "/", "", 207},
		{"PUT", FSPath + "/uploaded.txt", "uploaded", 201},
		{"GET", FSPath + "/missing.txt", "", 404},
	} {
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		req.Header.Set("Origin", "https://dashboard.example")
		req.Header.Set("Depth", "1")
		req.SetBasicAuth("rstream", "test-password")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != tc.status || res.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example" {
			t.Fatalf("%s %s: %d %v %s", tc.method, tc.target, res.Code, res.Header(), res.Body.String())
		}
	}
	req := httptest.NewRequest("GET", FSPath+"/hello.txt", nil)
	req.Header.Set("Origin", "https://dashboard.example")
	req.Header.Set("Range", "bytes=1-4")
	req.SetBasicAuth("rstream", "test-password")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != 206 || res.Body.String() != "ello" || res.Header().Get("Content-Range") != "bytes 1-4/11" || !strings.Contains(res.Header().Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Fatalf("partial download: %d %v %q", res.Code, res.Header(), res.Body.String())
	}
	req = httptest.NewRequest("PUT", FSPath+"/hello.txt", strings.NewReader("forbidden"))
	req.Header.Set("Origin", "https://untrusted.example")
	req.SetBasicAuth("rstream", "test-password")
	res = httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != 403 || res.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("untrusted origin was allowed")
	}
	data, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(data) != "hello world" {
		t.Fatal("rejected requests changed the filesystem")
	}
}

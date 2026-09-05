// See LICENSE file in the project root for license information.

package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedUIIntegrityAndCSP(t *testing.T) {
	html, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := assets.ReadFile("assets/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta manifest
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ tag, hash string }{{"script", meta.ScriptSHA256}, {"style", meta.StyleSHA256}} {
		match := regexp.MustCompile("(?s)<" + target.tag + ">(.*?)</" + target.tag + ">").FindSubmatch(html)
		if len(match) != 2 {
			t.Fatalf("missing %s", target.tag)
		}
		digest := sha256.Sum256(match[1])
		if base64.StdEncoding.EncodeToString(digest[:]) != target.hash {
			t.Fatalf("CSP hash does not authorize the embedded %s", target.tag)
		}
	}
	handler := Handler()
	for _, encoding := range []string{"", "gzip", "gzip;q=0", "*;q=1, gzip;q=0"} {
		request := httptest.NewRequest("GET", "/", nil)
		request.Header.Set("Accept-Encoding", encoding)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || !strings.Contains(response.Header().Get("Content-Security-Policy"), meta.ScriptSHA256) {
			t.Fatalf("invalid response: %d", response.Code)
		}
		if encoding == "gzip" {
			reader, err := gzip.NewReader(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil || !bytes.Equal(data, html) {
				t.Fatal("compressed artifact differs from HTML")
			}
		} else if !bytes.Equal(response.Body.Bytes(), html) {
			t.Fatal("gzip was used when explicitly refused")
		}
	}
}

// See LICENSE file in the project root for license information.

package filesystem

import (
	"encoding/xml"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/webdav"
)

func TestRevisionDistinguishesEqualSizeAndTimestamp(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "first")
	_, handler := testLocal(t, root, Policy{})
	old := fileRequest(handler, "GET", "file", "", "", "").Header().Get("ETag")
	name := filepath.Join(root, "file")
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "file", "other")
	if err := os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	current := fileRequest(handler, "GET", "file", "", "", "").Header().Get("ETag")
	if current == old {
		t.Fatal("different contents reuse the same revision")
	}
	if response := fileRequest(handler, "PUT", "file", "replacement", "If-Match", old); response.Code != 412 {
		t.Fatalf("stale revision accepted: %d", response.Code)
	}
	updated := fileRequest(handler, "PUT", "file", "replacement", "If-Match", current)
	if updated.Code != 201 {
		t.Fatalf("current revision refused: %d", updated.Code)
	}
	if read := fileRequest(handler, "GET", "file", "", "", ""); read.Header().Get("ETag") != updated.Header().Get("ETag") {
		t.Fatal("PUT and GET disagree on the published revision")
	}
}

func TestListingAndOpenDownloadUseContentRevision(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "first")
	local, handler := testLocal(t, root, Policy{})
	old := fileRequest(handler, "GET", "file", "", "", "").Header().Get("ETag")
	request := httptest.NewRequest("PROPFIND", "/fs/", strings.NewReader(`<propfind xmlns="DAV:"><prop><getetag/></prop></propfind>`))
	request.Header.Set("Depth", "1")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, request)
	var listing struct {
		Responses []struct {
			Href       string `xml:"href"`
			Properties []struct {
				ETag string `xml:"prop>getetag"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(listed.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, response := range listing.Responses {
		if response.Href == "/fs/file" {
			for _, prop := range response.Properties {
				if prop.ETag == old {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("listing lost the content revision: %s", listed.Body.String())
	}
	opened, err := local.OpenFile(t.Context(), "file", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if result := fileRequest(handler, "PUT", "file", "second", "If-Match", old); result.Code != 201 {
		t.Fatalf("replace: %d", result.Code)
	}
	revision, err := info.(webdav.ETager).ETag(t.Context())
	if err != nil || revision != old {
		t.Fatalf("open download revision changed: %q %v", revision, err)
	}
	data, err := io.ReadAll(opened)
	if err != nil || string(data) != "first" {
		t.Fatalf("open download bytes changed: %q %v", data, err)
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"
)

func TestResolveWebTTYFSBaseURL(t *testing.T) {
	tests := []struct {
		fsPath     string
		raw        string
		wantBase   string
		wantTarget string
	}{
		{raw: "rstrm://shell", wantBase: "http://shell/fs", wantTarget: "shell"},
		{raw: "rstrm://shell/dav", wantBase: "http://shell/dav", wantTarget: "shell"},
		{fsPath: "/dav", raw: "rstrm://shell", wantBase: "http://shell/dav", wantTarget: "shell"},
		{raw: "ws://127.0.0.1:8080", wantBase: "http://127.0.0.1:8080/fs", wantTarget: ""},
		{fsPath: "/dav", raw: "ws://127.0.0.1:8080", wantBase: "http://127.0.0.1:8080/dav", wantTarget: ""},
		{raw: "wss://shell.example", wantBase: "https://shell.example/fs", wantTarget: ""},
		{raw: "https://shell.example/fs", wantBase: "https://shell.example/fs", wantTarget: ""},
		{raw: "https://shell.example/base", wantBase: "https://shell.example/base/fs", wantTarget: ""},
		{raw: "https://shell.example/base?rstream.token=token", wantBase: "https://shell.example/base/fs?rstream.token=token", wantTarget: ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			base, target, err := resolveWebTTYFSBaseURL(tt.raw, tt.fsPath)
			if err != nil {
				t.Fatalf("resolveWebTTYFSBaseURL returned error: %v", err)
			}
			if base != tt.wantBase || target != tt.wantTarget {
				t.Fatalf("resolveWebTTYFSBaseURL(%q) = (%q, %q), want (%q, %q)", tt.raw, base, target, tt.wantBase, tt.wantTarget)
			}
		})
	}
}

func TestResolveWebTTYFSBaseURLRejectsMissingRstreamHost(t *testing.T) {
	_, _, err := resolveWebTTYFSBaseURL("rstrm:///fs", "")
	if err == nil {
		t.Fatal("expected missing host to be rejected")
	}
}

func TestWebTTYFSRemoteURLEncodesPath(t *testing.T) {
	client := &webTTYFSClient{baseURL: "http://shell/fs"}
	if got := client.remoteURL("/dir/file with space.txt"); got != "http://shell/fs/dir/file%20with%20space.txt" {
		t.Fatalf("unexpected remote URL: %q", got)
	}
}

func TestNormalizeWebTTYFSPath(t *testing.T) {
	tests := map[string]string{
		"":             "/",
		".":            "/",
		"file.txt":     "/file.txt",
		"/dir/../file": "/file",
	}
	for input, want := range tests {
		if got := normalizeWebTTYFSPath(input); got != want {
			t.Fatalf("normalizeWebTTYFSPath(%q) = %q want %q", input, got, want)
		}
	}
}

func TestWebDAVItemsFromMultiStatus(t *testing.T) {
	raw := webDAVMultiStatus{
		Responses: []webDAVResponse{
			{Href: "/fs/", Propstat: []webDAVPropstat{{Status: "HTTP/1.1 200 OK", Prop: webDAVProp{ResourceType: webDAVResourceType{Collection: &struct{}{}}}}}},
			{Href: "/fs/file.txt", Propstat: []webDAVPropstat{{Status: "HTTP/1.1 200 OK", Prop: webDAVProp{ContentLength: "12", LastModified: "today"}}}},
		},
	}
	items := webDAVItemsFromMultiStatus(raw)
	if len(items) != 2 {
		t.Fatalf("expected two items, got %#v", items)
	}
	if items[0].Path != "/" || items[0].Kind != "directory" {
		t.Fatalf("unexpected directory item: %#v", items[0])
	}
	if items[1].Path != "/file.txt" || items[1].Kind != "file" || items[1].Size == nil || *items[1].Size != 12 || !strings.Contains(items[1].Modified, "today") {
		t.Fatalf("unexpected file item: %#v", items[1])
	}
}

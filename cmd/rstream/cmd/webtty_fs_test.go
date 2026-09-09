// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestFilesystemTunnelDiscoverySupportsStandaloneFilesAndWebTTY(t *testing.T) {
	tunnels := []rstream.TunnelInventory{
		{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("files-id"), Name: rstream.StringPtr("exports"), Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP)}},
		{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("webtty-id"), Name: rstream.StringPtr("shell"), Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY), Labels: map[string]string{webtty.WebTTYCapabilitiesLabelKey: "exec,fs", webtty.WebTTYFSPathLabelKey: "/workspace"}}},
		{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("exec-id"), Name: rstream.StringPtr("exec-only"), Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP), Labels: map[string]string{webtty.WebTTYApplicationProtocolKey: webtty.WebTTYApplicationProtocol}}},
	}
	for _, test := range []struct {
		target string
		id     string
		path   string
	}{
		{target: "exports", id: "files-id"},
		{target: "files-id", id: "files-id"},
		{target: "shell", id: "webtty-id", path: "/workspace"},
		{target: "webtty-id", id: "webtty-id", path: "/workspace"},
	} {
		t.Run(test.target, func(t *testing.T) {
			server, err := selectWebTTYFilesystemServer(tunnels, test.target)
			if err != nil || server == nil {
				t.Fatalf("filesystem discovery = %v, %v", server, err)
			}
			if server.TunnelID != test.id || trimOptionalString(server.FSPath) != test.path {
				t.Fatalf("discovery selected another tunnel or filesystem path: %+v", server)
			}
			if err := validateWebTTYFilesystemCapability(test.target, server); err != nil {
				t.Fatal(err)
			}
		})
	}
	exec, err := selectWebTTYFilesystemServer(tunnels, "exec-only")
	if err != nil || validateWebTTYFilesystemCapability("exec-only", exec) == nil {
		t.Fatalf("HTTP WebTTY tunnel without filesystem capability was accepted: %v", err)
	}
	duplicate := tunnels[0]
	duplicate.ID = rstream.StringPtr("other-files-id")
	if _, err := selectWebTTYFilesystemServer(append(tunnels, duplicate), "exports"); err == nil {
		t.Fatal("ambiguous filesystem tunnel name was accepted")
	}
	if server, err := selectWebTTYFilesystemServer(tunnels, "missing"); err != nil || server != nil {
		t.Fatalf("missing filesystem tunnel = %v, %v", server, err)
	}
}

func TestFilesystemExplicitTargetDoesNotRequireInventory(t *testing.T) {
	server, err := resolveWebTTYFilesystemServer(t.Context(), nil, "restricted-tunnel", true)
	if err != nil || server == nil || webTTYRuntimeDialTarget(server) != "restricted-tunnel" {
		t.Fatalf("explicit filesystem target = %v, %v", server, err)
	}
}

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

func TestValidateWebTTYFilesystemCapability(t *testing.T) {
	tests := []struct {
		name       string
		serverInfo *webtty.ServerInfo
		want       string
	}{
		{name: "offline", want: "not online"},
		{name: "exec only", serverInfo: &webtty.ServerInfo{Capabilities: []string{webtty.WebTTYCapabilityExec}}, want: "does not advertise a filesystem sidecar"},
		{name: "filesystem", serverInfo: &webtty.ServerInfo{Capabilities: []string{webtty.WebTTYCapabilityExec, webtty.WebTTYCapabilityFS}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebTTYFilesystemCapability("shell", tt.serverInfo)
			if tt.want == "" && err != nil {
				t.Fatalf("validateWebTTYFilesystemCapability() error = %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("validateWebTTYFilesystemCapability() error = %v, want %q", err, tt.want)
			}
		})
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

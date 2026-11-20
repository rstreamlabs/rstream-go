# Embedded file HTTP service

Use `fileserver.New` to serve an export inside a Go application. Pass the
returned `*fileserver.Server` to an HTTP server as its handler. After the
HTTP server stops accepting requests and drains its handlers, close the file
server to release its backend and filesystem root.

The [file sharing guide](../docs/008-file-sharing.md) covers the CLI and
choosing the access policy. The [filesystem package](../filesystem/README.md)
defines mutation conditions and recoverable uploads.

For a writable, API-only WebDAV export, pass this configuration to
`fileserver.New`. The export must exist, and the process needs permission
to serve and modify it.

```go
config := fileserver.Config{
	Root:          "./maintenance",
	ReadWrite:     true,
	MaxUploadSize: 128 << 20,
}
```

Without `ReadWrite` the server opens the export in read-only mode.
Attach an authorization policy to your HTTP service, or set `Password`
for HTTP Basic auth and use HTTPS. An rstream HTTP tunnel can instead
authenticate at its published entrypoint.

## Serve a WebRTC export

Prepare the [Go application's reviewed ICE dependency](../docs/021-filesystem-webrtc.md#build-an-application-using-this-backend)
before building with WebRTC. A published CLI build already has this step.

This test exposes one disposable file on loopback and compares its bytes
through the filesystem client. It uses read-only WebRTC and local ICE
candidates. The listener accepts signaling and rejects direct HTTP downloads,
so the response must come from the peer connection.

```go
func TestWebRTCExport(t *testing.T) {
	root := t.TempDir()
	want := []byte("name,enabled\nexample,true\n")
	if err := os.WriteFile(filepath.Join(root, "report.csv"), want, 0600); err != nil {
		t.Fatal(err)
	}

	share, err := fileserver.New(fileserver.Config{
		Root: root, Backend: filesystem.BackendWebRTC,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer share.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fs/report.csv" {
			http.Error(w, "download over WebRTC", http.StatusForbidden)
			return
		}
		share.ServeHTTP(w, r)
	}))
	defer server.Close()

	client := filesystem.NewHTTPClient(server.URL+"/fs", server.Client())
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/fs/report.csv", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download: %s", response.Status)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("downloaded %q, want %q", got, want)
	}
}
```

Put the test in a Go test file. Import `bytes`, `context`, `io`, `net/http`,
`net/http/httptest`, `os`, `path/filepath`, `testing`, `time`, and the SDK's
`fileserver` and `filesystem` packages. Run `go test -run TestWebRTCExport`.

For peers across networks, configure `Config.RTC.ICE` to return fresh
credentials. For example, an authenticated rstream client can fetch relay
configuration for each new lease. Keep the parent client open until after the
file server closes.

```go
fileConfig.RTC.ICE = func(ctx context.Context) ([]webrtc.ICEServer, error) {
	relay, err := config.CreateTURNCredentialsFromEnv(ctx, config.TURNCredentialsEnvOptions{
		Client: projectClient,
	})
	if err != nil {
		return nil, err
	}
	return []webrtc.ICEServer{{
		URLs: relay.URLs, Username: relay.Username, Credential: relay.Credential,
	}}, nil
}
```

The callback uses `github.com/pion/webrtc/v4` and the SDK's `config` package.
See [TURN issuance](../docs/015-engine-mtls-turn.md#request-turn-credentials-in-go)
for project scope and permissions. Protect the HTTP signaling endpoints as
you would other file access.

## Read from a share

`filesystem.NewHTTPClient(baseURL, client)`
selects WebDAV or WebRTC from the server's advertised capabilities.
Set `baseURL` to the filesystem endpoint, including `/fs` for a default
share. For example, use `https://share.example.com/fs`, then request
`https://share.example.com/fs/report.csv`. If the server advertises a
different filesystem path, use that prefix. Supply a signaling client
with the credentials and private dialer the share requires.
Close each download response body after reading it.

## Routes and clients

| Path | Contract |
| --- | --- |
| `/` | Serves the handler from `Config.UI` when supplied. |
| `/_rstream/files/v1/info` | GET/HEAD metadata: version, name (no absolute path), kind, backend, `fs_path`, optional `archive_path`, effective access, capabilities, and `max_upload_size_bytes` when writable. `capabilities.write` reflects `--read-write`; `resume` means download resume. Version remains 1. |
| `/fs/…` | WebDAV OPTIONS, PROPFIND Depth 0/1, GET and HEAD; writes are forbidden by default. `--read-write` enables WebDAV mutation methods including PUT, MKCOL, COPY, MOVE and DELETE. |
| `/_rstream/files/v1/archive?path=/folder` | GET/HEAD streamed directory ZIP; identical exposure rules. |

Paths passed to the JS backend are decoded logical paths; encode each URL segment once. Individual downloads support Range, ETag, Last-Modified and conditional requests. Use `curl -C - -o backup.tar.zst 'https://<share>/fs/backup.tar.zst'` to resume; use validators such as `If-Range` when the source may have changed. Downloads use attachment disposition and do not render shared HTML/SVG as trusted UI.

When using `filesystem.Local` directly, close it after all requests stop. Actual operations remain rooted under concurrent symlink replacement. In-root symlinks remain compatible with WebTTY; outside links, special files, traversal and excluded targets are rejected. The process needs OS permission to read the source. This is not an isolation boundary against another local process modifying that source.

Listing rejects directories with more than 10,000 raw entries, including filtered entries (HTTP 507). Missing/infinite PROPFIND depth is forbidden, request XML is capped at 64 KiB, ZIP allows two concurrent streams, at most 100,000 entries and depth 64. A directory archive cannot resume an interrupted transfer. Retry the download if the server closes the connection midstream.

For a filesystem alongside a terminal, use `webtty.NewFileSystemHandler`
and close the returned handler through `io.Closer` after serving. WebDAV supports writable
exports. WebRTC is read-only. See [WebTTY filesystem configuration](../docs/webtty/000-server-configuration.md#publication-and-filesystem-options)
for sidecar compatibility and CLI defaults.

## Enforcing CORS

For a standalone Go HTTP server, set `fileserver.Config.CORS` to `&httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}}`. `fileserver.New` applies WebDAV defaults and wraps every route, including password-authentication errors. When publishing through an rstream tunnel, put the policy returned by `fileserver.NormalizeCORS` in `TunnelProperties.HTTPCORS` and leave the local `fileserver.Config.CORS` nil, as the CLI does. Engine then owns the policy, including edge-authentication errors. Set `Password` when a directly exposed server requires password protection; CORS itself is not authentication.

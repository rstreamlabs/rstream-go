# File sharing

`rstream files [path]` publishes a file or directory through one HTTPS tunnel. The path defaults to the current directory. It runs an embedded HTTP service, read-only by default, directly on the tunnel listener; there is no Python process, local port, Node runtime, or runtime asset download.

```sh
rstream files ./exports
rstream files ./backup.tar.zst --password
rstream files ./exports --exclude '*.log' --exclude private --rstream-auth
rstream files ./exports --password-file /path/outside/share/password -o json
```

Stop the foreground process to close the share. Automatic reconnection follows `forward`; use `--no-retry` to stop after a disconnect. Files are read live, without uploading a stored copy. Changes to the source are visible on refresh. This is not a filesystem snapshot: changing a file during a download or ZIP can interrupt or alter that transfer.

File sharing is unavailable in the restricted FIPS build for both WebDAV and WebRTC, including the WebTTY filesystem. See the [FIPS compatibility table](010-fips-140-3-profile.md#file-sharing-compatibility).

## Command surface

| Option | Behavior |
| --- | --- |
| `--backend webdav\|webrtc` | Select WebDAV (default) or read-only WebRTC using the current rstream project’s STUN/TURN. |
| `--read-write` | Enable WebDAV uploads, replacement, directory creation, copy, move and deletion. Default: disabled. Rejected with WebRTC. |
| `--max-upload-size <bytes>` | Per-request upload limit; requires `--read-write`. Default: 67108864 (64 MiB); must be greater than zero. |
| `--no-ui` | Return 404 for the embedded page while keeping transfer, archive and discovery endpoints available. Works with both backends. |
| `--include-hidden` | Include dotfiles and dot-directories; excluded by default for directory shares. |
| `--exclude <glob>` | Repeatable, case-insensitive Go `path.Match` glob. Without `/`, matches basenames at any depth; with `/`, matches root-relative paths. Matching directories exclude descendants. `**` has no special recursive meaning. |
| `--password` | Prompt without echo for a local HTTP Basic password. |
| `--password-file <path>` | Read a password from a file or `-` for stdin. One final CRLF/LF is removed; 1–4096 bytes, no embedded newline or NUL. Keep the password file outside the shared root. |
| `--username <name>` | Basic username, default `rstream`; requires a password option. |
| `--rstream-auth` | Request edge account authentication (Pro/Enterprise). |
| `--token-auth` | Request edge token authentication, including on Basic; intended for authenticated HTTP/WebDAV clients. |
| `--cors-allow-origin`, `--cors-allow-method`, `--cors-allow-header`, `--cors-expose-header`, `--cors-allow-credentials`, `--cors-max-age` | Explicit browser-origin policy; see below. |
| `--host`, `--name`, `--label` | Existing published tunnel domain/name/labels. |
| `--output`, `-o` | `text`, `json`, `xterm`, `none`. JSON status contains `files` metadata and the normal forwarding URL. `none` hides terminal output only; it does not disable the web page. |
| `--retry`, `--no-retry`, `--retry-interval` | Existing reconnection controls; interval in milliseconds, default 5000. |

Global configuration, context, region, transport and logging options continue to apply, including mTLS with an external signer. `files` is an imperative command: the shared context YAML supplies connection/authentication settings, but there is no declarative `files` YAML resource or Docker-label equivalent. A directly selected regular file explicitly shares that file, including a dotfile; hidden filters apply to directory shares. Explicit exclusions also apply to single-file shares; selecting an excluded file is an error. Its siblings are never listed or downloadable.

## Use a dashboard from another origin

Enable CORS explicitly for the dashboard's origin. It works with the WebDAV backend, including `--no-ui`:

```bash
rstream files ./exports --read-write --no-ui --token-auth \
  --cors-allow-origin https://dashboard.example
```

The share remains authenticated. Use an authorized delegated tunnel token on each actual request, either in `Authorization: Bearer …` or in the `rstream.token` query parameter. A valid browser preflight can succeed without a token, but it cannot read or modify files. Token expiry, resource restrictions and permissions still apply to the subsequent operation.

Files supplies WebDAV methods and request headers (including Authorization, Depth, Destination, Overwrite, conditional headers, Range and If-Range). It exposes download and resume metadata including Content-Disposition, Content-Range, Accept-Ranges, ETag, Last-Modified and Content-Length. The policy covers `/_rstream/files/v1/info`, `/fs/`, archives and errors. Write methods still require `--read-write`; CORS does not enable writes. Repeat `--cors-allow-origin` to add origins, and use the shared `--cors-allow-method`, `--cors-allow-header`, `--cors-expose-header`, `--cors-allow-credentials` and `--cors-max-age` options to customize the policy. An explicitly supplied list replaces that list's Files defaults.

CORS is available with the WebDAV backend. It cannot be combined with `--backend webrtc`, which requires same-origin access.

For a standalone Go HTTP server, set `fileserver.Config.CORS` to `&httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}}`. `fileserver.New` applies WebDAV defaults and wraps every route, including password-authentication errors. When publishing through an rstream tunnel, put the policy returned by `fileserver.NormalizeCORS` in `TunnelProperties.HTTPCORS` and leave the local `fileserver.Config.CORS` nil, as the CLI does. Engine then owns the policy, including edge-authentication errors. Set `Password` when a directly exposed server requires password protection; CORS itself is not authentication.

## Authentication

With no auth flag, inherit project policy. When it permits public access, CLI and browser explicitly identify the share as public. Account auth is not enabled automatically because it is unavailable on Basic. A local password works on every plan and protects UI, metadata, WebDAV and ZIP. It uses the browser's HTTP Basic login prompt.

Local Basic and edge auth both use `Authorization` and cannot be combined. Conflicting explicit flags are rejected locally; a conflicting effective project policy produces a nonretryable error. There is no fallback to public access. Account auth uses browser cookies. Token clients must send the token on every request; the browser UI does not collect or persist tokens.

HTTPS protects transport to the edge, and the tunnel protects the edge-to-agent hop. WebDAV is **not end-to-end encrypted** and uses normal project traffic quota. The optional WebRTC backend encrypts the peer connection and carries file bytes directly between peers when possible; TURN relays when necessary and uses the project's separate TURN quota. Files are not separately encrypted with a recipient-held key. See the [WebRTC protocol](011-filesystem-webrtc.md) for authentication, resource limits and browser compatibility.

## CLI and MCP downloads

The existing filesystem client commands also read standalone shares:

```sh
rstream files ./exports --name exports
rstream webtty fs --url rstrm://exports download /report.csv ./report.csv
```

The CLI resolves a WebTTY or HTTP tunnel through Engine inventory, then dials it privately even when it has a published URL. It discovers WebDAV or WebRTC over that connection. For a token restricted to stream access, add `--no-discovery` and supply the exact tunnel id or name; `--fs-path` selects a path other than `/fs`. This explicit path does not require inventory or control-plane access. Server authentication and read-only policy still apply.

Local MCP filesystem tools accept the same `rstrm://` target and use private dialing without inventory discovery. Supply the advertised `fs_path` when it differs from `/fs`. Both surfaces enforce the standalone share's configured write policy. Existing `webtty fs upload` and MCP write operations work when `--read-write` is enabled.

## Read-write service without a web page

```sh
rstream files ./maintenance --read-write --no-ui --token-auth --host maintenance.example.com --name maintenance --max-upload-size 134217728
rstream webtty fs --url rstrm://maintenance upload ./update.bin /update.bin
```

Use the tunnel's exact name or id with the filesystem client. The shared root must already exist. Read-only remains the default; `--read-write` is explicitly rejected with `--backend webrtc`. `--no-ui` is independent of terminal output, authentication and write access. Reconnection retains all these options and the existing filesystem service.

Go embedders can use `fileserver.Config{Root: "./maintenance", ReadWrite: true, MaxUploadSize: 134217728}` with `fileserver.New`. A zero-valued `ReadWrite` stays read-only; omitting `UI` exposes only the API. `Config.Validate` checks transfer options without filesystem or network access.

The browser UI still only browses and downloads, even on a writable share. Uploads and other mutations are available through WebDAV clients, the filesystem CLI and MCP. With public access, any client that can reach the share can use its enabled write operations. Authentication protects the same endpoints in either mode.

Uploads stream to a private sibling file with bounded memory. A successful PUT publishes the complete file by renaming it into place. Readers retain the previous file until that commit. An interrupted request, upload limit, failed write or digest mismatch preserves the original; creating a new file does not expose a fragment. The destination must be writable, and replacement preserves its permission bits. Replacement changes the inode, so existing open handles and hard links still refer to the previous file. Staging names beginning with `.rstream-upload-` are reserved and unavailable through the share.

The 64 MiB default limit can be increased with `--max-upload-size`; it limits each request body, not total disk usage or server-side copies. Both known-length and chunked oversized bodies return HTTP 413. Partial PUT requests with `Content-Range` return HTTP 400. Uploads are not resumable: retry the entire PUT. An optional `Content-Digest: sha-256=:BASE64_DIGEST:` validates the content before publication; malformed digests return 400 and mismatches return 422. A missing response does not establish whether the rename happened; this interface does not yet provide durable mutation receipts.

WebDAV mutations honor `If-Match` and `If-None-Match` against the resource's ETag. A stale revision or failed create-only condition returns 412 without changing the resource. Mutation admission is serialized for all handlers sharing one `filesystem.Local`, including paths that alias the same resource and ancestor moves/deletions. Waiting requests respect cancellation; reads continue while an upload is staged. ETags retain the existing modification-time/size format. This is not coordination with other processes or independently opened roots; application writers must not concurrently modify these files when relying on conditional writes. COPY still uses the existing WebDAV copy behavior and is not an atomic PUT. Crash recovery, durable receipts and a content-based revision scheme are separate from these request-lifetime guarantees.

HTTP `Range`/`If-Range` and the `resume` capability describe downloads only. ZIP downloads must still restart after interruption.

The process needs OS permissions for the requested operation. Hidden paths and exclusions apply to writes as well as reads, including symlink targets. Removing, moving or replacing a directory that contains filtered children is rejected. Filtered subtree checks examine at most 10,000 entries and 64 levels; larger mutations must be split. A selected single-file share permits changes only to that file, never its siblings. Capabilities advertise enabled protocol operations; they do not override OS permissions or filters.

## HTTP and backend contract

| Path | Contract |
| --- | --- |
| `/` | Embedded UI with same-origin assets and requests, no CDN; 404 with `--no-ui`. |
| `/_rstream/files/v1/info` | GET/HEAD metadata: version, name (no absolute path), kind, backend, `fs_path`, optional `archive_path`, effective access, capabilities, and `max_upload_size_bytes` when writable. `capabilities.write` reflects `--read-write`; `resume` means download resume. Version remains 1. |
| `/fs/…` | WebDAV OPTIONS, PROPFIND Depth 0/1, GET and HEAD; writes are forbidden by default. `--read-write` enables WebDAV mutation methods including PUT, MKCOL, COPY, MOVE and DELETE. |
| `/_rstream/files/v1/archive?path=/folder` | GET/HEAD streamed directory ZIP; identical exposure rules. |

Paths passed to the JS backend are decoded logical paths; encode each URL segment once. Individual downloads support Range, ETag, Last-Modified and conditional requests. Use `curl -C - -o backup.tar.zst 'https://<share>/fs/backup.tar.zst'` to resume; use validators such as `If-Range` when the source may have changed. Downloads use attachment disposition and do not render shared HTML/SVG as trusted UI.

`filesystem.Local` owns an `os.Root`; close it after all requests stop. Actual operations remain rooted under concurrent symlink replacement. In-root symlinks remain compatible with WebTTY; outside links, special files, traversal and excluded targets are rejected. The process needs OS permission to read the source. This is not an isolation boundary against another local process modifying that source.

Listing rejects directories with more than 10,000 raw entries, including filtered entries (HTTP 507). Missing/infinite PROPFIND depth is forbidden, request XML is capped at 64 KiB, UI renders 100 entries per page. ZIP allows two concurrent streams, at most 100,000 entries and depth 64; cycles fail. ZIP uses store mode and ZIP64, with a 64 KiB content buffer and bounded directory/central-directory metadata. File contents never become a browser Blob or accumulate in server memory. ZIP is not resumable; an error after headers aborts the connection instead of finalizing an incomplete archive.

The shared Go filesystem and selected transport also back `webtty.NewFileSystemHandler`; existing config, paths, writes and upload limit remain supported with WebDAV. `webtty server --fs-root ./exports --fs-backend webrtc` selects read-only WebRTC; all writes fail with 403, without WebDAV fallback. That returned handler implements `io.Closer`; embedders must close it after serving. WebTTY's filesystem remains incompatible with WebTTY E2E payload encryption.

The JS package `@rstreamlabs/filesystem` defines `FileSystemBackend` (list/stat/readStream/archiveStream/downloadURL) independently from `WebDAVFileSystem`. `WebTTYFileSystem` preserves its existing exports through a compatibility wrapper. Capabilities describe transport functionality and encryption separately, while `RemoteFileSystem` discovers WebDAV or WebRTC and `WebRTCFileSystem` explicitly requires WebRTC. Both reuse the same operations.

## UI regeneration

UI source lives in `rstream-nextjs/src/components/files-browser`, styles in `src/styles/files-browser.css`. From that worktree:

```sh
npm run files-ui:build -- --go-assets-dir=../rstream-go/fileserver/ui/assets
```

Commit HTML, gzip and manifest together. The manifest pins the contract version, source fingerprint, content hash and inline CSP hashes. `fileserver/ui` verifies integrity in tests; the generator enforces a 150 KiB gzip budget. Normal Go builds use `go:embed` only. Review UI changes before publishing either repository. See Next.js `docs/file-browser-ui.md` for SDK packaging and release order.

## Verification

Run the browser regression with `go test -tags browser ./fileserver -run TestCORSBrowser`. It requires Node.js with built-in WebSocket support and Chrome; set `RSTREAM_CHROME_BIN` when Chrome is not installed at the default macOS path. Missing browser dependencies fail the test.

Run `go test ./...`, `go test -race ./...`, `go vet ./...`. Filesystem tests cover encoded names, exclusions, readonly methods, single files, concurrent symlink replacement, ranges, cancellation and a streamed sparse file over 4 GiB. Server tests cover auth on all surfaces, write capabilities, optional UI, upload limits, interrupted uploads, OS permissions, ZIP and concurrency. CLI tests cover password input, option validation, write-mode status, effective policy, CLI/MCP writes and handler shutdown. UI tests check gzip, integrity, CSP and negotiation. Run native filesystem tests on Linux/macOS/Windows in release CI; cross-compilation alone does not qualify native behavior.

# Share files over HTTPS

`rstream files [path]` publishes a file or directory through one HTTPS tunnel. The path defaults to the current directory. It runs an embedded HTTP service, read-only by default, directly on the tunnel listener; there is no Python process, local port, Node runtime, or runtime asset download.

[Select a project](002-cli-workflow.md), then create a small directory with
a file you intend to share. Run this from its parent directory:

```sh
rstream files ./exports --password
```

Open the printed URL and sign in with the username `rstream` and the password
you chose in the terminal. Verify that you can download the file.

Stop the foreground process to close the share. Automatic reconnection follows `forward`; use `--no-retry` to stop after a disconnect. Files are read live, without uploading a stored copy. Changes to the source are visible on refresh. This is not a filesystem snapshot: changing a file during a download or ZIP can interrupt or alter that transfer.

File sharing is unavailable in the restricted FIPS build for both WebDAV and WebRTC, including the WebTTY filesystem. See the [FIPS compatibility table](017-fips-140-3-profile.md#file-sharing-compatibility).

## Share and access options

| Option | Behavior |
| --- | --- |
| `--backend webdav\|webrtc` | Select WebDAV (default) or read-only WebRTC using the current rstream project’s STUN/TURN. |
| `--read-write` | Enable WebDAV uploads, replacement, directory creation, copy, move and deletion. Default: disabled. Rejected with WebRTC. |
| `--max-upload-size <bytes>` | Per-request upload limit; requires `--read-write`. Default: 67108864 (64 MiB); must be greater than zero. |
| `--state-dir <path>` | Existing persistent directory dedicated to the export’s private upload journal. Overrides `RSTREAM_FILES_STATE_DIR`; otherwise the journal stays at the export root. WebDAV on platforms supporting durable uploads only. |
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

HTTPS protects transport to the edge, and the tunnel protects the edge-to-agent hop. WebDAV is **not end-to-end encrypted** and uses normal project traffic quota. The optional WebRTC backend encrypts the peer connection and carries file bytes directly between peers when possible; TURN relays when necessary and uses the project's separate TURN quota. Files are not separately encrypted with a recipient-held key. See [WebRTC file access](021-filesystem-webrtc.md) for authentication, resource limits and browser compatibility.

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

A successful upload replaces the destination atomically. If the request is
interrupted before publication or its content digest fails, the previous file
remains available. A lost response can leave the publication outcome unknown.
Use a durable upload ID when the caller must recover that outcome after a restart.

The per-request upload limit defaults to 64 MiB. Oversized uploads return HTTP
413. A PUT request replaces the whole file. Use a content digest to verify the
received bytes and an ETag precondition to guard against concurrent changes.
Other mutations can wait behind a slow upload to the same export. Independent
reads can continue while the upload is staged.

### Recoverable uploads

A client that needs to recover the result of an interrupted upload should first
check whether the server advertises durable upload receipts. Give each upload
its own ID. After a lost response, look up that ID to check the outcome. Reusing
an ID with the same request returns its original receipt. Start a new attempt
with a new ID after a confirmed interruption.

See the [upload receipt reference](../filesystem/README.md#recoverable-uploads)
for HTTP headers, status codes and the receipt format.

### Upload state outside the export

By default, durable uploads keep `.rstream-upload-receipts`, `.rstream-upload-pending` and journal temporary files at the export root, which must also permit file locking. Consequently, a writable destination under a non-writable or confined export root is insufficient. Select a separate persistent directory when the server cannot write or lock the root:

```sh
mkdir -m 700 /writable-volume/rstream-state
rstream files /srv/exports --read-write --no-ui --token-auth --state-dir /writable-volume/rstream-state
# Alternatively, set RSTREAM_FILES_STATE_DIR=/writable-volume/rstream-state.
```

The directory must already exist. The CLI flag overrides the environment variable; an explicitly empty flag is rejected. The library does not read this environment variable: embedders set `fileserver.Config.StateDir` or `filesystem.Policy.StateDir` when opening a `filesystem.Local`. Use a persistent directory with write access.

Each export needs one dedicated, persistent state directory used by every
process serving that export. The daemon checks ownership and write access
before opening a writable share. External journal storage is unavailable
on Windows.

Keep the same directory when restarting. Changing or removing the state can
lose the ability to recover earlier uploads. Shut down all writers before
moving an existing journal. For the storage and locking contract, see the
[filesystem maintainer reference](../filesystem/README.md#durable-upload-state).

The process also needs permission to write to the exported destination. Hidden
paths and exclusions still apply. Large directory mutations can require smaller
operations when subtree inspection reaches its limits.

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

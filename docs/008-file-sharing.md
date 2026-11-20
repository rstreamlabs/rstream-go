# Share files over HTTPS

`rstream files [path]` serves a file or directory from your machine through a published HTTPS
tunnel. The path defaults to the current directory. Files are read-only by
default and remain on your machine until a client downloads them.

Select a [CLI context](002-cli-workflow.md#connect-the-cli). Use a small
directory containing a file you intend to share. Access is decided before
opening the share.

| Project policy | Access to use |
| --- | --- |
| A disposable project that allows public requests | Add a local password with `--password`. |
| A project that requires edge authentication | Use its required sign-in or tunnel token. The file server's local password cannot be combined with edge authentication. |

Start the password-protected share from the parent of `exports`.

```sh
rstream files ./exports --password
```

Open the printed URL. Sign in as `rstream` with the password you entered and
download the sample file. A fresh request without credentials should be
denied. Stop the process with Ctrl+C to close the share.

For a managed share that uses tokens, pass `--token-auth` in place of
`--password`. Request the file at `/fs/report.csv` on the printed HTTPS
endpoint with a [scoped bearer token](https://rstream.io/docs/tunnels/http-tunnels/authentication#token-authentication).
Use the name of a file in your export. The authorized request returns the
file. Without the token, the published endpoint returns HTTP 401.

The Go client and CLI can also read this standalone share through the same
filesystem client used by WebTTY. A WebTTY server is only needed when the
files belong to its sidecar.

The share reads the directory live. Changes appear on refresh and can affect
a download in progress. Use `--no-retry` to stop after a disconnect.

File sharing is unavailable in the restricted FIPS build for both WebDAV and WebRTC, including the WebTTY filesystem. See the [FIPS compatibility table](017-fips-140-3-profile.md#file-sharing-compatibility).

## Share without a public hostname

`files` always creates a published endpoint. To share over a private
connection, use a [read-only private WebTTY sidecar](https://rstream.io/docs/webtty/quickstart#add-a-filesystem-sidecar). The WebTTY account also runs terminal
commands. Use an operator identity allowed to reach that host. For a
Go service that only serves files, embed the
[Filesystem HTTP handler](../fileserver/README.md) on a private tunnel.

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
rstream files ./exports --no-ui --token-auth \
  --cors-allow-origin https://dashboard.example
```

The share remains authenticated. Use an authorized delegated tunnel token on each actual request, either in `Authorization: Bearer …` or in the `rstream.token` query parameter. A valid browser preflight can succeed without a token, but it cannot read or modify files. Token expiry, resource restrictions and permissions still apply to the subsequent operation.

WebDAV permits the browser to send the methods and headers needed for file
operations. Custom lists replace those defaults. Use the
[HTTP CORS settings](005-http-tunnels.md#cross-origin-browser-access-cors)
for overrides. The WebRTC backend uses same-origin access.

## Authentication

With no auth flag, inherit project policy. When it permits public access, CLI and browser explicitly identify the share as public. Account auth is not enabled automatically because it is unavailable on Basic. A local password works on every plan and protects UI, metadata, WebDAV and ZIP. It uses the browser's HTTP Basic login prompt.

Local Basic and edge auth both use `Authorization` and cannot be combined. Conflicting explicit flags are rejected locally; a conflicting effective project policy produces a nonretryable error. There is no fallback to public access. Account auth uses browser cookies. Token clients must send the token on every request; the browser UI does not collect or persist tokens.

HTTPS protects transport to the edge, and the tunnel protects the edge-to-agent hop. WebDAV is **not end-to-end encrypted** and uses normal project traffic quota. The optional WebRTC backend encrypts the peer connection and carries file bytes directly between peers when possible; TURN relays when necessary and uses the project's separate TURN quota. Files are not separately encrypted with a recipient-held key. See [WebRTC file access](021-filesystem-webrtc.md) for authentication, resource limits and browser compatibility.

## CLI and MCP downloads

Use a managed project with HTTP token authentication for this example. Put
a sample `report.csv` in `./exports`. Start the token-protected share in one
terminal and download it from an authenticated rstream context in another.
Replace the destination path if it already exists.

```sh
rstream files ./exports --name exports --token-auth
rstream webtty fs --url rstrm://exports download /report.csv ./report.csv
```

Check that the downloaded file matches the original. Stop the share with
Ctrl+C when finished.

`--token-auth` protects the share's published HTTPS endpoint. With
`rstrm://`, the CLI dials through the Engine using its own rstream
credential. The Engine checks stream permission and tunnel scope. Add
`--no-discovery` and the exact tunnel name or ID when the credential has
stream access without inventory rights. Use `--fs-path` for a filesystem
path other than `/fs`. The file server still applies its own local password
and write policy when configured.

Local MCP filesystem tools accept the same `rstrm://` target and use private dialing without inventory discovery. Supply the advertised `fs_path` when it differs from `/fs`. Both surfaces enforce the standalone share's configured write policy. Existing `webtty fs upload` and MCP write operations work when `--read-write` is enabled.

## Read-write service without a web page

Use a token-protected managed project. Create the export directory on the
server and prepare a sample `update.bin` on the client. Start the share
before uploading and stop it with Ctrl+C after checking the file in the
export directory. Add `--host` only if you need a project-controlled domain.

```sh
rstream files ./maintenance --read-write --no-ui --token-auth \
  --name maintenance --max-upload-size 134217728
rstream webtty fs --url rstrm://maintenance upload ./update.bin /update.bin
```

Use the tunnel's exact name or id with the filesystem client. The shared root must already exist. Read-only remains the default; `--read-write` is explicitly rejected with `--backend webrtc`. `--no-ui` is independent of terminal output, authentication and write access. Reconnection retains all these options and the existing filesystem service.

The browser UI still only browses and downloads, even on a writable share. Uploads and other mutations are available through WebDAV clients, the filesystem CLI and MCP. With public access, any client that can reach the share can use its enabled write operations. Authentication protects the same endpoints in either mode.

A successful upload replaces the destination atomically. If the request is
interrupted before publication or its content digest fails, the previous file
remains available. A lost response can leave the publication outcome unknown.
Use a durable upload ID when the caller must recover that outcome after a restart.

The per-request upload limit defaults to 64 MiB. Oversized uploads return HTTP
413. A PUT request replaces the whole file. Use a content digest to verify the
received bytes and an ETag precondition to guard against concurrent changes.

### Recoverable uploads

Use [durable upload receipts](../filesystem/README.md#recoverable-uploads)
when a client must recover the result after losing a response. First check
that the server advertises the capability. Look up the original upload ID
before deciding whether another upload is needed.

### Upload state outside the export

Keep the upload journal in one persistent directory per export. Use an
external state directory when the export's security policy prevents journal
files or locking in the shared root.

```sh
mkdir -m 700 /writable-volume/rstream-state
rstream files /srv/exports --read-write --no-ui --token-auth --state-dir /writable-volume/rstream-state
```

Every process serving the same export must use the same location after
restart. `--state-dir` overrides `RSTREAM_FILES_STATE_DIR`. The directory
must already exist and be writable by the service account. External journal
storage is unavailable on Windows. See [durable upload state](../filesystem/README.md#durable-upload-state)
when moving existing upload history or configuring an embedded server.

## HTTP and backend contract

Use the [Go file-server reference](../fileserver/README.md) to embed the
service or implement a client against its HTTP and capability contracts.

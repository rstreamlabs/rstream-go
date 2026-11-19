# Embedded file HTTP service

Use `fileserver.New` to serve an export inside a Go application. The
[file sharing guide](../docs/008-file-sharing.md) covers the CLI and choosing
the access policy. The [filesystem package](../filesystem/README.md)
defines mutation conditions and recoverable uploads.

## Routes and clients

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

## Enforcing CORS

For a standalone Go HTTP server, set `fileserver.Config.CORS` to `&httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}}`. `fileserver.New` applies WebDAV defaults and wraps every route, including password-authentication errors. When publishing through an rstream tunnel, put the policy returned by `fileserver.NormalizeCORS` in `TunnelProperties.HTTPCORS` and leave the local `fileserver.Config.CORS` nil, as the CLI does. Engine then owns the policy, including edge-authentication errors. Set `Password` when a directly exposed server requires password protection; CORS itself is not authentication.

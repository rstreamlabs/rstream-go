# Filesystem writes and recovery

The filesystem package owns export-wide mutation coordination and upload
recovery. See [file sharing](../docs/008-file-sharing.md) for the CLI, its access
policy and the upload receipt contract.

## Mutation admission

WebDAV mutations honor `If-Match` and `If-None-Match` against the resource's ETag. A stale revision or failed create-only condition returns 412 without changing the resource. Mutation admission covers all handlers and independently opened services for the same exported root, including aliases within that root and ancestor moves/deletions. A cancellable OS lock holds the precondition and mutation together; process exit releases it. Unix locks the rooted directory inode. Windows retains a reserved `.rstream-upload-mutation-lock` file, which is never listed or remotely accessible. The lock remains held throughout body reception and publication: a slow upload delays mutations to other paths in the same export. Waiting requests respect cancellation; reads continue while an upload is staged. This is namespace serialization, not parallel per-file upload admission. An unavailable OS lock refuses mutation rather than proceeding without coordination.

Regular-file ETags identify the SHA-256 of the content. GET, PUT, PROPFIND and conditional mutations use the same revision; equal sizes and timestamps do not mask changed bytes. Hashing uses bounded memory and respects request cancellation. Existence-only conditions (`If-Match: *` and `If-None-Match: *`) use metadata without reading the content. Conditions containing entity tags hash the current content once per admission; no size-and-timestamp cache substitutes for that comparison. GET hashes its open descriptor, keeping its revision and bytes together during an atomic replacement. Directory ETags still use modification time and size. A revision is not a persistent identifier for the file or its name.

The namespace lock coordinates these services, not arbitrary local writers or separately exported overlapping roots. Keep writable export namespaces under one ownership discipline; application writers must not concurrently modify them when relying on conditional writes. COPY still uses the existing WebDAV copy behavior and is not an atomic PUT.

## Durable upload state

The service creates a private `.rstream-upload-state` child with owner-only permissions. It binds the journal to the canonical export root and its filesystem identity, rejects reuse by another export, and checks journal writes and synchronization before opening a writable service. A missing, inaccessible, invalid or unsupported configured state directory fails startup. Read-only services can reopen existing state to retrieve receipts. This option is unavailable on Windows until durable directory synchronization is supported there.

All processes serving the same export **must use the same persistent state directory**, including after restart. New state directories also coordinate mutations there, so an AppArmor profile may deny locking the export root while allowing the private state directory. All mutation paths, including ordinary conditional writes, share this coordination. The state directory must survive for as long as upload IDs can be retried; do not use an automatically cleaned `/tmp` directory for durable operation. Changing or deleting the directory can discard idempotence history. A read-only export has no writable place to remember the selected external path, so independent processes using different state directories cannot detect every configuration mismatch. There is no additional global registry or hidden writable-path dependency. Deploy the state location as part of the export's persistent configuration.

Previously created external journals retain their original export-root locking behavior, preserving coordination with older processes and their receipts. They are not silently upgraded while writers may still be active. New journals use owner format version 2, which older SDKs reject. Moving an existing journal to private coordination requires an offline migration with all writers stopped; do not edit its owner metadata or discard its receipts during a rolling upgrade.

The server refuses to hide an existing journal at the export root when external state is requested. Automatic migration of existing receipts is not provided; retain their current location until an offline migration has been designed for that deployment. Relocating or replacing an export root, changing its mount identity, or copying state between hosts requires reviewing its ownership binding rather than silently creating a fresh history.

Files cannot list, read, overwrite, remove or move the reserved state subtree, including through an enclosing export, a symlink alias, ZIP, or an ancestor directory mutation. Selecting the state subtree itself as an export is also rejected. The enclosing user-selected directory may contain other ordinary files; only the reserved child contains private state.

Only journal metadata moves. Upload content is still staged beside its destination and atomically renamed on the destination filesystem. Journal and destination may reside on different filesystems; each is synchronized independently, and interrupted commits are reconciled through the same durable receipts. Protocol capabilities describe supported operations rather than a live disk-health guarantee: each durable upload still checks journal admission before modifying content, including receipt-directory write access. A later storage failure can still require receipt lookup and produce an explicitly uncertain outcome.

The process needs OS permissions for the requested operation. Hidden paths and exclusions apply to writes as well as reads, including symlink targets. Removing, moving or replacing a directory that contains filtered children is rejected. Filtered subtree checks examine at most 10,000 entries and 64 levels; larger mutations must be split. A selected single-file share permits changes only to that file, never its siblings. Capabilities advertise enabled protocol operations; they do not override OS permissions or filters.

## Recoverable uploads

The info response exposes `content_revision`, `conditional_write` and `upload_receipts` separately from write permission. WebDAV also returns `Upload-Protocol: durable-v1` when durable uploads are supported. Windows currently advertises `atomic-sha256-v1` and refuses a durable upload with 501: directory-journal synchronization is not implemented there. Never infer support from an older server's generic PUT success.

Clients requiring these guarantees use `PATCH /fs/path` with `File-System-Action: put` or `delete` and an explicit revision precondition. The handler admits the existing PUT/DELETE implementation under the namespace lock. Older WebDAV owners reject PATCH rather than ignoring unfamiliar headers and performing an unconditional mutation. There is no fallback to ordinary PUT/DELETE after an unsupported response. Ordinary WebDAV methods remain available for existing callers.

For a durable PUT, send `Upload-Id` (a new 16–128 character identifier containing ASCII letters, digits, `_` or `-`), `Upload-Length` (decimal complete byte count), `Content-Digest` (SHA-256), and the intended `If-Match` or `If-None-Match` precondition. `Upload-Length` supports streaming browser requests where the application cannot set Content-Length. The streamed body is checked before publication. The JSON response has the same shape as `GET /fs/path?upload-receipt=ID`:

```json
{
  "id": "upload-example-0001",
  "path": "path",
  "state": "committed",
  "status": 201,
  "size_bytes": "3",
  "sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  "revision": "\"sha256-ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\"",
  "started_at": "2026-01-01T00:00:00Z",
  "completed_at": "2026-01-01T00:00:01Z"
}
```

A 200 receipt response means the attempt was found; inspect `state` and its recorded `status` to determine the outcome. `receiving` and `prepared` are in progress; `committed`, `rejected`, `interrupted` and `uncertain` are terminal. The UTC timestamps are the owner's wall clock, not synchronized acquisition timestamps. The receipt retains its original revision after later replacement or removal of the file. An identical PUT with the same ID returns that receipt without writing again; changed parameters return 409. An interrupted attempt requires a new explicit ID to start another transfer. Looking up an ID never replays its request.

The owner synchronizes staging data, a private preparation journal, the renamed directory and the final receipt before acknowledging success. On restart, the next lookup or mutation reconciles unfinished preparation before any later write can replace its evidence. Staging inode identity, size and digest establish a committed rename; missing or contradictory evidence becomes `uncertain`. Uploads interrupted before publication leave the original intact. This is process-restart recovery under the same filesystem ownership discipline, not coordination with arbitrary external writers.

Receipts use the same authenticated export and current path policy as file reads; the ID grants no access. The metadata is hidden and cannot be downloaded through WebDAV or ZIP. It is retained without automatic expiry, bounded to 4,096 receipts per export; a full journal (receipt limit, disk space or quota) refuses new durable uploads with 507. An inaccessible or otherwise unavailable journal returns 503, with the underlying admission error in the server log. Existing receipt reads and ordinary file operations remain available. There is no expiry or purge API in this protocol. Removing an old receipt would forget its identity and could let the same request write again. Do not remove this journal while its identities must remain recoverable. Journal retention and any higher-level operation retention must be coordinated by the owning application. The receipt does not by itself supply a cross-service operations catalogue or an audit identity.

HTTP `Range`/`If-Range` and the `resume` capability describe downloads only. ZIP downloads must still restart after interruption.

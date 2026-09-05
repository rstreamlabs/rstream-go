# Filesystem WebRTC transport

The shared `filesystem` package composes the same rooted filesystem policy with either WebDAV or WebRTC. `rstream files --backend webrtc` and `webtty server --fs-root ./exports --fs-backend webrtc` select Pion WebRTC v4. Both are opt-in; WebDAV remains the default. Engine and Operator are unchanged. WebTTY terminal execution and terminal E2E are separate.

## Operations and clients

WebRTC supports listing, stat, streamed file reads, ranges and the standalone server’s ZIP handler. Existing DAV request/response semantics are carried across the data channel, so paths, status errors, XML parsing and disk policy have one implementation. HTTP GET/HEAD/PROPFIND remain available for native browser downloads and existing read clients. Writes are rejected both over HTTP and signaling, including PUT, MKCOL, DELETE, COPY and MOVE; neither clients nor servers fall back to WebDAV writes.

Go `filesystem.NewHTTPClient` wraps the existing authenticated HTTP client and tunnel dialer. CLI and MCP share that client. JS `RemoteFileSystem` discovers the backend; `WebRTCFileSystem` explicitly requires it; `WebTTYFileSystem` uses the same adapter and preserves its error type. Old servers with a missing discovery endpoint continue to use WebDAV. In Node, the optional `@roamhq/wrtc` provider must be available, or callers supply `createPeerConnection`; a missing provider raises a clear error. Browsers use their native RTCPeerConnection.

## Version 1 wire contract

The filesystem prefix (normally `/fs`) exposes `/.rstream/files/v1`. GET returns `version: 1`, `backend`, `ice_servers`, `lease_seconds` and `restart_seconds`. POST accepts `offer`, `renew`, `restart` or `close`. An offer includes SDP and a read request (`method`, origin-relative `uri`, header arrays, optional XML body). The answer contains a random session identifier and SDP. Signaling is bounded to 128 KiB, request XML to 16 KiB and SDP to 64 KiB. Redirects and cross-origin browser signaling are rejected.

Each response owns one peer and one reliable ordered DataChannel named `rstream.files.v1`. A JSON status/header frame precedes binary chunks of at most 32 KiB. A JSON `done` frame terminates success; an `error` frame interrupts the stream. Clients verify declared Content-Length before reporting completion. HEAD has no body. Errors or cancellation close the peer; Go callers must close response bodies and SDK callers must consume or cancel streams.

The receiver initially grants 32 chunk credits (1 MiB), then sends `credit` as chunks are consumed and `done` when fully drained. An additional sender buffered-amount limit bounds queued data even for an invalid credit sender. Go bounds SCTP receive buffering to 2 MiB. There are at most 16 active or pending peer setups per service. The server owns peer workers, authorization timers and cancellation through `Close`; close waits for shutdown. These limits are independent of directory and archive limits.

Signaling has the same local password/edge access policy as file reads. A session lease defaults to 90 seconds and is renewed through authenticated HTTP every 30 seconds. Losing authorization, the tunnel, or renewal stops the transfer. ICE credentials are refreshed with an ICE restart every five minutes, after the initial channel is established. The CLI obtains one-hour credentials from the selected rstream project, advertises its STUN server and supported TURN UDP/TCP/TLS URLs, and excludes unsupported TURN-over-DTLS URLs. No public third-party STUN service is used.

## Browser downloads and security

The shared `@rstreamlabs/utils/download` helper opens the browser’s file picker synchronously from the click, then streams to its WritableStream with backpressure, progress and cancellation. Files and the existing encrypted-transfer demo reuse this destination/progress code; the demo keeps its own decryption pipeline. Supported browsers use the same disk flow for WebDAV and WebRTC. Other browsers and native link actions use streaming HTTP attachment URLs, without whole-file Blob buffering. This compatibility path uses normal Engine traffic. A Service Worker bridge for native WebRTC downloads is outside this version.

Direct WebRTC file data bypasses Engine; rstream TURN relays when a direct route is unavailable. This is not a promise of quota-free relay traffic. WebRTC encrypts the peer transport with DTLS but does not implement the planned application-level E2E key distribution or independently authenticated file encryption. Filesystem access is independent of WebTTY terminal encryption; the existing restriction on exposing a filesystem with terminal E2E remains.

## Qualification

Run the Go suite and race detector, build `filesystem/rtc/testdata/server`, then run the JS filesystem `test:e2e` script with `RSTREAM_FILES_E2E_SERVER` pointing to that binary. The fixture provides real Pion peers and optional local TURN and rejects direct HTTP data in RTC-only mode. Tests cover Go/Node interop, files/WebTTY, CLI/MCP reads and write errors, range hashes, malformed lengths, bounded slow consumers, cancellation, session limits, authorization revocation and ICE restart during transfer. The browser destination test streams more than 4 GiB with a bounded queue; it does not claim to write 4 GiB to disk in that unit test.

Release requires native qualification for supported Go targets and Node native-provider platforms, plus browser review. Cross-compilation only establishes build compatibility. Publish the JS changesets, replace Next’s temporary vendored packages with actual published versions, regenerate the embedded artifact, and release Go/product documentation together. No push or deployment is part of local implementation.

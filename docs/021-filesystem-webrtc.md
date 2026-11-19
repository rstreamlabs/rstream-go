# Read files over WebRTC

Choose WebRTC to transfer files over a peer connection. The default WebDAV backend also supports writes when enabled. Both backends enforce the export directory and the configured access policy.

| Use | Configuration |
| --- | --- |
| Standalone file share | Add `--backend webrtc` to `rstream files`. |
| Files alongside a WebTTY server | Use WebSocket transport and add `--fs-root ./exports --fs-backend webrtc`. |
| Go application | Use `filesystem.NewHTTPClient` with the authenticated HTTP client and tunnel dialer. Close response bodies after reading. |

## Access and traffic

WebRTC supports listing, file reads and byte ranges. Upload, write, rename and delete require a writable WebDAV backend. CLI and MCP clients detect the configured backend. File access is unavailable for a WebTTY server with terminal end-to-end encryption or in the [FIPS CLI](017-fips-140-3-profile.md#file-sharing-compatibility).

Signaling and lease renewal use authenticated HTTP. File data moves directly between peers when the network permits it. rstream TURN relays data when a direct path is unavailable, using the project's TURN quota. Browser downloads that use an HTTPS attachment link travel through the Engine. The WebRTC connection encrypts data in transit.

A device that cannot reach the Control plane needs an explicit Engine address and complete relay settings to derive TURN credentials locally. See [TURN credential selection](015-engine-mtls-turn.md#turn-selection-and-authorization) for permissions and configuration.

## Build an application using this backend

Published CLI builds already include the build preparation. If you build a separate Go application that uses this backend, follow the [consuming module instructions](../tools/icepatch/README.md#build-a-consuming-module) for the reviewed ICE dependency.

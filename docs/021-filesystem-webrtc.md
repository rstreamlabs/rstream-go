# Read files over WebRTC

Choose WebRTC to transfer files over a peer connection. The default WebDAV backend also supports writes when enabled. Both backends enforce the export directory and the configured access policy.

| Use | Configuration |
| --- | --- |
| Standalone file share | Add `--backend webrtc` to `rstream files`. |
| Files alongside a WebTTY server | Use WebSocket transport and add `--fs-root ./exports --fs-backend webrtc`. |
| Go application | Use `filesystem.NewHTTPClient` with the authenticated HTTP client and tunnel dialer. Close response bodies after reading. |

## Try a protected download

Choose a managed project with WebRTC file support and rstream STUN/TURN
credentials. Use the same project on both devices and give the receiving
client permission to discover and dial the share. On the server, put a
sample `report.csv` in `./exports` and start a protected WebRTC share.

```sh
rstream files ./exports --name exports --backend webrtc --token-auth
```

From the receiving device, download the file and compare it with the original.

```sh
rstream webtty fs --url rstrm://exports download /report.csv ./report.csv
```

Stop the share with Ctrl+C. For a browser recipient, the
[product backend guide](https://rstream.io/docs/tunnels/file-sharing#choose-the-transfer-backend)
describes the peer path and HTTPS attachment fallback.

## Access and traffic

WebRTC supports listing, file reads and byte ranges. Upload, write, rename and delete require a writable WebDAV backend. CLI and MCP clients detect the configured backend. File access is unavailable for a WebTTY server with terminal end-to-end encryption or in the [FIPS CLI](017-fips-140-3-profile.md#file-sharing-compatibility).

Signaling and lease renewal use authenticated HTTP. File data moves directly between peers when the network permits it. rstream TURN relays data when a direct path is unavailable, using the project's TURN quota. Browser downloads that use an HTTPS attachment link travel through the Engine. The WebRTC connection encrypts data in transit.

Without a reachable Control plane, configure the device's Engine address.
An Engine with TURN enabled can issue credentials directly. Local derivation
is available to eligible credentials when relay settings are supplied. See [TURN credential selection](015-engine-mtls-turn.md#turn-selection-and-authorization) for permissions and configuration.

## Build an application using this backend

Published CLI builds already include the build preparation. If you build a separate Go application that uses this backend, follow the [consuming module instructions](../tools/icepatch/README.md#build-a-consuming-module) for the reviewed ICE dependency.

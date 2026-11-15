# Embed the Go SDK

Use Go when your application should own the Engine connection, accept or dial streams, or compose datagram protocols. This is rstream's reference SDK: its breadth and standard Go network interfaces are the reason to choose it, not a measured speed advantage.

## Choose released code or this checkout

| Path | Preparation | First result |
| --- | --- | --- |
| Consume the published module | Create a Go module, add `github.com/rstreamlabs/rstream-go`, select an authorized CLI context. | Follow the complete [HTTP program in the README](../README.md#use-the-go-library): save main.go, tidy, run and curl its printed URL. |
| Run or modify these examples | [Build this checkout](001-building-from-source.md); Make prepares the verified dependency graph. | [HTTP server/client pair](../examples/README.md#serve-http) from the repository root. |
| Forward an existing process | No application code change. | [CLI workflow](002-cli-workflow.md). |

A source checkout and a published SDK are different inputs. Go modules do not inherit dependency replacements. Applications using the filesystem WebRTC path must follow the [verified ICE preparation](../tools/icepatch/README.md#build-a-consuming-module) with a reviewed source/helper; do not edit the module cache or assume `go get` installs that preparation. The guide states which helper is available from source versus a published release.

## Network interfaces and ownership

| Object/API | Go integration | Owner closes |
| --- | --- | --- |
| `Client` | Shared authenticated runtime and Engine HTTP APIs. | Close after its controls and application operations stop; this releases owned transports and HTTP pools. |
| `Client.Connect` → `ControlChannel` | Create tunnels and receive their connections. | Close the channel after tunnels/accept loops stop. |
| `BytestreamTunnel` | `net.Listener`; accepted streams implement `net.Conn`. | Close accepted connections, tunnel and channel. `http.Server.Serve` accepts the tunnel directly. |
| `Client.Dial` | `net.Conn` for a private tunnel selected by name/ID. | Close each connection, including on request cancellation. |
| `DatagramTunnel`, `PacketListener`, `PacketDial` | Packet sessions and `net.PacketConn` adapters for protocol libraries. | Close sessions/listeners and stop owned workers before their client. |

Pass contexts/deadlines for bounded setup and requests. A context used to establish a tunnel is not a complete application shutdown policy. Stop the HTTP server or connection workers before releasing the tunnel. SDK objects do not supervise a service's reconnection loop for it; recreate failed control/tunnel generations under your own lifecycle. CLI `forward`, `run` and WebTTY provide their documented supervision when you choose those commands.

## Choose the next capability

| Requirement | Reference or sample |
| --- | --- |
| TLS/QUIC client transport, proxy, private CA, DNS and interface binding | [Transport configuration](004-transport.md). |
| HTTP/1.1, h2c, HTTP/3, published TCP/TLS/DTLS/QUIC or private access | [Tunnel properties](005-tunnel-properties.md), [example catalog](../examples/README.md). |
| WebSocket or WebTransport, CONNECT-UDP/IP | [Extended CONNECT](014-extended-connect.md); application protocol code stays in its library. |
| Inventory/watch/session APIs with token or certificate | [Engine API mTLS/TURN](015-engine-mtls-turn.md). Control plane administration still needs its own authorized token. |
| Restricted cryptographic distribution | [FIPS profile](017-fips-140-3-profile.md); its exclusions are intentional. |

Coverage still depends on Engine version, edition, listeners, project permissions and plan. Native unreliable datagrams preserve packet boundaries but do not promise every packet; [qualification notes](../test/performance/000-datagram-qualification.md) explain loss attribution and limits. Embedding a protocol does not make its application policy, target authorization or operational recovery rstream's responsibility.

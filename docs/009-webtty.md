# WebTTY

WebTTY runs a terminal or a command on a remote machine through rstream. Use
a tunnel server for a direct connection. Register the server when you need
workspace-managed access or session operations supported by your deployment.

## Open a terminal

### Start a tunnel server

Select an authorized project on both machines. On the remote machine, start a WebSocket server with a private tunnel. The project must allow private tunnels.

```sh
rstream webtty server --rstream --name shell --no-publish
```

On the operator's machine, using credentials for that project:

```sh
rstream webtty exec --url rstrm://shell -o text -- whoami
```

Expect the username of the operating system account running the server.
Stop the server with Ctrl+C. On a project that allows publication, omit
`--no-publish` to enable a browser endpoint as well. Use a dedicated service
account and configure access for any server kept running beyond this example.
The Go server on Windows supports command execution through pipes.

## Configure and operate a server

| Task | Reference |
| --- | --- |
| Set up access, an operating system user, end-to-end encryption or a registered server | [WebTTY configuration](webtty/000-configuration.md) |
| Inspect, join or stop managed sessions, connect without discovery or transfer files | [WebTTY session operations](webtty/001-session-operations.md) |
| Embed a server or client and choose transports or manage session lifetime | [Go runtime reference](webtty/002-go-runtime.md) |

For browser and dashboard workflows, see the [product WebTTY guide](https://rstream.io/docs/webtty).

## End-to-end encryption

End-to-end encryption protects terminal input and output between the WebTTY
client and server. Session routing and lifecycle metadata remain available
to the Engine. See [endpoint trust setup](webtty/000-configuration.md#require-end-to-end-terminal-encryption)
and the [Go encryption reference](webtty/002-go-runtime.md#e2e-crypto).

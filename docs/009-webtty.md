# WebTTY

WebTTY runs a terminal or a command on a remote machine through rstream. Use
a tunnel server for a direct connection. Register the server when you need
workspace-managed access or session operations supported by your deployment.

## Open a terminal

[Configure the CLI](002-cli-workflow.md#connect-the-cli) on the server and
operator machines. Use the same project or direct Engine on both. On Community Edition, start
with the private command below. The server
in this example runs commands as the operating system user that starts it.
Use a dedicated account for a server that will stay running.

| Mode | When to use it |
| --- | --- |
| Published WebSocket | Use a managed project that permits HTTP token authentication, including hosted Basic. The server requests token protection for the public endpoint. A browser opening the endpoint needs an authorized session. |
| Private WebSocket | Use a project that permits private tunnels, or a direct Community Edition Engine. Only rstream clients can dial the endpoint. |

On a managed project, start published WebTTY with:

```sh
rstream webtty server --rstream --name shell
```

On Community Edition or a managed project that permits private tunnels, use:

```sh
rstream webtty server --rstream --name shell --no-publish
```

In a second terminal with the operator context, run:

```sh
rstream webtty exec --url rstrm://shell -o text -- whoami
```

Expect the username of the account running the server. Stop the server with
Ctrl+C. If your token permits inventory, run `rstream webtty list` to verify
that the lightweight server is gone. The Go server on Windows supports command
execution through pipes. See [browser access](https://rstream.io/docs/webtty/quickstart#publish-through-rstream)
for the published endpoint.

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

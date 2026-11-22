# Forward a service from YAML

Use this example to define a tunnel in a file and apply it with the CLI.

| Prerequisite | Purpose |
| --- | --- |
| Go and the rstream CLI | Go runs the small `whoami` service. Install the CLI or [build it from this checkout](../../docs/001-building-from-source.md). |
| An authorized rstream context | Select a project that permits a published HTTP tunnel. See the [CLI workflow](../../docs/002-cli-workflow.md). |

From the repository root, start the upstream service:

```sh
go run ./cmd/whoami --listen 127.0.0.1:8080
```

In a second terminal, apply the bundled [tunnels.yaml](tunnels.yaml) and watch for updates.

```sh
rstream -v run --apply examples/run-yaml/tunnels.yaml --watch
```

Request the printed forwarding URL to see the JSON description of your
request. When your project requires HTTP token authentication, set
`tunnel.http.auth.token: true` before applying the file and
[verify it with a scoped caller token](https://rstream.io/docs/tunnels/http-tunnels/authentication#token-authentication).
Stop both processes with Ctrl+C. The
[configuration guide](../../docs/007-declarative-tunnels.md#yaml-schema)
covers the remaining YAML fields.

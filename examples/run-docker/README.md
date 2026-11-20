# Forward a Docker service from labels

This example runs a `whoami` container and uses the rstream CLI to publish it. The CLI discovers labeled containers and updates their tunnels as Docker reports changes.

| Prerequisite | Purpose |
| --- | --- |
| Docker with Compose | Runs both services and provides the Docker socket used for discovery. |
| Engine endpoint and project token | Authorize the agent to publish HTTP tunnels. Use a token scoped to this example's project. |
| Container images | The [Compose file](docker-compose.yml) uses `rstream/rstream:latest` and `rstream/whoami:latest`. |

From this directory, copy [`.env.example`](.env.example) to `.env` and set these values. Keep the file private.

| Variable | Value |
| --- | --- |
| `RSTREAM_ENGINE` | Engine hostname and port for the project, such as `engine.example.com:443`. |
| `RSTREAM_AUTHENTICATION_TOKEN` | Token with the required permissions on that project. |

The discovery container can access the Docker socket. Run the example on a
host where you trust the container and its service labels. The sample publishes
a disposable `whoami` service. To require HTTP access tokens, add the label
`rstream.tunnel.web.http.auth.token: "true"` before starting it.

```sh
docker compose up
```

Open the forwarding URL from the rstream service logs and check that `whoami`
returns JSON. If you configured token authentication, use an authorized
request and confirm an anonymous request is denied. Stop Compose with Ctrl+C.
See [declarative tunnels](../../docs/007-declarative-tunnels.md) for label options.

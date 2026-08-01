# Webhooks

The Go SDK and CLI expose two related event surfaces:

- `rstream events` watches live engine events over SSE or WebSocket.
- Control plane webhook methods manage durable webhook destinations and delivery history.

`rstream.Event` preserves canonical live-event metadata (`id`, `created_at`, `workspace_id`, `project_id`, `cluster_id`, and `user_id`) in addition to the event `type` and raw `object`. Control plane `ProjectEvent` responses use the durable API shape: `eventId`, `eventType`, `eventCategory`, `payload`, `createdAt`, and retention metadata.

Control plane methods include:

- `ListProjectEvents`
- `ListProjectWebhooks`
- `CreateProjectWebhook`
- `UpdateProjectWebhook`
- `DeleteProjectWebhook`
- `RotateProjectWebhookSecret`
- `ListProjectWebhookDeliveries`
- `GetProjectWebhookDelivery`

`CreateProjectWebhook` and `UpdateProjectWebhook` accept
`ProjectWebhookEndpointConfig` for HTTPS endpoint destinations. The SDK rejects
unsupported destination types, non-HTTPS URLs, and URLs that embed credentials
before opening a network request. `ProjectWebhook.Config` is kept as raw JSON so
future destination-specific configs can be represented without changing the
top-level response shape; use `DecodeEndpointConfig()` for current HTTP endpoint
destinations.

Webhook deliveries are distinct from lifecycle events. Delivery records include attempts, HTTP status, response time, response body, and retry state for one webhook destination.

## Webhook-compatible local forwarding

By default, `rstream events` emits the live watch event shape. Use `--webhook`
when a local receiver should see the same request body and signed headers as a
durable webhook delivery:

```bash
rstream events \
  --webhook \
  --events tunnel.created,tunnel.deleted \
  --forward-to http://localhost:3000/api/rstream/webhook
```

When `--webhook` is enabled, the CLI filters out non-deliverable watch events,
builds the canonical webhook JSON body, signs the raw body, and forwards the
request with `rstream-signature`, `rstream-event-id`, `rstream-event-type`,
`rstream-webhook-id`, and `rstream-delivery-id`. If `--webhook-secret` is not
provided, the CLI generates a `whsec_...` secret and prints it to stderr. Use the
same value in the receiving backend for local verification. Local forwarding can
target `http://` receivers such as a Next.js development server, or `https://`
receivers when testing a deployed backend.

For stdout inspection, add `--include-webhook-headers`. The output becomes an
envelope containing the generated headers and body. The envelope is for local
debugging only; forwarded HTTP requests still use the real webhook request body.

CLI forwarding is not a delivery backend. It does not create Control plane
webhook destinations, persist delivery attempts, or retry after process exit.
Use configured project webhooks for durable delivery history and retry behavior.

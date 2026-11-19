# Receive live events and webhooks

Use the event stream to observe changes while your client is connected. Configure a project webhook when a receiver needs durable delivery and retry history.

| Surface | Purpose |
| --- | --- |
| `rstream events` | Watch live engine events over SSE or WebSocket. |
| Control plane webhook methods | Manage durable destinations, retries and delivery history. |
| `rstream events --webhook` | Forward signed client/tunnel events to a local receiver during development. |

`rstream.Event` preserves canonical live-event metadata (`id`, `created_at`, `workspace_id`, `project_id`, `cluster_id`, and `user_id`) in addition to the event `type` and raw `object`. Control plane `ProjectEvent` responses use the durable API shape: `eventId`, `eventType`, `eventCategory`, `payload`, `createdAt`, and retention metadata.

| Task | Control plane methods |
| --- | --- |
| Inspect project lifecycle events | `ListProjectEvents` |
| Manage destinations | `ListProjectWebhooks`, `CreateProjectWebhook`, `UpdateProjectWebhook`, `DeleteProjectWebhook` |
| Rotate receiver credentials | `RotateProjectWebhookSecret` |
| Inspect delivery attempts | `ListProjectWebhookDeliveries`, `GetProjectWebhookDelivery` |

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

The local webhook catalog contains `client.created`, `client.deleted`,
`tunnel.created` and `tunnel.deleted`. WebTTY events are filtered out in this
mode. Use configured project destinations with a compatible receiver for those
events; the [product guide](https://rstream.io/docs/integrations/webhooks#receiver-compatibility)
describes parser support across SDKs. This restriction applies to local webhook
conversion, not the ordinary live watch stream.

The Go SDK exposes signing and event-conversion helpers; it does not provide a
combined receiver signature verifier/parser. Verify signatures before acting on
the body in application receivers.

For stdout inspection, add `--include-webhook-headers`. The output becomes an
envelope containing the generated headers and body. The envelope is for local
debugging only; forwarded HTTP requests still use the real webhook request body.

CLI forwarding is not a delivery backend. It does not create Control plane
webhook destinations, persist delivery attempts, or retry after process exit.
Use configured project webhooks for durable delivery history and retry behavior.

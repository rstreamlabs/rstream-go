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

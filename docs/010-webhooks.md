# Receive live events and webhooks

Choose how the receiver needs to consume events. A live stream is useful
while an operator, application or local test is connected. A project webhook
delivers events to an HTTPS receiver and records attempts and retries.

| Use | Interface |
| --- | --- |
| Inspect the current event stream | `rstream events --transport sse` or the SDK's `Client.Watch`. |
| Test a local receiver with signed requests | `rstream events --webhook --forward-to`. |
| Manage durable delivery | [Project webhooks](https://rstream.io/docs/integrations/webhooks#destinations) or the Control plane SDK methods below. |

## Webhook-compatible local forwarding

Start your receiver at `http://localhost:3000/api/rstream/webhook` and configure
it to verify the [signature on the raw request body](https://rstream.io/docs/integrations/webhooks#signatures).
Select a [CLI context](002-cli-workflow.md#connect-the-cli) that can watch project
events. Give the CLI and receiver the same dedicated test secret.

```sh
rstream events --transport sse \
  --webhook --webhook-secret "$RSTREAM_WEBHOOK_SECRET" \
  --events tunnel.created,tunnel.deleted \
  --forward-to http://localhost:3000/api/rstream/webhook
```

Set `RSTREAM_WEBHOOK_SECRET` to your test receiver's secret before running the
command. If you omit `--webhook-secret`, configure the receiver with the
temporary secret the CLI prints to stderr. In another terminal, create and
stop a tunnel allowed by that project. The receiver should verify one request
for each selected event. Stop the watcher with Ctrl+C.

To inspect the signed body and headers without sending HTTP requests, replace
`--forward-to` with `--include-webhook-headers`. Local signing supports
`client.created`, `client.deleted`, `tunnel.created` and `tunnel.deleted`.
Use a durable destination and a [compatible receiver](https://rstream.io/docs/integrations/webhooks)
for WebTTY events.

## Control plane methods in Go

Use the authenticated Control plane client to manage a destination or inspect
the lifecycle of a project event. `ListProjectEvents` returns retained
`ProjectEvent` records. Delivery records track a destination's attempts,
status and retry state.

| Task | Control plane methods |
| --- | --- |
| Inspect project lifecycle events | `ListProjectEvents` |
| Manage destinations | `ListProjectWebhooks`, `CreateProjectWebhook`, `UpdateProjectWebhook`, `DeleteProjectWebhook` |
| Rotate a receiver secret | `RotateProjectWebhookSecret` |
| Inspect delivery attempts | `ListProjectWebhookDeliveries`, `GetProjectWebhookDelivery` |

Pass `ProjectWebhookEndpointConfig` to create or update an HTTPS destination
and use `DecodeEndpointConfig()` to read that variant of a response. The Go
SDK exposes signing and live-event conversion helpers. A Go receiver should
verify the signature and parse the resulting payload separately.

# Qualify native datagram delivery

Native QUIC datagrams are best effort. Packet reordering can cause sufficiently old, previously unseen packets to be discarded by QUIC receive history before reaching the SDK. Application receive queues can drop packets independently. Neither successful reliable-stream tests nor larger SDK queues establish datagram delivery under these conditions.

## Repeatable fixture

Use the maintained [transport probe](001-transport-probes.md) with disposable local Engine endpoints, a fixture token and a verified CA. Keep all certificates, traces, raw results and generated binaries outside the source tree. The probe does not provide a deployed environment or change project policy.

| Variable | Record or control |
| --- | --- |
| Source graph | SDK/Engine commits, Go version, dependency versions and binary metadata. Compare a reference and candidate in alternating order. |
| Payload and count | Identical datagram size/count across runs; retain unique sequence numbers. |
| Pacing | `--pacing-ms` 1–1000, default 1 ms. Record actual send duration: congestion backpressure changes offered load. |
| Drain | `--drain-seconds` 1–30, default 3 s. Missing packets are delivery-window misses, not a physical loss measurement. |
| Impairment | Record direction and every delay/jitter/loss/reorder setting. Isolate forward and return legs. |
| Resource bounds | Preserve queue limits, goroutine ownership, overall 90 s deadline and joined cleanup. |

Run clean, constant-delay/loss, jitter-only, reordering-without-injected-loss and reordering-with-loss controls. Compare both matched actual offered load and identical configured pacing; these answer different questions. A netem reorder profile can create near-zero-delay ACK cycles alongside delayed packets. Do not replace an adversarial profile with an easier one and call the original finding fixed.

## Attribute a miss

Record sent, received and echoed sequence numbers, duplicates, reordering, actual send duration and queue snapshots. Separate send failure, receive-history rejection, SDK queue overflow, relay/application loss and drain-window expiry. A `duplicate` trace reason can mean potentially duplicate after a history cutoff; it does not prove earlier delivery. Packet/ACK traces can test that explanation, but keep them metadata-only and avoid payloads or tokens.

A standalone upstream probe can isolate a library mechanism; it is research evidence, not a maintained product test. Keep its source and logs outside this repository. An rstream echo can cross two impaired client-to-Engine legs, so its delivery percentage cannot be compared directly with a one-leg probe.

## Interpret the result

Retain raw ranges, exclusions and incomplete runs. Confirm shutdown with a reliable marker outside the measurement window; an unreliable close notification is not a completion oracle. These measurements establish the selected fixture's behavior, not production capacity or a protocol violation. RFC 9000 permits bounded receive history.

There is no public QUIC receive-history retention knob in the reviewed dependency. Do not patch upstream code, disable duplicate/replay checks, enlarge unrelated queues, throttle all traffic or force reliable delivery to hide this limitation. An application that requires every byte should choose a reliable stream or an explicitly designed delivery protocol. Nested QUIC, WebTransport, HTTP datagrams and MASQUE need their own qualification; this method does not certify them.

See [quic-go datagrams](https://quic-go.net/docs/quic/datagrams/), [RFC 9000 receive history](https://www.rfc-editor.org/rfc/rfc9000.html#section-13.2.3) and [netem reordering](https://man7.org/linux/man-pages/man8/tc-netem.8.html).

# QUIC datagram qualification — 13 November 2026

The QUIC 0.64.1 dependency upgrade has an unresolved native-datagram delivery
gate. Keep the upgrade PR in draft. Passing functional tests and faster reliable
streams do not resolve this finding.

## What the traces establish

The main loss occurs before rstream's receive/relay queues. A QUIC receiver rejects
previously unseen, reordered packets as `duplicate` after advancing its minimum
accepted packet number. The label means *potentially duplicate*; it does not prove
that the packet was delivered earlier.

In a standalone QUIC echo probe, with no rstream SDK, engine or proxy, 150 of
1,000 packets were missing. Of these, 145 reached the server but were rejected
without any prior receipt. Packet-number and ACK-range traces match **all 145**
rejections to an earlier ACK-of-ACK that permits the receiver's history cutoff to
advance past the delayed packet. The remaining five were not observed at the
receiver; they are not attributed to this mechanism.

For example, server packet 12 acknowledges client packet 23. Client packet 25
acknowledges server packet 12 before client packet 14 arrives. The receiver's
cutoff can advance to 24; packet 14 arrives about 24.5 ms after sending and is
discarded despite never having been processed.

The relevant upstream paths are `sentPacketHandler.detectAndRemoveAckedPackets`,
`appDataReceivedPacketTracker.IgnoreBelow` and
`receivedPacketHistory.IsPotentiallyDuplicate`. The receive-history implementation
is identical in 0.60.0 and 0.64.1. The old version already exhibits this loss; the
new sender produces more of it in this stress profile. The release boundary is
between the tested 0.63.0 and 0.64.1; the individual change responsible for the
higher offered rate has not been isolated.

## Controlled results

Each standalone sample sends 1,000 1,000-byte datagrams with nominal 1 ms pacing,
uses verified TLS 1.3, and drains for three seconds. Only client-to-server traffic
is impaired. The echo return path is unchanged. Each table entry contains two
samples; percentages are measured echo delivery, not physical network loss.

| Sender quic-go | Receiver quic-go | Original stress delivery | Actual send duration |
| --- | --- | --- | --- |
| 0.60.0 | 0.60.0 | 93.1–93.7% | 4.69–4.82 s |
| 0.60.0 | 0.64.1 | 91.6–92.8% | 4.61–4.67 s |
| 0.64.1 | 0.60.0 | 81.2–83.0% | 1.86–2.03 s |
| 0.64.1 | 0.64.1 | 85.2–86.1% | 1.78–2.14 s |

Changing the receiver alone does not reproduce the large difference. Sender
versions 0.61.0, 0.62.0 and 0.63.0 remain near the old reference. A 0.65.0 sender
with the 0.60.0 receiver still delivers only 82.8–84.6%; testing that version is
diagnostic evidence, not a proposed companion-library upgrade.

| Control, same-version endpoints | 0.60.0 delivery | 0.64.1 delivery | Observation |
| --- | --- | --- | --- |
| Clean | 100% | 100% | All eight clean/mixed-version samples deliver every packet |
| Constant 20 ms delay, 1% loss | 99.0–99.1% | 98.5–99.1% | No late packet rejection |
| 20 ms delay / 5 ms jitter, 1% loss, no explicit `reorder` | 98.1–99.4% | 96.9–97.7% | Jitter itself can reorder packets |
| Original reordering, **zero injected loss** | 91.7–93.9% | 78.1–82.9% | Every missing packet is accounted for by a late rejection |
| Original profile plus a separate fixed 20 ms forward delay | 98.9–99.0% | 97.8–98.7% | Smaller difference; no equivalence claim |
| Original profile, **5 ms pacing** | 94.4–94.9% | 93.6–94.6% | Actual send durations overlap, around 5.7 s |

The original profile is `netem delay 20ms 5ms distribution normal loss 1%
reorder 25% 50%`. Netem sends the selected reordered packets immediately; it does
not preserve a 20 ms minimum propagation delay. This creates very short ACK
cycles alongside delayed packets and accentuates the history-cutoff behavior.
It remains a useful adversarial test. The controls must accompany it; changing
the profile alone would not fix the demonstrated loss.

The new sender encounters less congestion backpressure, so nominally identical
1 ms pacing does **not** mean identical actual offered load. At 5 ms pacing the
large gap largely disappears, while history-related losses remain. These small,
shared-host samples do not establish statistical equivalence or production
capacity.

## rstream impact and correction boundary

The full rstream echo crosses two SDK-to-engine QUIC legs, both subject to the
fixture's impairment. Its delivery ratio is not directly interchangeable with
the single impaired leg in the standalone probe. Version-crossed rstream probes
also point to the SDK-side change: old SDK/new engine delivers 92.3%, whereas new
SDK/old engine delivers 76.1% in the diagnostic samples.

Some SDK echo queues also overflow in individual high-rate probes. They are a
separate, measured contribution: one timed rstream sample has 14 echo-queue drops;
another has zero queue drops and still returns only 785/1,000 packets. Enlarging
queues cannot recover packets rejected earlier by QUIC. In the standalone probe,
there are no rstream queues, and no return-path packet loss was observed.

The demonstrated risk concerns native unreliable QUIC datagrams under packet
reordering, including applications transporting UDP or another QUIC connection
over that path. Reliable QUIC streams retransmit their data; their passing tests
do not certify nested QUIC over datagrams. WebTransport, HTTP datagrams and MASQUE
can share the underlying mechanism, but this probe does not certify those paths
or quantify their impact. No production incidence is inferred from the lab.

There is no demonstrated simple rstream queue/configuration fix. The public
`quic.Config` has no receive-history retention setting. A correction belongs in
the upstream ACK/history policy or in an explicitly designed application delivery
policy. It must retain bounded state and duplicate/replay rejection, with tests
for delayed unseen packets, actual duplicates, ACK-range floods, cancellation and
stream/control fairness. RFC 9000 permits bounded receive history and the resulting
discard of sufficiently old packets; these observations do not establish a
protocol violation.

No upstream source, project settings, queue bounds, delivery mode or production
configuration were changed. Do not silently throttle all traffic, disable
duplicate protection, force reliable delivery, or treat 0.65.0 as a fix. Preserve
the original case as a pre-merge qualification gate until a validated correction
or an explicit acceptance decision resolves it.

## Evidence and repeatability

The machine-local research owner is
`DEV_ROOT/local/pocs/quic-go-release-compat-2026-11-12/`; generated sources,
module graphs, binaries and traces are under
`DEV_ROOT/local/artifacts/runtime/rstream-lot2-quic-2026-11-13/datagram-diagnosis/`.
`datagram-standalone.py`, `datagram-standalone-run.py`, `datagram-controls.py` and
`datagram-versions.py` build/run the isolated probes; `datagram-validate.py`
rechecks packet accounting and ACK causality. This research fixture uses its
local Docker lab image and retained verified certificate; it is not a portable
product test or a production command.

`standalone-summary.json` contains 47 complete samples, and `validation.json`
records the checks and one excluded shutdown failure. That pilot lost its
unreliable connection-close notification; subsequent probes use a reliable
completion marker after measurement. Its failed sample is excluded, while earlier
individually completed samples are retained. `ack-purge-proof.json` retains concrete
packet/ACK examples. Engine overlays add bounded metadata-only qlog collectors;
they do not patch quic-go or log application payloads/tokens.

Sources: [quic-go datagrams](https://quic-go.net/docs/quic/datagrams/),
[0.64.0 changes](https://github.com/quic-go/quic-go/releases/tag/v0.64.0),
[0.65.0 changes](https://github.com/quic-go/quic-go/releases/tag/v0.65.0),
[RFC 9000 §12.3](https://www.rfc-editor.org/rfc/rfc9000.html#section-12.3),
[RFC 9000 §13.2.3–4](https://www.rfc-editor.org/rfc/rfc9000.html#section-13.2.3),
[netem reordering semantics](https://man7.org/linux/man-pages/man8/tc-netem.8.html).

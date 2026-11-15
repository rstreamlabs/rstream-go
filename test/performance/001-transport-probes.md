# Local transport probes

The Linux-only `transport` program exercises verified private tunnels through
actual fixture engines. Follow [Build from source](../../docs/001-building-from-source.md) first, then build from the repository root:

```sh
CGO_ENABLED=0 GOOS=linux go build -modfile=out/ice-patch/go.mod -o /absolute/output/transport-benchmark ./test/performance/transport
```

The output directory must already exist. Run the binary on Linux with disposable fixture endpoints and a trusted fixture CA. Certificates and payloads are verified; 0-RTT is disabled.

| Input | Format and purpose | Default or condition |
| --- | --- | --- |
| `--owner`, `--peer` | Engine host:port for publishing and consuming clients. | Both required; can select the same local fixture Engine. |
| `--ca` | Path to PEM fixture trust bundle. | Required; no insecure TLS fallback. |
| `RSTREAM_AUTHENTICATION_TOKEN` | Temporary fixture token shared by the two clients. | Required; never use a personal/deployed credential. |
| `--transport` | tls or quic client-to-Engine transport. | quic; no automatic selection in this probe. |
| `--workload` | stream, mixed, slow, cancel or datagram. | stream; see observations below. |
| `--cross-region` | Boolean permitting cross-region tunnel routing. | false; requires a fixture supporting that route. |
| `--pings` | Number of stream-latency samples. | 100; range 1–10000. |
| `--bytes` | Bulk stream payload size in bytes. | 16777216 (16 MiB); range 1–2147483648. |
| `--packets` | Number of datagrams. | 1000; range 1–100000. |
| `--pacing-ms` | Datagram pacing in milliseconds. | 1; range 1–1000; actual send time is measured separately. |
| `--drain-seconds` | Post-send datagram receive window in seconds. | 3; range 1–30. |
| `--profile-prefix` | Absolute/new output prefix for exclusive CPU/allocation files. | Disabled when omitted; parent directory must exist. |

| Workload | Observation |
| --- | --- |
| `stream` | Ping latency before verified bulk transfer |
| `mixed` | Interactive latency only while bulk traffic is active on another stream; both use the same consumer client/transport |
| `slow` | Verified transfer with a 2 ms pause after each 64 KiB read |
| `cancel` | Close an active stream with unread response, require writer shutdown within two seconds, then verify another stream |
| `datagram` | Delivery, duplicates, reordering, actual send duration and receive-queue snapshots |

The probe has a 90-second deadline. Echo workers, resource sampling and clients
are owned and joined on exit. Transfer sizes, sample counts and datagram timing
are bounded. `--pacing-ms=1..1000` and `--drain-seconds=1..30` control datagram
timing; defaults are 1 ms and three seconds. Missing packets mean delivery-window
misses, not physical link loss. Datagram delivery is best effort; this probe measures the selected transport without changing its delivery guarantees. The reviewed QUIC dependency set has an open delivery limitation under packet reordering: packets can be discarded by QUIC receive history before reaching the SDK. This is separate from application queue overflow; the [datagram method](000-datagram-qualification.md) describes the required controls. A successful stream or WebTransport test does not qualify native datagram behavior, and larger SDK queues cannot recover packets already rejected by QUIC.

JSON contains CPU/allocation deltas for both SDK peers in one process, latency
quantiles, overlapping ping count, sampled heap/goroutine peaks and before/after
goroutine counts around client cleanup. Sampling uses `runtime/metrics`
every 20 ms, contributes measurement overhead and does not capture an exact peak
or RSS. JSON identifies the sampler and interval. Repeated `ReadMemStats`
sampling was removed after profiling showed its stop-the-world overhead. CPU/allocation transfer deltas exclude setup. An optional
`--profile-prefix=/new/output/sample` produces exclusive mode-0600 CPU/allocation
profile files over the probe, including connection setup; the parent directory
must exist. Use a separate profiling pass, then `go tool pprof` on those files.
Never overwrite evidence from earlier runs.

Run Linux helpers under `go test -modfile=out/ice-patch/go.mod -race ./test/performance/transport`. Focused
public transport tests in `quic_stream_close_test.go` additionally prove close
unblocks reads and flow-controlled writes, survives deadline/close races and
concurrent writers, preserves accepted bytes and retains half-close responses.
Those deterministic tests complement the timing-based runtime workload.
The real HTTP/3/WebTransport control in `webtty/transport_close_test.go` also
checks session close with three flow-controlled writers, without changing the
WebTransport implementation or extrapolating the native QUIC finding to it.

Provide two test engine endpoints and a CA bundle for this probe. Use identical helpers, compiler and settings for reference and candidate, retain raw ranges and binary metadata, and alternate order. Local measurements do not establish production
capacity. This utility changes no project policy, dependency version or protocol
feature default.

For an isolated stream-wrapper comparison without the resource sampler, run from the SDK root:

```sh
go test -modfile=out/ice-patch/go.mod -run '^$' -bench '^BenchmarkQUICTransportStreamRoundTrip$' -benchtime=2s -count=3 -benchmem .
```

It measures a verified 32 KiB echo over real local QUIC, includes both peers in allocation counts, and excludes certificate/connection setup. Compare identical helpers and compiler; this is not a production-capacity benchmark.

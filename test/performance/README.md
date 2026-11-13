# Local transport probes

The Linux-only `transport` program exercises verified private tunnels through
actual engines. Build with `CGO_ENABLED=0 GOOS=linux go build -o
/absolute/output/transport-benchmark ./test/performance/transport` on one line.
Supply `--owner`, `--peer`, `--ca`, `--transport=tls|quic`,
`--workload=stream|mixed|slow|cancel|datagram` and optionally `--cross-region=true`.
`RSTREAM_AUTHENTICATION_TOKEN` must hold the temporary fixture token.
Certificates and payloads are checked; 0-RTT is disabled.

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
misses, not physical link loss. Keep the
[native-datagram qualification issue](https://github.com/rstreamlabs/rstream-engine/pull/217)
separate; this probe neither fixes it nor switches delivery guarantees.

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

Run Linux helpers under `go test -race ./test/performance/transport`. Focused
public transport tests in `quic_stream_close_test.go` additionally prove close
unblocks reads and flow-controlled writes, survives deadline/close races and
concurrent writers, preserves accepted bytes and retains half-close responses.
Those deterministic tests complement the timing-based runtime workload.
The real HTTP/3/WebTransport control in `webtty/transport_close_test.go` also
checks session close with three flow-controlled writers, without changing the
WebTransport implementation or extrapolating the native QUIC finding to it.

The engine's `test/performance/README.md` specifies its disposable two-engine
fixture, impairment, quotas and comparison procedure. Use identical helpers,
compiler and settings for reference and candidate, retain raw ranges and binary
metadata, and alternate order. Local measurements do not establish production
capacity. This utility changes no project policy, dependency version or protocol
feature default.

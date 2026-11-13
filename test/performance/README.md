# Transport benchmark

The Linux-only `transport` program measures verified private stream/datagram
tunnels through actual engines, using explicit TLS or QUIC and direct/cross-region
routing. It is a test utility, not a load generator or production capacity test.

Build with `CGO_ENABLED=0 GOOS=linux go build -o /absolute/output/transport-benchmark
./test/performance/transport` in one shell line. Run with `--owner`, `--peer`,
`--ca`, `--transport=tls|quic`, `--workload=stream|datagram` and optionally
`--cross-region=true`. `RSTREAM_AUTHENTICATION_TOKEN` must contain a fixture token.
Certificates and payloads are verified; 0-RTT is disabled. The invocation has a
90-second deadline and closes its owned tunnel, clients and echo worker.

JSON reports process CPU, allocation deltas, transferred bytes, latency quantiles,
and datagram delivery/duplicate/reordering counts. CPU includes both SDK peers in
one process; setup is excluded. Stream measurements cover bulk transfer after
latency pings. Datagrams use 1 ms pacing and a three-second drain, so missing
packets are delivery-deadline misses rather than a physical loss measurement.

`--pacing-ms` (default `1`, range `1..1000`) and `--drain-seconds` (default `3`,
range `1..30`) permit paced-load and late-delivery diagnostics. JSON includes the
actual send duration and supported receive-queue snapshots for both SDK peers.
Compare actual offered rates as well as requested pacing: blocking writes can
throttle an older transport, while a faster sender can saturate existing bounded
queues. Also attribute loss before application queues: the November 2026
[QUIC datagram diagnosis](quic-datagram-diagnosis.md) reproduces previously unseen
reordered packets being rejected after QUIC receive-history purging. Queue growth
cannot fix those losses. The QUIC upgrade retains a pre-merge qualification gate.
Keep stress results separate from equal-rate comparisons and select the existing
guaranteed-delivery mode explicitly when the application requires reliable packets.

The engine repository's `test/performance/README.md` describes the isolated Docker
fixture, forwarding-router impairment, repeated comparison and metric limits.
Use identical helper sources/compiler/settings with each version's SDK module
graph; retain `go version -m` for both binaries. Run Linux unit/race tests with
`go test -race ./test/performance/transport`.

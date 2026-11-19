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

The engine repository's `test/performance/README.md` describes the isolated Docker
fixture, forwarding-router impairment, repeated comparison and metric limits.
Use identical helper sources/compiler/settings with each version's SDK module
graph; retain `go version -m` for both binaries. Run Linux unit/race tests with
`go test -race ./test/performance/transport`.

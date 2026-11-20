# Upgrade the QUIC companion libraries together

The SDK and CLI use the dependency graph in [go.mod](../go.mod). QUIC 0.64.1 was evaluated with WebTransport 0.13, MASQUE 0.6 and CONNECT-IP 0.6 in a separate qualification configuration.

[QUIC v0.65.0](https://github.com/quic-go/quic-go/releases/tag/v0.65.0) changes the `HTTPStream` signature and how callers send the HTTP status. WebTransport 0.13 and MASQUE 0.6 use the earlier signature. CONNECT-IP 0.7 requires QUIC 0.65.

Adopt compatible published releases together, then verify proxy sessions, stream ownership, cancellation and mixed-client behavior. The native datagram receive-history finding remains a separate gate in the [datagram qualification method](../test/performance/000-datagram-qualification.md); an API-compatible build does not establish that it is resolved.

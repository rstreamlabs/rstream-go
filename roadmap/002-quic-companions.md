# Upgrade the QUIC companion libraries together

Upgrade QUIC, WebTransport and MASQUE as a compatible set once releases support the same HTTP/3 stream API. Verify proxy sessions, stream shutdown and cancellation, and interoperability with existing clients.

Verify native datagram delivery under reordering separately using the [datagram tests](../test/performance/000-datagram-qualification.md).

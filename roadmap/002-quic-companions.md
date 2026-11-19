# Upgrade the QUIC companion libraries together

Keep QUIC 0.64.1, WebTransport 0.13, MASQUE 0.6 and CONNECT-IP 0.6 as a compatible set. QUIC 0.65 changes an HTTP/3 stream API used by the companions; CONNECT-IP 0.7 cannot be upgraded independently.

Adopt compatible published releases together, then verify proxy sessions, stream ownership, cancellation and mixed-client behavior. The native datagram receive-history finding remains a separate gate in the [datagram qualification method](../test/performance/000-datagram-qualification.md); an API-compatible build does not establish that it is resolved.

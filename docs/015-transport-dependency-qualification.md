# Transport security maintenance qualification

This branch updates x/crypto to 0.59.0 and x/sys to 0.50.0 while retaining the
qualified WebRTC 4.2.20 / ICE 4.4.2 / SCTP 1.11.1 stack and the verified ICE
URL snapshot patch. It does not change QUIC versions, introduce DTLS 1.3 or
represent a stable SDK publication.

The separate [Pion maintenance candidate](https://github.com/rstreamlabs/rstream-go/pull/186)
remains draft because repeated TURN/ICE restart transfers regress with the
existing native Node ARM64 provider. Browser and x86_64 successes do not certify
that consumer. Its technical record contains the public-API-only control and
crossed TURN-server results. No Pion source is patched as a workaround.

CI builds this branch's owner and an immutable JS consumer, then runs direct and
forced-TURN transfers, permissions, cancellation, ESM and authenticated renewal.
Both native Ubuntu x86_64 and ARM64 must pass the full suite and three additional
renewal transfers. The TURN fixture uses an active non-loopback IPv4 address;
HTTP data fallback remains disabled in WebRTC qualification. Existing Go/Go race,
FIPS and platform gates remain required.

Consumer modules still need their own verified ICE modfile because Go does not
inherit dependency replacements. Keep its source/version/hash checks; do not
substitute the unqualified newer ICE graph to make dependency versions uniform.

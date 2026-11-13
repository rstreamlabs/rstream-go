# Transport dependency qualification

The maintenance candidate uses WebRTC 4.2.26, ICE 4.4.9, SCTP 1.12.1 and TURN
5.1.3. QUIC versions belong to the separate coordinated upgrade. This source
branch does not represent a stable SDK release or a production rollout.

Keep the [verified ICE snapshot patch](../tools/icepatch/README.md): official
4.4.9 still fails the URL-update race test. Consuming modules must prepare their
own verified modfile; Go does not inherit replacements from dependencies.

## Open interoperability gate

**The Pion upgrade is not qualified for the existing Node ARM64 consumer.**
The immutable real JS consumer runs in SDK CI on native Ubuntu x86_64 and ARM64.
Both run the complete suite and, after it passes, three additional renewal cases.
On 13 November 2026, x86_64 passes all runs; ARM64 passes eight cases but times
out in the 30-second renewed/restarted TURN transfer. Local Linux ARM64 Docker
reproduces the same case on three consecutive runs; the preceding graph passes
three times in 9.5–9.8 seconds. No assertions, payload or deadlines are relaxed.

Modern Chromium 156 and Firefox 157 on macOS pass the candidate's real 8 MiB
transfer with three/four authenticated ICE restarts in 7.4/8.3 seconds. The
separate native `@roamhq/wrtc` 0.10.0 macOS gathering limit reproduces on both
old and new graphs. These results distinguish browser, architecture and provider;
a green Go/Go suite or x86_64 CI does not certify Node ARM64.

The [public Pion control](../test/pion-ice-restart/README.md) reproduces the
old-pass/new-timeout distinction without rstream signaling, its bounded UDP
adapter or ICE patch. Crossing old/new TURN servers preserves that distinction,
so the TURN server alone is not its explanation. It does not identify the exact
Pion component or prove a universal Pion bug. No upstream issue has been posted.

Other diagnostic controls exclude SCTP alone, the local ICE patch, the bounded
UDP adapter, candidate SDP extensions and Transport 5.1.1/5.1.5 as sufficient
explanations. Awaiting a newly selected pair passed isolated trials but failed
the complete real suite; that speculative SDK change was discarded. Merely
waiting for `connected` can observe the preceding pair and also did not fix it.

Keep this candidate in draft. Deliver independent security maintenance with the
preceding WebRTC/ICE stack, and repin compatible consumers to that qualified
source. Do not retire useful forks/patches, omit ARM64, increase timeouts or
count the failed interoperability run as qualification. QUIC datagram diagnosis
is a separate unresolved finding.

The fixture binds TURN to an active non-loopback IPv4 interface so native
libwebrtc can use its interface-bound sockets. Cross-fixture controls confirm
old-pass/new-fail with both loopback and interface binding; this fixture change
is not the cause of the dependency-dependent result.

## Reproduce the consumer gate

Build the companion server with the verified graph, then run the JS repository's
filesystem e2e suite against that executable:

```sh
make ice-patch
go build -modfile=out/ice-patch/go.mod -o out/files-owner ./filesystem/rtc/testdata/server
# In rstream-js, after npm ci and building filesystem and webtty:
RSTREAM_FILES_E2E_SERVER=/absolute/path/to/out/files-owner npm -w @rstreamlabs/filesystem run test:e2e
```

Retain the source revisions and actual selected module graph alongside results.
The suite fails on missing dependencies; a skipped test is not qualification.

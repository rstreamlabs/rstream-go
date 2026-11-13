# Transport dependency qualification

The maintenance candidate uses WebRTC 4.2.26, ICE 4.4.9, SCTP 1.12.1 and TURN
5.1.3. QUIC versions belong to the separate coordinated upgrade. This source
branch does not represent a stable SDK release or a production rollout.

Keep the [verified ICE snapshot patch](../tools/icepatch/README.md): official
4.4.9 still fails the URL-update race test. Consuming modules must prepare their
own verified modfile; Go does not inherit replacements from dependencies.

## Open interoperability gate

The real Go/Node filesystem suite passes direct and forced-TURN transfers,
read-only enforcement, cancellation, authorization failures and ESM loading on
Linux. Its transfer across credential renewal and frequent ICE restarts times
out with this candidate and passes with the preceding dependency graph. Full
Go/Go race suites and the engine's direct/cross-region runtime matrix pass;
these do not certify the failing Node path.

Diagnostic module-version controls exclude SCTP alone, the local ICE patch,
the bounded UDP adapter, candidate SDP extensions and Transport 5.1.1/5.1.5 as
sufficient explanations. Root cause and correction are still under review.
Do not retire the patch, weaken assertions, increase the transfer timeout or
mark the candidate ready merely because ordinary CI passes.

The test fixture binds TURN to an active non-loopback IPv4 interface so native
libwebrtc can use its interface-bound sockets. On the local macOS environment,
`@roamhq/wrtc` 0.10.0 also remains in gathering after producing valid relay
candidates, on both old and new Go graphs. That separate environmental/provider
limit is not evidence of a new Go dependency regression.

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

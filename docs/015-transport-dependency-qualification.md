# Transport dependency qualification

This candidate uses WebRTC 4.2.26, ICE 4.4.9, SCTP 1.12.1 and TURN 5.1.3.
QUIC versions remain a separate upgrade. Keep the
[verified ICE URL snapshot patch](../tools/icepatch/README.md): official ICE
4.4.9 still reproduces that race. Consuming modules need their own verified
modfile; Go does not inherit dependency replacements.

## Restart recovery

Initial candidate transfers exposed a restart-cadence weakness: signaling may
finish while ICE is still selecting a path, and selection may precede reliable
SCTP recovery. Replacing the path again during this interval can stall a valid
transfer. The initial Node ARM64 case fails consistently; repeated x86_64 runs
also expose it after successful trials. This does not establish a Pion defect.

The Go client now owns at most one bounded renegotiation worker. Authorization
renewal continues independently while it waits for selection and reliable
progress. Close cancels the owner and joins its workers. The selected-pair
observer uses a bounded nonblocking notification and rechecks actual selection;
a stale/coalesced event cannot release it.

An empty-queue read actively awaiting data during recovery receives a 20-second
budget. Paused consumers retain their bounded receive window and authorization
renewals without an idle timeout. Atomic progress/snapshot checks prevent a late
recovery marker from hiding data already received. Completion and cancellation
retain their existing stream semantics. No Pion source is patched as a workaround.

The companion JS implementation is source
`9b170e37165de684ad2614fc3d9638614dd24a77`; its typed selection and recovery
helpers enforce the same ownership, authorization and backpressure rules. See
its filesystem technical recovery document for provider statistics requirements.
CI pins that immutable source and runs native Ubuntu x86_64 and ARM64: full real
Go/Node transfer suite plus repeated original renewal stress. The corrected
runtime at Go source `86f80e042235d2438cb5b34990d7c3474ca0dff6` passes both
architectures, including three additional stress repetitions. Keep those gates
required for subsequent changes; source qualification does not authorize merge
or stable publication.

Local validation includes full Go Linux race/vet, frozen strict FIPS and source
govulncheck. RTC tests cover consumer-paced 8 MiB TURN transfers, short leases,
close during negotiation, stale selection events, cancellation, paused readers
and concurrent progress. JS's twelve real cases pass with the candidate server,
including the unchanged original 30-second payload/hash/restart stress, a
three-second lease, a 21-second paused reader and denied renewal. Chromium and
Firefox trials also pass; these do not substitute for the native CI matrix.

## Persistent baseline controls

The [public Pion control](../test/pion-ice-restart/README.md) retains the failing
fast-replacement baseline using unmodified Pion modules, without rstream
signaling, its UDP adapter or the ICE patch. Crossed old/new TURN servers retain
the old/new distinction, excluding the server alone as the explanation. A control
without repeated SetConfiguration also fails. The application recovery correction
does not depend on identifying a particular upstream implementation defect.

SCTP alone, the local ICE patch, bounded UDP writer, candidate SDP extensions and
Transport 5.1.1/5.1.5 were not sufficient explanations. Waiting only for a selected
pair passed isolated trials but failed the full suite; blocking authorization
while waiting also expired a short lease. Those incomplete approaches were
replaced with the independent renewal/recovery lifecycle above. No upstream issue
is posted. Original failed evidence is retained rather than counted as qualified.

The fixture binds TURN to an active non-loopback IPv4 interface. Cross-fixture
controls reproduce old-pass/new-fail with both loopback and interface binding.
The separate native @roamhq/wrtc 0.10.0 macOS gathering limitation reproduces on
both graphs; this correction does not claim to fix that provider behavior.

## Embedded browser and release coupling

The Go embedded file browser is regenerated with the corrected filesystem package
from the immutable JS source above, using the product's locked generator inputs.
Its HTML, gzip and CSP hashes are checked by Go tests and the actual browser
matrix. The ordinary product lock remains on the published package; a local
candidate build does not publish a replacement version to npm.

See the product's `tools/build/files-browser-candidates.md` for reproduction and
source/package fingerprint recording. Coordinate stable JS publication, product
lock repinning and the final asset regeneration in the later authorized release
phase. Current source/PR qualification is not a stable SDK publication or rollout.

## Reproduce the real consumer gate

```sh
make ice-patch
go build -modfile=out/ice-patch/go.mod -o out/files-owner ./filesystem/rtc/testdata/server
# In the pinned JS checkout, after npm ci and building filesystem and webtty:
RSTREAM_FILES_E2E_SERVER=/absolute/path/to/out/files-owner npm -w @rstreamlabs/filesystem run test:e2e
```

Retain the resolved module graph, source revisions, provider and architecture.
The suite fails on missing dependencies; skipping it is not qualification. Keep
project security policy, certificate checks, authorization and the independent
QUIC datagram acceptance gate unchanged.

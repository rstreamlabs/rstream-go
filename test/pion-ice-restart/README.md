# Public Pion ICE restart control

This isolated module retains the failing fast-replacement baseline independently
of rstream signaling, authorization, its UDP adapter and verified ICE snapshot patch.
It uses only published Pion APIs and unmodified modules. It does not implement a
production service: its fixed local TURN credentials and signaling endpoint are
synthetic test inputs, and every listener/process belongs to the test.

The Node consumer receives 256 ordered 32 KiB messages with a 32-message credit
window, checks every byte, and consumes one message every 25 ms. It restarts ICE
every two seconds while transferring. The transfer must finish within 30 seconds;
the consumer closes its peer and owned child processes on completion or failure.
No HTTP data fallback exists.

Run with Go 1.27.4 and Node 24. Install the JS repository's locked dependencies,
then point `NODE_PATH` at that checkout's `node_modules` so the control loads its
actual optional `@roamhq/wrtc` 0.10.0 provider. Build and run both cases separately:

```sh
go build -modfile=old.mod -o old-owner .
NODE_PATH=/absolute/rstream-js/node_modules SERVER="$PWD/old-owner" node client.cjs

go build -o candidate-owner .
NODE_PATH=/absolute/rstream-js/node_modules SERVER="$PWD/candidate-owner" node client.cjs
```

`old.mod` selects WebRTC 4.2.20 / ICE 4.4.2 / SCTP 1.11.1 / TURN 5.1.0;
`go.mod` selects WebRTC 4.2.26 / ICE 4.4.9 / SCTP 1.12.1 / TURN 5.1.3.
Both keep DTLS 3.1.15. Record `go list -m all` with the chosen modfile, architecture,
kernel/container and native provider; changing the root dependency alone does
not describe the resolved graph.

Optional `TURN_SERVER=/absolute/other-owner` runs the other graph's TURN server
as a separately owned process while `SERVER` provides WebRTC. This isolates TURN
server changes from the client graph. It changes neither graph's Pion sources.

On 13 November 2026, Linux ARM64 Docker on macOS reproduces old-pass/new-timeout,
including old-WebRTC/new-TURN and new-WebRTC/old-TURN controls. This does not
establish a universal upstream defect. Initial native Ubuntu x86_64 runs pass,
but a later repeated SDK transfer also times out there. Modern Chromium/Firefox
trials pass against the candidate SDK on macOS.
See [the qualification record](../../docs/015-transport-dependency-qualification.md)
for the application gate and current limitations. No upstream issue has been
posted. The corrected SDK serializes selection/reliable recovery while renewing
authorization independently; this control intentionally retains the old cadence.
Do not present its failure as a diagnosed Pion bug.

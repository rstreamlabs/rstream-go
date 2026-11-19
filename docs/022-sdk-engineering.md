# Go SDK engineering

Use this guide when changing the SDK or CLI. [Build from source](001-building-from-source.md)
sets up the toolchain and verified dependency graph; [Contributing](../CONTRIBUTING.md)
gives the change workflow. [CODING_STYLE.md](../CODING_STYLE.md) defines the shared
style and reliability rules.

## Ownership and synchronization

| Concern                | Repository practice                                                                                                               |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Mutable state          | Give each field an owner or explicit synchronization rule. State whether a mutex, channel loop or immutable snapshot protects it. |
| Locks                  | Keep critical sections small. Release shared-state locks before blocking I/O, waiting for workers or invoking external callbacks. |
| Goroutines and timers  | Attach them to a bounded lifecycle with a stop signal and a join/completion path. The owner observes errors and shutdown.         |
| Channels               | Define who sends, closes and drains them, and bound buffered work. Cancellation must unblock blocked senders/receivers.           |
| Contexts and deadlines | Propagate them through setup and I/O. Retain the operation's resources until cancellation has completed.                          |
| Close/retry/revoke     | Make concurrent and repeated calls safe; prevent a retired generation from affecting its successor.                               |

For connection code, track partial reads/writes, final bytes returned with an
error and the difference between message boundaries and stream framing. Reuse
buffers in hot paths only while their previous operation has released ownership.
Choose limits for peers, pending operations, queues and leases before adding
background work.

## Configuration and public behavior

CLI flags, apply YAML and Docker labels share `cmd/rstream/internal/tunnelconfig`.
WebTTY CLI/YAML options use the typed bindings catalog. Preserve explicit false,
zero and empty values, normalize before runtime I/O and extend the source-parity
inventories when adding options. The public API, CLI and documentation should
express the same policy.

Keep certificate verification, project admission and authentication intact.
Errors should identify the failed operation without logging credentials or
untrusted peer content. Authentication/crypto changes follow the security and
cross-language vector requirements in Contributing.

## Protocol and dependency changes

`pb/rstream.proto` owns the wire schema and protocol version; `go.mod` and the
compiled-graph tests own the selected transport dependencies. Keep user guides
focused on the current behavior. The [release guide](019-cli-releases.md) owns
compatibility checks and component promotion order.

Private native-datagram routing negotiates an independent dialer identifier in
`StreamReq`/`StreamRsp`. The datagram prefix uses the returned 12-byte identifier;
lifecycle messages retain `stream_id`. Preserve routing fallback when the
optional field is absent, reject malformed/zero/non-independent identifiers and
keep both endpoints' close behavior covered. TLS and guaranteed-delivery
paths use stream framing separately.

QUIC, WebTransport and MASQUE companion dependencies share HTTP/3 APIs. Check
their resolved graph together, including generated modfiles and the verified
ICE replacement. Consumers resolve their own Go module graph; dependency
replacements in this repository apply to its build only.

## Tests for changed behavior

| Change                | Required evidence                                                                                                              |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Bug fix               | A regression through the owning public entry point that fails on the old behavior.                                             |
| Configuration choice  | Representative coverage of each path and source-parity tests where inputs share normalization.                                 |
| Concurrency/lifecycle | Parallel use, failure, cancellation, retry and shutdown under the race detector. Assert resource bounds as well as completion. |
| Protocol framing      | Partial/final I/O, malformed input, peer loss and supported stream/datagram paths.                                             |
| External dependency   | A real configured integration fixture for required backend behavior; keep optional skips explicit.                             |

Use deterministic barriers/channels to control concurrency tests rather than
sleeps for ordering. Follow the [test guide](../test/README.md) for fixture setup,
then run the owning suites, race checks and vet from the prepared source graph.

# Coding Style

Code must stay compact, minimal, and readable.

No empty lines inside functions.

Only useful comments when they clarify a non-obvious choice.

Errors must be handled explicitly. Log actionable messages.

Only add dependencies that are widely used, maintained, and appropriate. Keep dependency surface small.

Prefer clear naming and simple control flow over cleverness.

Keep allocations low in hot paths. Reuse data, avoid repeated parsing/formatting when possible.

## Reliability

Every mutable field has one owner or an explicit synchronization rule.

Never perform blocking I/O while holding a shared-state lock. Correctness-critical I/O must carry a context, a deadline, and an explicit failure policy.

Asynchronous work must be bounded, observable, and owned by a lifecycle. Goroutines, timers, channels, and leases need a deterministic shutdown path.

Close, release, revoke, and retry paths must be safe under concurrent and repeated calls.

## Tests

Every bug fix needs a regression test that fails before the fix.

Test behavior through its public entry point. Every mode, transport, output, or configuration choice that selects a distinct execution path needs a representative test.

Concurrency and lifecycle changes must cover parallel use, cancellation, failure, retry, and shutdown under the race detector.

Concurrency tests must assert resource bounds such as connections, goroutines, queues, or leases, not only successful completion.

Required integration tests must exercise real dependencies and must not pass by skipping them.

# Coding Style

Code must stay compact, minimal, and readable.

No empty lines inside functions.

Only useful comments when they clarify a non-obvious choice.

Errors must be handled explicitly. Log actionable messages.

Never block critical paths. Any I/O must be async / best-effort and must not impact latency.

Only add dependencies that are widely used, maintained, and appropriate. Keep dependency surface small.

Prefer clear naming and simple control flow over cleverness.

Keep allocations low in hot paths. Reuse data, avoid repeated parsing/formatting when possible.

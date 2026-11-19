# Go diagnostic API

Import `github.com/rstreamlabs/rstream-go/doctor` to run the same diagnostics as
`rstream doctor` inside a Go process. Use the selected CLI context or a configuration supplied by the application. The Go process runs the checks itself.

```go
import (
    "context"
    "time"

    "github.com/rstreamlabs/rstream-go/config"
    "github.com/rstreamlabs/rstream-go/doctor"
)

func diagnose(ctx context.Context) (doctor.Report, error) {
    resolution, err := config.ResolveFromEnv(config.ClientEnvOptions{})
    if err != nil {
        return doctor.Report{}, err
    }
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    return doctor.Run(ctx, resolution.Resolved, doctor.Options{})
}
```

`config.ResolveFromEnv` is an explicit opt-in to the shared YAML configuration,
environment overrides and credential stores. If the application already has a
`config.Resolved`, pass it directly to `doctor.Run`; the runner does not load a
configuration file or discover credentials from the process environment. A
custom configuration can also be resolved with `config.Resolve`. Regional
project selection, configured Control plane headers, TLS/QUIC transport options
and mTLS identities (including external signers) are preserved.

## Results and errors

`Run(ctx, resolved, options)` returns `(doctor.Report, error)`.

| Result | How to use it |
| --- | --- |
| `Report.Checks` | Show each check's status, message and optional details. The status can be pass, warn, fail or skip. |
| `Report.Summary` | Count checks by status. A failure makes `Report.Err()` return `doctor.ErrChecksFailed`. |
| `doctor.ErrChecksFailed` | Use `errors.Is` to identify failed checks and keep the report for diagnostics. |
| Cancellation or deadline | Check `context.Canceled` or `context.DeadlineExceeded` first with `errors.Is`. The error can also match `ErrChecksFailed`. Use the partial report for the failed probe. |

Use `encoding/json` for the same structured fields as the CLI, or use the typed
results to present them in your application.

The shared checks cover context, token claims or mTLS identity/signatures,
Control plane authentication, project status, engine address, DNS, TLS and
QUIC connectivity, selected tunnel transport, and Engine readiness/inventory.
Unavailable projects produce an actionable project failure and skip transport
probes. mTLS agents probe native admission, dedicated API discovery, and Engine HTTP readiness/inventory without requiring a token. Insufficient inventory rights produce a warning; they are not classified as invalid certificates. The runner does not mint TURN credentials.
The restricted FIPS build also validates its cryptographic runtime before
starting diagnostics.

The CLI additionally loads its config and resolves flags/environment. It adds
its existing `config` check and `configPath` metadata, or reports a config/context
resolution failure before calling the runner. Its table/JSON formats, flags and
nonzero exit on failed checks are preserved.

## Optional deep probe

```go
report, err := doctor.Run(ctx, resolved, doctor.Options{Deep: true})
```

`Deep` is false by default. When enabled, it creates and closes a temporary
private tunnel. If the project does not support private tunnels, the existing
published HTTP fallback is attempted. This check uses the configured identity's
permissions and can briefly publish an endpoint; enable it intentionally. No
temporary tunnel is created in the standard mode.

## Lifetimes and repeated use

Pass a non-nil `context.Context`; set a deadline when the entire run needs a time
budget. The runner also bounds individual probes and reports them in a stable order.

The runner closes the clients, control channels and temporary tunnels it owns,
including on failures and cancellation. It never takes ownership of a transport
supplied by the caller. Do not mutate the input config, maps, TLS settings or
transports while diagnostics use them; custom transports and credential callbacks
must support the concurrency the application requests. Run calls have independent
reports and can execute concurrently with an immutable configuration.

// See LICENSE file in the project root for license information.

package doctor

import (
	"context"
	"errors"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

// ErrChecksFailed means at least one check failed. The report remains usable.
var ErrChecksFailed = errors.New("one or more doctor checks failed")

// Options controls optional diagnostics. The zero value runs standard checks.
type Options struct {
	// Deep creates and closes a temporary tunnel, with a published HTTP fallback
	// when the project does not support private tunnels.
	Deep bool
}

// Run diagnoses a resolved SDK configuration without reading CLI flags, loading
// configuration files, consulting environment variables for credentials, or
// writing formatted output. The caller must not mutate resolved during Run.
// Each probe has a timeout; ctx can set a shorter deadline for the entire run.
// Run owns and closes its probe resources, but never closes a caller's transport.
// It returns ErrChecksFailed for failed diagnostics, and preserves context errors
// for errors.Is. Cancellation returns the checks completed so far.
func Run(ctx context.Context, resolved config.Resolved, options Options) (Report, error) {
	report := Report{Version: rstream.Version, Channel: rstream.Channel, GeneratedAt: time.Now().UTC()}
	if ctx.Err() != nil {
		return finish(report, ctx)
	}
	if rstream.FIPSProfileEnabled() {
		if err := rstream.RequireFIPS(); err != nil {
			report.add("fips", StatusFail, err.Error(), nil)
			return finish(report, ctx)
		}
	}
	if err := resolveRegion(ctx, &resolved); err != nil {
		report.add("context", StatusFail, err.Error(), nil)
		return finish(report, ctx)
	}
	report.APIURL = resolved.APIURL
	report.ContextName = resolved.ContextName
	report.Engine = resolved.Engine
	if resolved.Context != nil {
		report.ProjectEndpoint = resolved.Context.ProjectEndpoint
	}
	checkDoctorContext(&report, resolved)
	checkDoctorAuthentication(ctx, &report, resolved)
	if ctx.Err() != nil {
		return finish(report, ctx)
	}
	projectReady := true
	if doctorUsesControlPlane(resolved) && !resolved.HasMTLS() {
		checkDoctorControlPlane(ctx, &report, resolved)
		if ctx.Err() != nil {
			return finish(report, ctx)
		}
		projectReady = checkDoctorProject(ctx, &report, resolved)
	} else {
		report.add("control_plane_auth", StatusSkip, "agent connection does not require a Control plane token", nil)
		report.add("project", StatusSkip, "using the configured Engine directly", nil)
	}
	if ctx.Err() != nil {
		return finish(report, ctx)
	}
	if projectReady {
		checkDoctorNetwork(ctx, &report, resolved)
		if ctx.Err() != nil {
			return finish(report, ctx)
		}
		checkDoctorEngine(ctx, &report, resolved)
	} else {
		message := "project must be active before transport checks"
		for _, name := range []string{"engine_address", "dns", "tls", "quic_transport", "tunnel_transport", "engine"} {
			report.add(name, StatusSkip, message, nil)
		}
	}
	if ctx.Err() != nil {
		return finish(report, ctx)
	}
	if options.Deep && projectReady {
		checkDoctorTunnelCreation(ctx, &report, resolved)
	} else if options.Deep {
		report.add("tunnel_creation", StatusSkip, "project must be active before a tunnel lifecycle probe", nil)
	}
	return finish(report, ctx)
}

// Err reports whether diagnostics failed. Warnings and skipped checks are not errors.
func (r Report) Err() error {
	if r.Summary.Fail > 0 {
		return ErrChecksFailed
	}
	return nil
}

func finish(report Report, ctx context.Context) (Report, error) {
	if err := ctx.Err(); err != nil {
		report.add("execution", StatusFail, err.Error(), nil)
	}
	report.finalize()
	return report, errors.Join(report.Err(), ctx.Err())
}

func resolveRegion(ctx context.Context, resolved *config.Resolved) error {
	if resolved.Region == "" {
		return nil
	}
	if resolved.Context == nil || resolved.Context.ProjectEndpoint == "" {
		return errors.New("managed project endpoint is required for region selection")
	}
	token := resolved.Token
	if token == "" && resolved.Environment != nil {
		var err error
		token, _, err = config.TokenFromAuth(resolved.Environment.Auth)
		if err != nil {
			return err
		}
	}
	client := controlplane.NewClient(resolved.APIURL, token, controlplane.WithHeaders(resolved.ControlPlaneHeaders))
	defer client.CloseIdleConnections()
	if err := client.RequireToken(); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	project, err := client.ResolveProjectByEndpoint(runCtx, resolved.Context.ProjectEndpoint)
	if err != nil {
		return mapControlPlaneError(err)
	}
	return config.ResolveProjectRegion(resolved, project)
}

func closeClient(report *Report, client *rstream.Client) {
	if err := client.Close(); err != nil {
		report.add("cleanup", StatusFail, "failed to close diagnostic client", map[string]string{"error": err.Error()})
	}
}

func mapControlPlaneError(err error) error {
	if errors.Is(err, controlplane.ErrAccessProtection) {
		return errors.New("control plane access was intercepted by deployment protection (configure the required control-plane headers for this context)")
	}
	if errors.Is(err, controlplane.ErrUnauthorized) {
		return errors.New("not authenticated (run rstream login or set RSTREAM_AUTHENTICATION_TOKEN)")
	}
	if errors.Is(err, controlplane.ErrForbidden) {
		return errors.New("not authorized (check token permissions and project access)")
	}
	return err
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/reconciler"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runapply"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/rundocker"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runengine"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "run",
	Short:        "Run declarative tunnels (YAML or Docker)",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyPath, _ := cmd.Flags().GetString("apply")
		useDocker, _ := cmd.Flags().GetBool("docker")
		watch, _ := cmd.Flags().GetBool("watch")
		if applyPath == "" && !useDocker {
			return errors.New("either --apply or --docker is required")
		}
		if applyPath != "" && useDocker {
			return errors.New("--apply and --docker are mutually exclusive")
		}
		logger := slog.With("component", "run")
		ctx := cmd.Context()
		res, cfg, env, err := loadRuntimeContext(cmd)
		if err != nil {
			return err
		}
		fallback := runmodel.ResolvedContext{
			Name:      res.ContextName,
			Engine:    res.Engine,
			Token:     res.Token,
			Transport: res.Transport,
		}
		lookup := func(name string) (runmodel.ResolvedContext, error) {
			resolved, err := resolveNamedContext(cfg, env, cmd, name)
			if err != nil {
				return runmodel.ResolvedContext{}, err
			}
			return runmodel.ResolvedContext{
				Name:      name,
				Engine:    resolved.Engine,
				Token:     resolved.Token,
				Transport: resolved.Transport,
			}, nil
		}
		starter := runengine.New(
			runengine.WithLogger(slog.With("component", "run.tunnel")),
			runengine.WithRetry(1*time.Second, 30*time.Second),
		)
		recon := reconciler.New(ctx, starter, slog.With("component", "reconciler"))
		defer recon.Stop()
		if applyPath != "" {
			source := runapply.NewSource(applyPath, fallback, lookup, slog.With("component", "run.apply"))
			return runLoop(ctx, source, recon, watch, logger)
		}
		dockerSocket, _ := cmd.Flags().GetString("docker-socket")
		dockerNetwork, _ := cmd.Flags().GetString("docker-network")
		dockerDefault, _ := cmd.Flags().GetString("docker-default-context")
		dockerAllowContextLabels, _ := cmd.Flags().GetBool("docker-allow-context-labels")
		if dockerDefault != "" {
			resolved, err := lookup(dockerDefault)
			if err != nil {
				return err
			}
			fallback = resolved
		}
		source, err := rundocker.NewSource(dockerSocket, dockerNetwork, fallback, lookup, dockerAllowContextLabels, slog.With("component", "run.docker"))
		if err != nil {
			return err
		}
		defer source.Close()
		return runLoop(ctx, source, recon, watch, logger)
	},
}

func init() {
	runCmd.Flags().SortFlags = false
	runCmd.PersistentFlags().SortFlags = false
	runCmd.Flags().String("apply", "", "path to tunnels YAML")
	runCmd.Flags().Bool("docker", false, "discover tunnels from Docker")
	runCmd.Flags().Bool("watch", false, "watch for changes and reconcile")
	runCmd.Flags().String("docker-socket", "unix:///var/run/docker.sock", "Docker socket")
	runCmd.Flags().String("docker-network", "", "Docker network for bare-port resolution (multi-network containers)")
	runCmd.Flags().String("docker-default-context", "", "default context for Docker tunnels")
	runCmd.Flags().Bool("docker-allow-context-labels", false, "allow containers to select rstream contexts with rstream.context labels")
	runCmd.MarkFlagsMutuallyExclusive("apply", "docker")
	rootCmd.AddCommand(runCmd)
}

func runLoop(ctx context.Context, source interface {
	List(context.Context) ([]runmodel.DesiredTunnel, error)
	Watch(context.Context) (<-chan struct{}, error)
}, recon *reconciler.Reconciler, watch bool, logger *slog.Logger) error {
	apply := func() error {
		desired, err := source.List(ctx)
		if err != nil {
			logger.Warn("Failed to load desired state", "error", err)
			return err
		}
		if err := recon.Reconcile(desired); err != nil {
			logger.Warn("Failed to reconcile desired state", "error", err)
			return err
		}
		return nil
	}
	if err := apply(); err != nil && !watch {
		return err
	}
	if !watch {
		<-ctx.Done()
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return ctx.Err()
	}
	ch, err := source.Watch(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			_ = apply()
		}
	}
}

func loadRuntimeContext(cmd *cobra.Command) (config.Resolved, config.Config, config.EnvSettings, error) {
	runtime, err := resolveRuntime(cmd, false, false)
	if err != nil {
		return config.Resolved{}, config.Config{}, config.EnvSettings{}, err
	}
	env := config.ReadEnv()
	return runtime.Resolved, runtime.Config, env, nil
}

func resolveNamedContext(cfg config.Config, env config.EnvSettings, cmd *cobra.Command, name string) (config.Resolved, error) {
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagRegion, _ := cmd.Flags().GetString("region")
	input := config.ResolveInput{
		Config:                 cfg,
		FlagAPIURL:             flagAPIURL,
		FlagContext:            name,
		FlagRegion:             flagRegion,
		EnvAPIURL:              env.APIURL,
		EnvContext:             env.Context,
		EnvEngine:              env.Engine,
		EnvToken:               env.Token,
		EnvRegion:              env.Region,
		EnvControlPlaneHeaders: env.ControlPlaneHeaders,
		RequireEngine:          true,
		RequireToken:           true,
		ResolveToken:           true,
	}
	resolved, err := config.Resolve(input)
	if err != nil {
		return config.Resolved{}, err
	}
	if resolved.Region != "" {
		if err := resolveRuntimeRegion(cmd, cfg, &resolved); err != nil {
			return config.Resolved{}, err
		}
	}
	return resolved, nil
}

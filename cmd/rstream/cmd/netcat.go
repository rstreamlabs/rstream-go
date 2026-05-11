// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

const (
	defaultNetcatOpenTimeout = 10 * time.Second
	defaultNetcatMaxConns    = 64
)

type netcatDialer func(context.Context) (net.Conn, error)

type netcatListenerResult struct {
	Listener  net.Listener
	Display   string
	Generated bool
}

type netcatListenerFactory func(context.Context) (*netcatListenerResult, error)

type netcatClientConfig struct {
	Target      string
	Interactive bool
	HalfClose   bool
	Dial        netcatDialer
	Stdin       io.Reader
	Stdout      io.Writer
	Logger      *slog.Logger
}

type netcatExecConfig struct {
	Command string
	Shell   bool
}

type netcatServerConfig struct {
	Listen              netcatListenerFactory
	DownstreamHalfClose bool
	UpstreamHalfClose   bool
	Upstream            netcatDialer
	Exec                *netcatExecConfig
	OpenTimeout         time.Duration
	MaxConnections      int
	Stderr              io.Writer
	Logger              *slog.Logger
}

var netcatCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "netcat [options] <remote>",
	Aliases:      []string{"ncat", "nc"},
	Short:        "Netcat-like utility supporting TCP and rstream tunnels",
	Args:         cobra.ArbitraryArgs,
	SilenceUsage: true,
	Example: `  rstream nc 127.0.0.1:1234
  rstream nc -L 127.0.0.1:1234 -c "date"
  rstream nc rstrm://ssh-server
  rstream nc -L rstrm://ssh-server -R 127.0.0.1:22`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateNetcatFlags(cmd, args); err != nil {
			return err
		}
		logger := slog.With("cmd", "netcat")
		if netcatUsesServerMode(cmd) {
			cfg, err := newNetcatServerConfig(cmd, logger)
			if err != nil {
				return err
			}
			return runNetcatServer(cmd.Context(), cfg)
		}
		cfg, err := newNetcatClientConfig(cmd, logger, args[0])
		if err != nil {
			return err
		}
		return runNetcatClient(cmd.Context(), cfg)
	},
}

func init() {
	netcatCmd.Flags().SortFlags = false
	netcatCmd.PersistentFlags().SortFlags = false
	netcatCmd.Flags().StringP("listen", "L", "", "listen endpoint for server mode (host:port or rstrm://[name])")
	netcatCmd.Flags().StringP("remote", "R", "", "upstream endpoint for server proxy mode (host:port or rstrm://<id-or-name>)")
	netcatCmd.Flags().StringP("exec", "e", "", "run a command without shell interpretation")
	netcatCmd.Flags().StringP("sh-exec", "c", "", "run a command by passing it to a system shell")
	netcatCmd.Flags().BoolP("interactive", "i", false, "enable interactive mode")
	netcatCmd.Flags().BoolP("no-interactive", "I", false, "disable interactive mode")
	netcatCmd.Flags().Int("max-connections", defaultNetcatMaxConns, "maximum concurrent server-mode connections")
	netcatCmd.MarkFlagsMutuallyExclusive("interactive", "no-interactive")
	netcatCmd.MarkFlagsMutuallyExclusive("remote", "exec", "sh-exec")
	rootCmd.AddCommand(netcatCmd)
}

func netcatUsesServerMode(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("listen")
}

func validateNetcatFlags(cmd *cobra.Command, args []string) error {
	serverMode := netcatUsesServerMode(cmd)
	if serverMode {
		if len(args) != 0 {
			return fmt.Errorf("server mode does not accept positional arguments")
		}
		remoteSet := cmd.Flags().Changed("remote")
		execSet := cmd.Flags().Changed("exec")
		shExecSet := cmd.Flags().Changed("sh-exec")
		modeCount := 0
		for _, set := range []bool{remoteSet, execSet, shExecSet} {
			if set {
				modeCount++
			}
		}
		if modeCount == 0 {
			return fmt.Errorf("server mode requires exactly one of --remote, --exec, or --sh-exec")
		}
		if modeCount > 1 {
			return fmt.Errorf("server mode accepts only one of --remote, --exec, or --sh-exec")
		}
		if cmd.Flags().Changed("interactive") || cmd.Flags().Changed("no-interactive") {
			return fmt.Errorf("--interactive and --no-interactive are only valid in client mode")
		}
		maxConnections, _ := cmd.Flags().GetInt("max-connections")
		if maxConnections <= 0 {
			return fmt.Errorf("--max-connections must be greater than 0")
		}
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("client mode requires exactly one remote endpoint")
	}
	if cmd.Flags().Changed("remote") || cmd.Flags().Changed("exec") || cmd.Flags().Changed("sh-exec") {
		return fmt.Errorf("--remote, --exec, and --sh-exec require --listen")
	}
	if cmd.Flags().Changed("max-connections") {
		return fmt.Errorf("--max-connections requires --listen")
	}
	return nil
}

func newNetcatClientConfig(cmd *cobra.Command, logger *slog.Logger, rawTarget string) (*netcatClientConfig, error) {
	target, err := parseNetcatDialTarget(rawTarget)
	if err != nil {
		return nil, err
	}
	interactivePtr := getBoolPtr(cmd, "interactive")
	noInteractivePtr := getBoolPtr(cmd, "no-interactive")
	interactive := true
	switch {
	case interactivePtr != nil && *interactivePtr:
		interactive = true
	case noInteractivePtr != nil && *noInteractivePtr:
		interactive = false
	}
	rstreamClient, err := newNetcatRstreamClient(cmd, target.Kind == netcatEndpointRstream)
	if err != nil {
		return nil, err
	}
	return &netcatClientConfig{
		Target:      target.String(),
		Interactive: interactive,
		HalfClose:   target.Kind == netcatEndpointTCP,
		Dial:        newNetcatDialer(target, rstreamClient),
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Logger:      logger,
	}, nil
}

func newNetcatServerConfig(cmd *cobra.Command, logger *slog.Logger) (*netcatServerConfig, error) {
	rawListen, _ := cmd.Flags().GetString("listen")
	listenTarget, err := parseNetcatListenTarget(rawListen)
	if err != nil {
		return nil, err
	}
	usesRstream := listenTarget.Kind == netcatEndpointRstream
	maxConnections, _ := cmd.Flags().GetInt("max-connections")
	var upstreamDialer netcatDialer
	if cmd.Flags().Changed("remote") {
		rawRemote, _ := cmd.Flags().GetString("remote")
		remoteTarget, err := parseNetcatDialTarget(rawRemote)
		if err != nil {
			return nil, err
		}
		usesRstream = usesRstream || remoteTarget.Kind == netcatEndpointRstream
		rstreamClient, err := newNetcatRstreamClient(cmd, usesRstream)
		if err != nil {
			return nil, err
		}
		upstreamDialer = newNetcatDialer(remoteTarget, rstreamClient)
		return &netcatServerConfig{
			Listen:              newNetcatListenerFactory(listenTarget, rstreamClient),
			DownstreamHalfClose: listenTarget.Kind == netcatEndpointTCP,
			UpstreamHalfClose:   remoteTarget.Kind == netcatEndpointTCP,
			Upstream:            upstreamDialer,
			OpenTimeout:         defaultNetcatOpenTimeout,
			MaxConnections:      maxConnections,
			Stderr:              os.Stderr,
			Logger:              logger,
		}, nil
	}
	rstreamClient, err := newNetcatRstreamClient(cmd, usesRstream)
	if err != nil {
		return nil, err
	}
	var execCfg *netcatExecConfig
	if cmd.Flags().Changed("exec") {
		value, _ := cmd.Flags().GetString("exec")
		execCfg = &netcatExecConfig{Command: value}
	} else {
		value, _ := cmd.Flags().GetString("sh-exec")
		execCfg = &netcatExecConfig{Command: value, Shell: true}
	}
	return &netcatServerConfig{
		Listen:              newNetcatListenerFactory(listenTarget, rstreamClient),
		DownstreamHalfClose: listenTarget.Kind == netcatEndpointTCP,
		Exec:                execCfg,
		OpenTimeout:         defaultNetcatOpenTimeout,
		MaxConnections:      maxConnections,
		Stderr:              os.Stderr,
		Logger:              logger,
	}, nil
}

func newNetcatRstreamClient(cmd *cobra.Command, required bool) (*rstream.Client, error) {
	if !required {
		return nil, nil
	}
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve runtime: %w", err)
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to create rstream client: %w", err)
	}
	return client, nil
}

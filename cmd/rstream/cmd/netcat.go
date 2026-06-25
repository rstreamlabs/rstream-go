// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
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

type netcatPacketDialer func(context.Context) (net.PacketConn, net.Addr, error)

type netcatListenerResult struct {
	Listener  net.Listener
	Display   string
	Generated bool
}

type netcatListenerFactory func(context.Context) (*netcatListenerResult, error)

type netcatPacketListenerResult struct {
	Listener  rstream.PacketListener
	Display   string
	Generated bool
}

type netcatPacketListenerFactory func(context.Context) (*netcatPacketListenerResult, error)

type netcatClientConfig struct {
	Target      string
	Interactive bool
	HalfClose   bool
	Datagram    bool
	IdleTimeout time.Duration
	Dial        netcatDialer
	PacketDial  netcatPacketDialer
	Exec        *netcatExecConfig
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Logger      *slog.Logger
}

type netcatExecConfig struct {
	Command string
	Shell   bool
}

type netcatServerConfig struct {
	Listen              netcatListenerFactory
	PacketListen        netcatPacketListenerFactory
	Datagram            bool
	IdleTimeout         time.Duration
	DownstreamHalfClose bool
	UpstreamHalfClose   bool
	Upstream            netcatDialer
	UpstreamUDP         string
	UDPListen           string
	UDPPeer             string
	PacketDial          netcatPacketDialer
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
  rstream nc -L rstrm://ssh-server -R 127.0.0.1:22
  rstream nc -u rstrm://media
  rstream nc -u -L rstrm://media -c "media-producer" --idle-timeout 60s
  rstream nc -u -L rstrm://media -R udp://127.0.0.1:5004
  rstream nc -u -L udp://127.0.0.1:5004 -R rstrm://media --udp-peer 127.0.0.1:5006`,
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
			if cfg.Datagram {
				return netcatRunResult(runNetcatDatagramServer(cmd.Context(), cfg))
			}
			return netcatRunResult(runNetcatServer(cmd.Context(), cfg))
		}
		cfg, err := newNetcatClientConfig(cmd, logger, args[0])
		if err != nil {
			return err
		}
		if cfg.Datagram {
			return netcatRunResult(runNetcatDatagramClient(cmd.Context(), cfg))
		}
		return netcatRunResult(runNetcatClient(cmd.Context(), cfg))
	},
}

// netcatRunResult treats context cancellation as a clean shutdown: it is the
// result of an intentional interrupt (signal), not a session failure.
func netcatRunResult(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
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
	netcatCmd.Flags().BoolP("datagram", "u", false, "use datagram mode (rstream endpoints only)")
	netcatCmd.Flags().String("framing", string(netcatFramingRFC4571), "stdio framing for datagram mode (rfc4571)")
	netcatCmd.Flags().Duration("idle-timeout", 0, "close a datagram session after no datagram is received for this duration (0 disables)")
	netcatCmd.Flags().String("udp-peer", "", "eagerly open a tunnel session for this local udp peer (requires --listen udp://)")
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
	if err := validateNetcatDatagramFlags(cmd, serverMode); err != nil {
		return err
	}
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
	if cmd.Flags().Changed("remote") {
		return fmt.Errorf("--remote requires --listen")
	}
	execSet := cmd.Flags().Changed("exec") || cmd.Flags().Changed("sh-exec")
	if execSet && (cmd.Flags().Changed("interactive") || cmd.Flags().Changed("no-interactive")) {
		return fmt.Errorf("--interactive and --no-interactive are not valid with --exec or --sh-exec")
	}
	if cmd.Flags().Changed("max-connections") {
		return fmt.Errorf("--max-connections requires --listen")
	}
	return nil
}

func validateNetcatDatagramFlags(cmd *cobra.Command, serverMode bool) error {
	datagram, _ := cmd.Flags().GetBool("datagram")
	listenKind := netcatEndpointKind("")
	remoteKind := netcatEndpointKind("")
	if serverMode {
		rawListen, _ := cmd.Flags().GetString("listen")
		target, err := parseNetcatListenTarget(rawListen)
		if err != nil {
			return err
		}
		listenKind = target.Kind
	}
	if cmd.Flags().Changed("remote") {
		rawRemote, _ := cmd.Flags().GetString("remote")
		target, err := parseNetcatDialTarget(rawRemote)
		if err != nil {
			return err
		}
		remoteKind = target.Kind
	}
	udpInvolved := listenKind == netcatEndpointUDP || remoteKind == netcatEndpointUDP
	if !datagram {
		if udpInvolved {
			return fmt.Errorf("udp endpoints require --datagram")
		}
		if cmd.Flags().Changed("framing") {
			return fmt.Errorf("--framing requires --datagram")
		}
		if cmd.Flags().Changed("idle-timeout") {
			return fmt.Errorf("--idle-timeout requires --datagram")
		}
		if cmd.Flags().Changed("udp-peer") {
			return fmt.Errorf("--udp-peer requires --datagram")
		}
		return nil
	}
	framing, _ := cmd.Flags().GetString("framing")
	if netcatFraming(framing) != netcatFramingRFC4571 {
		return fmt.Errorf("unsupported framing %q (supported: %s)", framing, netcatFramingRFC4571)
	}
	if cmd.Flags().Changed("framing") && udpInvolved {
		return fmt.Errorf("--framing applies to stdio sessions and is not valid with udp endpoints")
	}
	idleTimeout, _ := cmd.Flags().GetDuration("idle-timeout")
	if idleTimeout < 0 {
		return fmt.Errorf("--idle-timeout must not be negative")
	}
	if !serverMode {
		if cmd.Flags().Changed("udp-peer") {
			return fmt.Errorf("--udp-peer requires --listen udp://host:port")
		}
		return nil
	}
	if listenKind == netcatEndpointUDP {
		if remoteKind != netcatEndpointRstream {
			return fmt.Errorf("udp listen endpoints require an rstream --remote (rstrm://<id-or-name>)")
		}
	} else if remoteKind != "" && remoteKind != netcatEndpointUDP {
		return fmt.Errorf("datagram server mode requires a udp --remote (udp://host:port) or --exec/--sh-exec")
	}
	if cmd.Flags().Changed("udp-peer") && listenKind != netcatEndpointUDP {
		return fmt.Errorf("--udp-peer requires --listen udp://host:port")
	}
	return nil
}

func newNetcatClientConfig(cmd *cobra.Command, logger *slog.Logger, rawTarget string) (*netcatClientConfig, error) {
	target, err := parseNetcatDialTarget(rawTarget)
	if err != nil {
		return nil, err
	}
	if target.Kind == netcatEndpointUDP {
		return nil, fmt.Errorf("udp endpoints are only valid in server mode (--listen or --remote)")
	}
	datagram, _ := cmd.Flags().GetBool("datagram")
	if datagram && target.Kind != netcatEndpointRstream {
		return nil, fmt.Errorf("datagram mode requires an rstream endpoint (rstrm://<id-or-name>)")
	}
	idleTimeout, _ := cmd.Flags().GetDuration("idle-timeout")
	interactivePtr := getBoolPtr(cmd, "interactive")
	noInteractivePtr := getBoolPtr(cmd, "no-interactive")
	interactive := true
	switch {
	case interactivePtr != nil && *interactivePtr:
		interactive = true
	case noInteractivePtr != nil && *noInteractivePtr:
		interactive = false
	}
	var execCfg *netcatExecConfig
	if cmd.Flags().Changed("exec") {
		value, _ := cmd.Flags().GetString("exec")
		execCfg = &netcatExecConfig{Command: value}
	} else if cmd.Flags().Changed("sh-exec") {
		value, _ := cmd.Flags().GetString("sh-exec")
		execCfg = &netcatExecConfig{Command: value, Shell: true}
	}
	rstreamClient, err := newNetcatRstreamClient(cmd, target.Kind == netcatEndpointRstream)
	if err != nil {
		return nil, err
	}
	cfg := &netcatClientConfig{
		Target:      target.String(),
		Interactive: interactive,
		HalfClose:   target.Kind == netcatEndpointTCP,
		Datagram:    datagram,
		IdleTimeout: idleTimeout,
		Exec:        execCfg,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Logger:      logger,
	}
	if datagram {
		cfg.PacketDial = newNetcatPacketDialer(target, rstreamClient)
	} else {
		cfg.Dial = newNetcatDialer(target, rstreamClient)
	}
	return cfg, nil
}

func newNetcatServerConfig(cmd *cobra.Command, logger *slog.Logger) (*netcatServerConfig, error) {
	rawListen, _ := cmd.Flags().GetString("listen")
	listenTarget, err := parseNetcatListenTarget(rawListen)
	if err != nil {
		return nil, err
	}
	datagram, _ := cmd.Flags().GetBool("datagram")
	if datagram && listenTarget.Kind == netcatEndpointTCP {
		return nil, fmt.Errorf("datagram mode requires an rstream or udp listen endpoint (rstrm://[name] or udp://host:port)")
	}
	if !datagram && listenTarget.Kind == netcatEndpointUDP {
		return nil, fmt.Errorf("udp listen endpoints require --datagram")
	}
	idleTimeout, _ := cmd.Flags().GetDuration("idle-timeout")
	udpPeer, _ := cmd.Flags().GetString("udp-peer")
	usesRstream := listenTarget.Kind == netcatEndpointRstream
	maxConnections, _ := cmd.Flags().GetInt("max-connections")
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
		cfg := &netcatServerConfig{
			Datagram:       datagram,
			IdleTimeout:    idleTimeout,
			OpenTimeout:    defaultNetcatOpenTimeout,
			MaxConnections: maxConnections,
			Stderr:         os.Stderr,
			Logger:         logger,
		}
		switch {
		case listenTarget.Kind == netcatEndpointUDP:
			if remoteTarget.Kind != netcatEndpointRstream {
				return nil, fmt.Errorf("udp listen endpoints require an rstream --remote (rstrm://<id-or-name>)")
			}
			cfg.UDPListen = listenTarget.Address
			cfg.UDPPeer = udpPeer
			cfg.PacketDial = newNetcatPacketDialer(remoteTarget, rstreamClient)
		case datagram:
			if remoteTarget.Kind != netcatEndpointUDP {
				return nil, fmt.Errorf("datagram server mode requires a udp --remote (udp://host:port) or --exec/--sh-exec")
			}
			cfg.PacketListen = newNetcatPacketListenerFactory(listenTarget, rstreamClient)
			cfg.UpstreamUDP = remoteTarget.Address
		default:
			if remoteTarget.Kind == netcatEndpointUDP {
				return nil, fmt.Errorf("udp endpoints require --datagram")
			}
			cfg.Listen = newNetcatListenerFactory(listenTarget, rstreamClient)
			cfg.DownstreamHalfClose = listenTarget.Kind == netcatEndpointTCP
			cfg.UpstreamHalfClose = remoteTarget.Kind == netcatEndpointTCP
			cfg.Upstream = newNetcatDialer(remoteTarget, rstreamClient)
		}
		return cfg, nil
	}
	if listenTarget.Kind == netcatEndpointUDP {
		return nil, fmt.Errorf("udp listen endpoints require an rstream --remote (rstrm://<id-or-name>)")
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
	cfg := &netcatServerConfig{
		Datagram:            datagram,
		IdleTimeout:         idleTimeout,
		DownstreamHalfClose: listenTarget.Kind == netcatEndpointTCP,
		Exec:                execCfg,
		OpenTimeout:         defaultNetcatOpenTimeout,
		MaxConnections:      maxConnections,
		Stderr:              os.Stderr,
		Logger:              logger,
	}
	if datagram {
		cfg.PacketListen = newNetcatPacketListenerFactory(listenTarget, rstreamClient)
	} else {
		cfg.Listen = newNetcatListenerFactory(listenTarget, rstreamClient)
	}
	return cfg, nil
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

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type commandExitError struct {
	code int
}

func (e *commandExitError) Error() string {
	return fmt.Sprintf("remote command exited with code %d", e.code)
}

func (e *commandExitError) ExitCode() int {
	if e.code <= 0 {
		return 1
	}
	if e.code > 255 {
		return 255
	}
	return e.code
}

var webttyCmd = &cobra.Command{
	GroupID: "utils",
	Use:     "webtty",
	Short:   "Web Remote Terminal (WebTTY)",
}

var webttyServerCmd = &cobra.Command{
	Use:          "server",
	Short:        "Web Remote Terminal (Server)",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := slog.With("cmd", "webtty", "subcmd", "server")
		retryPtr := getBoolPtr(cmd, "retry")
		noRetryPtr := getBoolPtr(cmd, "no-retry")
		var autoReconnect *bool
		switch {
		case retryPtr != nil && *retryPtr:
			autoReconnect = rstream.BoolPtr(true)
		case noRetryPtr != nil && *noRetryPtr:
			autoReconnect = rstream.BoolPtr(false)
		}
		intervalPtr := getInt64Ptr(cmd, "retry-interval")
		retryInterval := 5 * time.Second
		if intervalPtr != nil {
			retryInterval = time.Duration(*intervalPtr) * time.Millisecond
		}
		shutdownTimeoutMs, _ := cmd.Flags().GetInt64("shutdown-timeout")
		shutdownTimeout := time.Duration(shutdownTimeoutMs) * time.Millisecond
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			err := runWebTTYServerOnce(ctx, cmd, logger, shutdownTimeout)
			if err == nil {
				return nil
			}
			if autoReconnect != nil && !*autoReconnect {
				return err
			}
			logger.Error("webtty server failed", "error", err, "retry_in", retryInterval)
			select {
			case <-time.After(retryInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	},
}

func runWebTTYServerOnce(ctx context.Context, cmd *cobra.Command, logger *slog.Logger, shutdownTimeout time.Duration) error {
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{SessionCloseDeadline: &shutdownTimeout, Logger: logger})
	server := &http.Server{Handler: handler}
	var listener net.Listener
	if useWeb, _ := cmd.Flags().GetBool("web"); useWeb {
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return fmt.Errorf("failed to resolve runtime: %w", err)
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return fmt.Errorf("failed to create rstream client: %w", err)
		}
		ctrl, err := client.Connect(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to connect to rstream engine server: %w", err)
		}
		defer ctrl.Close()
		props := rstream.TunnelProperties{
			Publish:     rstream.BoolPtr(true),
			Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
			HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
			TokenAuth:   rstream.BoolPtr(true),
			Labels:      webtty.DefaultLabels(),
		}
		tunnel, err := ctrl.CreateTunnel(ctx, props)
		if err != nil {
			return fmt.Errorf("failed to create tunnel: %w", err)
		}
		defer tunnel.Close()
		nl, ok := tunnel.(interface{ net.Listener })
		if !ok {
			return fmt.Errorf("tunnel does not implement net.Listener")
		}
		listener = nl
	} else {
		addr, _ := cmd.Flags().GetString("listen")
		netListener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", addr, err)
		}
		listener = netListener
	}
	logger.Info("webtty server started", "address", listener.Addr().String())
	shutdownOnce := sync.Once{}
	shutdown := func(reason string) {
		shutdownOnce.Do(func() {
			logger.Info("stopping webtty server", "reason", reason)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			handler.BeginDrain()
			if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("http server shutdown failed", "error", err)
			}
			if err := handler.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logger.Warn("session shutdown failed", "error", err)
			}
		})
	}
	stopShutdownWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown("context canceled")
		case <-stopShutdownWatcher:
		}
	}()
	err := server.Serve(listener)
	close(stopShutdownWatcher)
	shutdown("serve loop ended")
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

var webttyClientCmd = &cobra.Command{
	Use:          "client [--] [cmd...]",
	Short:        "Web Remote Terminal (Client)",
	SilenceUsage: true,
	Args:         cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logger := slog.With("cmd", "webtty", "subcmd", "client")
		urlValue, _ := cmd.Flags().GetString("url")
		interactivePtr := getBoolPtr(cmd, "interactive")
		noInteractivePtr := getBoolPtr(cmd, "no-interactive")
		interactive := false
		switch {
		case interactivePtr != nil && *interactivePtr:
			interactive = true
		case noInteractivePtr != nil && *noInteractivePtr:
			interactive = false
		default:
			interactive = len(args) == 0
		}
		ttyPtr := getBoolPtr(cmd, "tty")
		noTTYPtr := getBoolPtr(cmd, "no-tty")
		allocateTTY := false
		switch {
		case ttyPtr != nil && *ttyPtr:
			allocateTTY = true
		case noTTYPtr != nil && *noTTYPtr:
			allocateTTY = false
		default:
			allocateTTY = len(args) == 0
		}
		envVars, _ := cmd.Flags().GetStringArray("env")
		workdir := getStringPtr(cmd, "workdir")
		username := getStringPtr(cmd, "user")
		exitCode, err := webtty.RunClient(ctx, &webtty.ClientConfig{
			URL:           urlValue,
			Interactive:   interactive,
			AllocateTTY:   allocateTTY,
			SendHeartbeat: true,
			EnvVars:       envVars,
			Workdir:       workdir,
			Username:      username,
			CmdArgs:       args,
			Logger:        logger,
		})
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return &commandExitError{code: exitCode}
		}
		return nil
	},
}

func init() {
	webttyCmd.Flags().SortFlags = false
	webttyCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(webttyCmd)
}

func init() {
	webttyServerCmd.Flags().SortFlags = false
	webttyServerCmd.PersistentFlags().SortFlags = false
	webttyServerCmd.Flags().String("listen", ":8080", "listen address (e.g. :8080 or 0.0.0.0:8080)")
	webttyServerCmd.Flags().BoolP("web", "w", false, "publish the server on the web via rstream tunnel")
	webttyServerCmd.MarkFlagsMutuallyExclusive("listen", "web")
	webttyServerCmd.Flags().Bool("retry", false, "enable automatic reconnection on disconnect")
	webttyServerCmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	webttyServerCmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	webttyServerCmd.Flags().Int64("retry-interval", 0, "retry interval in ms")
	webttyServerCmd.Flags().Int64("shutdown-timeout", 8000, "graceful shutdown timeout in ms")
	webttyCmd.AddCommand(webttyServerCmd)
}

func init() {
	webttyClientCmd.Flags().SortFlags = false
	webttyClientCmd.PersistentFlags().SortFlags = false
	webttyClientCmd.Flags().String("url", "ws://127.0.0.1:8080", "websocket endpoint URL (ws:// or wss://)")
	webttyClientCmd.Flags().BoolP("interactive", "i", false, "enable interactive mode")
	webttyClientCmd.Flags().BoolP("no-interactive", "I", false, "disable interactive mode")
	webttyClientCmd.MarkFlagsMutuallyExclusive("interactive", "no-interactive")
	webttyClientCmd.Flags().BoolP("tty", "t", false, "enable TTY allocation")
	webttyClientCmd.Flags().BoolP("no-tty", "T", false, "disable TTY allocation")
	webttyClientCmd.MarkFlagsMutuallyExclusive("tty", "no-tty")
	webttyClientCmd.Flags().StringArrayP("env", "e", nil, "pass environment variable (KEY or KEY=VALUE)")
	webttyClientCmd.Flags().StringP("workdir", "w", "", "set the working directory")
	webttyClientCmd.Flags().StringP("user", "u", "", "username or UID")
	webttyCmd.AddCommand(webttyClientCmd)
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

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
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			err := func() error {
				server := &http.Server{
					Handler: webtty.NewWebTTYHandler(nil),
				}
				var listener net.Listener
				if useWeb, _ := cmd.Flags().GetBool("web"); useWeb {
					client, err := newClientFromFlags(cmd)
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
						Labels:      map[string]string{"application-protocol": "rstream.rtty"}, // for compatibility purposes
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
				slog.With("cmd", "webtty").Info("server started", "address", listener.Addr().String())
				go func() {
					<-ctx.Done()
					server.Shutdown(context.Background())
				}()
				err := server.Serve(listener)
				if err == nil || errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}()
			if err == nil {
				return nil
			}
			if autoReconnect != nil && !*autoReconnect {
				return err
			}
			slog.With("cmd", "webtty").Error("server error", "error", err, "retry_in", retryInterval)
			select {
			case <-time.After(retryInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
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
	webttyServerCmd.Flags().String("listen", ":6002", "listen address (e.g. :6002 or 0.0.0.0:6002)")
	webttyServerCmd.Flags().BoolP("web", "w", false, "publish the server on the web via rstream tunnel")
	webttyServerCmd.MarkFlagsMutuallyExclusive("listen", "web")
	webttyServerCmd.Flags().Bool("retry", false, "enable automatic reconnection on disconnect")
	webttyServerCmd.Flags().Bool("no-retry", false, "disable automatic reconnection on disconnect")
	webttyServerCmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	webttyServerCmd.Flags().Int64("retry-interval", 0, "retry interval in ms")
	webttyCmd.AddCommand(webttyServerCmd)
}

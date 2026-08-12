// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/cmd/logging"
	"github.com/spf13/cobra"
)

var uiTransport string

var uiCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "ui",
	Short:        "Interactive terminal UI for rstream resources",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !logging.IsTerminal(os.Stdout) {
			return fmt.Errorf("ui requires a terminal")
		}
		if flagVerbose {
			return fmt.Errorf("ui is not compatible with verbose mode")
		}
		transport := strings.ToLower(strings.TrimSpace(uiTransport))
		if transport != "websocket" && transport != "sse" {
			return fmt.Errorf("invalid --transport %q (valid: websocket, sse)", uiTransport)
		}
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return err
		}
		clientCloser := ownRstreamClient(client)
		defer func() {
			if err := clientCloser.Close(); err != nil {
				slog.Warn("failed to close UI client", "error", err)
			}
		}()
		store := newUIStore(transport)
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		resolver := newUIRuntimeResolver(runtime.ConfigPath, uiRuntimeOptionsFromCommand(cmd))
		isDefault := runtime.Config.Defaults.Context != nil && runtime.Config.Defaults.Context.Name == runtime.Resolved.ContextName
		ui, err := newUIApp(ctx, cancel, client, store, runtime, resolver, uiConnectionInfo{
			ContextName: runtime.Resolved.ContextName,
			APIURL:      runtime.Resolved.APIURL,
			Engine:      runtime.Resolved.Engine,
			SessionOnly: !isDefault,
		})
		if err != nil {
			return err
		}
		if err := ui.Run(); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	},
}

func init() {
	uiCmd.Flags().SortFlags = false
	uiCmd.PersistentFlags().SortFlags = false
	uiCmd.Flags().StringVar(&uiTransport, "transport", "sse", "watch transport (sse, websocket)")
	rootCmd.AddCommand(uiCmd)
}

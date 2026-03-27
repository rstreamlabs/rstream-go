// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
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
		store := newUIStore(transport)
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		go store.run(ctx, client)
		ui, err := newUIApp(ctx, cancel, client, store, uiConnectionInfo{
			ContextName: runtime.Resolved.ContextName,
			APIURL:      runtime.Resolved.APIURL,
			Engine:      runtime.Resolved.Engine,
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

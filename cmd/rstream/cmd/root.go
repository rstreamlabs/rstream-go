// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/cmd/logging"
	"github.com/spf13/cobra"
)

var (
	flagVerbose   bool
	flagLogLevel  string
	flagLogFormat string
)

var rootCmd = newRootCmd()

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rstream",
		Short:   "CLI for rstream - serverless networking",
		Version: rstream.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initLogger(cmd)
		},
	}
	cmd.AddGroup(&cobra.Group{ID: "common", Title: "Common Commands:"})
	cmd.AddGroup(&cobra.Group{ID: "management", Title: "Management Commands:"})
	cmd.AddGroup(&cobra.Group{ID: "utils", Title: "Utility Commands:"})
	cmd.Flags().SortFlags = false
	cmd.PersistentFlags().SortFlags = false
	cmd.PersistentFlags().String("config", "", "path to rstream configuration file")
	cmd.PersistentFlags().String("context", "", "override Engine API context name")
	cmd.PersistentFlags().String("api-url", "", "override Control plane API URL")
	cmd.PersistentFlags().String("region", "", "select an authorized project region (default: auto)")
	cmd.PersistentFlags().String("tunnel-transport", "", "tunnel transport mode (auto, tls, quic)")
	cmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "enable verbose mode")
	cmd.PersistentFlags().StringVarP(&flagLogLevel, "log-level", "l", "info", "log level (debug, info, warn, error, none)")
	cmd.PersistentFlags().StringVarP(&flagLogFormat, "log-format", "f", "auto", "log format (auto, json, json-pretty, text, text-pretty)")
	cmd.PersistentFlags().Bool("version", false, "show version information and exit")
	return cmd
}

func initLogger(cmd *cobra.Command) error {
	verbose, _ := cmd.InheritedFlags().GetBool("verbose")
	level, _ := cmd.InheritedFlags().GetString("log-level")
	format, _ := cmd.InheritedFlags().GetString("log-format")
	var out io.Writer
	if verbose {
		out = os.Stderr
	} else {
		out = io.Discard
	}
	logger, err := logging.New(logging.Config{
		Level:  level,
		Format: format,
		Output: out,
	})
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	return nil
}

func ExecuteContext(ctx context.Context) {
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if ex, ok := err.(interface{ ExitCode() int }); ok {
			os.Exit(ex.ExitCode())
		}
		os.Exit(1)
	}
}

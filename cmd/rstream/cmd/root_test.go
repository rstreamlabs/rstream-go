// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestInitLoggerValidatesLoggingFlags(t *testing.T) {
	root := newRootCmd()
	command := &cobra.Command{Use: "child"}
	root.AddCommand(command)
	if err := initLogger(command); err != nil {
		t.Fatalf("initLogger(defaults) error = %v", err)
	}
	if err := root.PersistentFlags().Set("log-level", "none"); err != nil {
		t.Fatalf("set log-level flag: %v", err)
	}
	if err := initLogger(command); err != nil {
		t.Fatalf("initLogger(none) error = %v", err)
	}
	if err := root.PersistentFlags().Set("log-level", "invalid"); err != nil {
		t.Fatalf("set log-level flag: %v", err)
	}
	if err := initLogger(command); err == nil || !strings.Contains(err.Error(), "invalid log level") {
		t.Fatalf("initLogger(invalid level) = %v, want log level error", err)
	}
}

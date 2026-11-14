//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

func TestBenchmarkRejectsUnboundedDatagramTimingBeforeIO(t *testing.T) {
	arguments, commandLine := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = arguments, commandLine })
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "fixture-token")
	for _, option := range []string{"--pacing-ms=0", "--pacing-ms=1001", "--drain-seconds=0", "--drain-seconds=31"} {
		t.Run(option, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet("benchmark", flag.ContinueOnError)
			os.Args = []string{"benchmark", "--owner=fixture", "--peer=fixture", "--ca=missing-fixture-ca", option}
			if _, err := run(); err == nil || !strings.Contains(err.Error(), "invalid benchmark configuration") {
				t.Fatalf("timing option reached certificate/network I/O: %v", err)
			}
		})
	}
}

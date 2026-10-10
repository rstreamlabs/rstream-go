// See LICENSE file in the project root for license information.

//go:build !rstream_fips

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

func TestContextExecMTLSFlags(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	helper := filepath.Join(t.TempDir(), "missing-helper")
	mustSetFlag(t, command, "mtls-exec", helper)
	mustSetFlag(t, command, "mtls-exec-arg", "--device")
	mustSetFlag(t, command, "mtls-exec-arg", "test")
	mustSetFlag(t, command, "mtls-certificate-sha256", strings.Repeat("ab", 32))
	mustSetFlag(t, command, "mtls-exec-timeout", "2s")
	ctx := config.Context{Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{Kind: "inline", Value: "old-token"}}}}
	if err := setContextMTLSFromFlags(command, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Auth.Token != nil || ctx.Auth.MTLS == nil {
		t.Fatal("explicit exec selection did not replace old context authentication")
	}
	exec := ctx.Auth.MTLS.Storage.Exec
	if exec.Command != helper || exec.Timeout != "2s" || strings.Join(exec.Args, ",") != "--device,test" {
		t.Fatalf("flags not preserved: %#v", exec)
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "mtls-exec-timeout", "4s")
	if err := setContextMTLSFromFlags(command, &ctx); err != nil || ctx.Auth.MTLS.Storage.Exec.Timeout != "4s" || ctx.Auth.MTLS.Storage.Exec.Command != helper {
		t.Fatalf("update did not preserve the identity: %v", err)
	}
	mustSetFlag(t, command, "token", "new-token")
	if err := setContextMTLSFromFlags(command, &ctx); err == nil {
		t.Fatal("conflicting authentication flags accepted")
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "token", "new-token")
	if err := setContextTokenFromFlags(command, &ctx, "new-token", ""); err != nil || ctx.Auth.MTLS != nil {
		t.Fatalf("explicit token selection did not replace mTLS: %v", err)
	}
}

func TestContextCreatePersistsExecWithoutRunningIt(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	helper := filepath.Join(t.TempDir(), "not-installed")
	command := newContextCreateCommand()
	command.Flags().String("config", "", "")
	command.Flags().String("api-url", "", "")
	command.Flags().String("region", "", "")
	command.SetArgs([]string{"device", "--config", path, "--no-api-url", "--engine", "device.example:443", "--mtls-exec", helper, "--mtls-certificate-sha256", strings.Repeat("cd", 32)})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || len(cfg.Contexts) != 1 {
		t.Fatalf("read created context: %v", err)
	}
	storage := cfg.Contexts[0].Auth.MTLS.Storage
	if storage.Kind != "exec" || storage.Exec.Command != helper {
		t.Fatal("created context lost exec settings")
	}
	if _, err := os.Stat(helper); !os.IsNotExist(err) {
		t.Fatal("test requires an absent executable to prove creation is offline")
	}
}

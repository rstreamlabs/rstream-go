// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"strings"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

func addContextMTLSFlags(cmd *cobra.Command) {
	cmd.Flags().String("mtls-exec", "", "absolute path to an external mTLS signer")
	cmd.Flags().StringArray("mtls-exec-arg", nil, "external signer argument (repeatable; no secrets)")
	cmd.Flags().String("mtls-certificate-sha256", "", "SHA-256 fingerprint of the enrolled client certificate")
	cmd.Flags().Duration("mtls-exec-timeout", 0, "external signer operation timeout (default 5s, maximum 1m)")
	cmd.MarkFlagsMutuallyExclusive("mtls-exec", "token")
	cmd.MarkFlagsMutuallyExclusive("mtls-exec", "token-stdin")
	cmd.MarkFlagsMutuallyExclusive("mtls-exec", "token-file")
	cmd.MarkFlagsMutuallyExclusive("mtls-exec", "token-storage")
}

func setContextMTLSFromFlags(cmd *cobra.Command, ctx *config.Context) error {
	names := []string{"mtls-exec", "mtls-exec-arg", "mtls-certificate-sha256", "mtls-exec-timeout"}
	changed := false
	for _, name := range names {
		changed = changed || cmd.Flags().Changed(name)
	}
	if !changed {
		return nil
	}
	for _, name := range []string{"token", "token-stdin", "token-file", "token-storage"} {
		if cmd.Flags().Changed(name) {
			return errors.New("external mTLS settings cannot be combined with token settings")
		}
	}
	storage := config.MTLSStorage{Kind: config.MTLSStorageExec, Exec: &config.MTLSExecStorage{}}
	if ctx.Auth != nil && ctx.Auth.MTLS != nil && ctx.Auth.MTLS.Storage != nil && ctx.Auth.MTLS.Storage.Kind == config.MTLSStorageExec {
		storage = *ctx.Auth.MTLS.Storage
		if storage.Exec != nil {
			options := *storage.Exec
			storage.Exec = &options
		} else {
			storage.Exec = &config.MTLSExecStorage{}
		}
	}
	if cmd.Flags().Changed("mtls-exec") {
		storage.Exec.Command, _ = cmd.Flags().GetString("mtls-exec")
	}
	if cmd.Flags().Changed("mtls-exec-arg") {
		storage.Exec.Args, _ = cmd.Flags().GetStringArray("mtls-exec-arg")
	}
	if cmd.Flags().Changed("mtls-certificate-sha256") {
		value, _ := cmd.Flags().GetString("mtls-certificate-sha256")
		storage.CertificateSHA256 = strings.TrimSpace(value)
	}
	if cmd.Flags().Changed("mtls-exec-timeout") {
		value, _ := cmd.Flags().GetDuration("mtls-exec-timeout")
		storage.Exec.Timeout = value.String()
	}
	auth := &config.Auth{MTLS: &config.MTLS{Storage: &storage}}
	if _, _, err := config.MTLSConfigFromAuth(auth); err != nil {
		return err
	}
	ctx.Auth = auth
	return nil
}

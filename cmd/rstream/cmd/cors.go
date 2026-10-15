// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/rstreamlabs/rstream-go/httpcors"
	"github.com/spf13/cobra"
)

func addCORSFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("cors-allow-origin", nil, "allow an explicit HTTP(S) browser origin (repeatable)")
	cmd.Flags().StringArray("cors-allow-method", nil, "allow a CORS method (repeatable)")
	cmd.Flags().StringArray("cors-allow-header", nil, "allow a CORS request header (repeatable)")
	cmd.Flags().StringArray("cors-expose-header", nil, "expose a response header to browsers (repeatable)")
	cmd.Flags().Bool("cors-allow-credentials", false, "allow browser credentials for the configured origins")
	cmd.Flags().Int64("cors-max-age", 0, "CORS preflight cache lifetime in seconds (0–86400)")
}

func corsFromFlags(cmd *cobra.Command) *httpcors.Config {
	enabled := false
	for _, name := range []string{"cors-allow-origin", "cors-allow-method", "cors-allow-header", "cors-expose-header", "cors-allow-credentials", "cors-max-age"} {
		enabled = enabled || cmd.Flags().Changed(name)
	}
	if !enabled {
		return nil
	}
	config := &httpcors.Config{}
	config.AllowedOrigins, _ = cmd.Flags().GetStringArray("cors-allow-origin")
	config.AllowedMethods, _ = cmd.Flags().GetStringArray("cors-allow-method")
	config.AllowedHeaders, _ = cmd.Flags().GetStringArray("cors-allow-header")
	config.ExposedHeaders, _ = cmd.Flags().GetStringArray("cors-expose-header")
	config.AllowCredentials, _ = cmd.Flags().GetBool("cors-allow-credentials")
	config.MaxAge, _ = cmd.Flags().GetInt64("cors-max-age")
	return config
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/doctor"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	GroupID:      "utils",
	Use:          "doctor",
	Short:        "Diagnose rstream CLI, project, token, and engine readiness",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		report, diagnosticErr := runDoctor(cmd)
		output, _ := cmd.Flags().GetString("output")
		var err error
		switch output {
		case "json":
			err = writeDoctorJSON(cmd.OutOrStdout(), report)
		case "table":
			err = printDoctorTable(cmd.OutOrStdout(), report)
		default:
			return validateOutputMode(output, "table", "json")
		}
		if err != nil {
			return err
		}
		return diagnosticErr
	},
}

func init() {
	doctorCmd.Flags().SortFlags = false
	doctorCmd.PersistentFlags().SortFlags = false
	doctorCmd.Flags().StringP("output", "o", "table", "output mode (table, json)")
	doctorCmd.Flags().Bool("deep", false, "create and close a tunnel lifecycle probe")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command) (doctor.Report, error) {
	report := doctor.Report{Version: rstream.Version, Channel: rstream.Channel, GeneratedAt: time.Now().UTC()}
	path, cfg, err := loadConfig(cmd)
	if err != nil {
		report.Checks = []doctor.Check{{Name: "config", Status: doctor.StatusFail, Message: err.Error()}}
		report.Summary.Fail = 1
		return report, doctor.ErrChecksFailed
	}
	report.ConfigPath = path
	report.Checks = []doctor.Check{{Name: "config", Status: doctor.StatusPass, Message: "configuration loaded", Details: map[string]string{"path": path}}}
	report.Summary.Pass = 1
	resolved, err := resolveDoctorRuntime(cmd, cfg)
	if err != nil {
		report.Checks = append(report.Checks, doctor.Check{Name: "context", Status: doctor.StatusFail, Message: err.Error()})
		report.Summary.Fail = 1
		return report, doctor.ErrChecksFailed
	}
	deep, _ := cmd.Flags().GetBool("deep")
	result, err := doctor.Run(cmd.Context(), resolved, doctor.Options{Deep: deep})
	result.ConfigPath = path
	result.Checks = append(report.Checks, result.Checks...)
	result.Summary.Pass++
	return result, err
}

func resolveDoctorRuntime(cmd *cobra.Command, cfg config.Config) (config.Resolved, error) {
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagContext, _ := cmd.Flags().GetString("context")
	flagRegion, _ := cmd.Flags().GetString("region")
	flagTunnelTransport, _ := cmd.Flags().GetString("tunnel-transport")
	env := config.ReadEnv()
	resolved, err := config.Resolve(config.ResolveInput{
		Config:                 cfg,
		FlagAPIURL:             flagAPIURL,
		FlagContext:            flagContext,
		FlagRegion:             flagRegion,
		EnvAPIURL:              env.APIURL,
		EnvContext:             env.Context,
		EnvEngine:              env.Engine,
		EnvToken:               env.Token,
		EnvMTLSCert:            env.MTLSCert,
		EnvMTLSKey:             env.MTLSKey,
		EnvRegion:              env.Region,
		EnvControlPlaneHeaders: env.ControlPlaneHeaders,
		FlagTunnelTransport:    flagTunnelTransport,
		EnvTunnelTransport:     env.TunnelTransport,
		EnvUseQUIC:             env.UseQUIC,
		ResolveToken:           true,
	})
	if err != nil {
		return config.Resolved{}, err
	}
	return resolved, nil
}

func printDoctorTable(w io.Writer, report doctor.Report) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "Version\t%s\n", formatVersion(report.Version, report.Channel))
	if report.ConfigPath != "" {
		fmt.Fprintf(tw, "Config\t%s\n", report.ConfigPath)
	}
	if report.ContextName != "" {
		fmt.Fprintf(tw, "Context\t%s\n", report.ContextName)
	}
	if report.ProjectEndpoint != "" {
		fmt.Fprintf(tw, "Project\t%s\n", report.ProjectEndpoint)
	}
	if report.Engine != "" {
		fmt.Fprintf(tw, "Engine\t%s\n", report.Engine)
	}
	fmt.Fprintf(tw, "Summary\tpass=%d warn=%d fail=%d skip=%d\n\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Skip)
	fmt.Fprintln(tw, "CHECK\tSTATUS\tMESSAGE")
	for _, check := range report.Checks {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", check.Name, check.Status, check.Message)
	}
	return tw.Flush()
}

func writeDoctorJSON(w io.Writer, report doctor.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

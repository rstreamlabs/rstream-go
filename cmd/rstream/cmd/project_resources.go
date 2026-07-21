// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

var projectDomainCmd = &cobra.Command{
	Use:          "domain",
	Short:        "Manage custom domains",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var projectDomainListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List custom domains",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		response, err := client.ListProjectDomains(cmd.Context(), projectID, controlplane.ListProjectDomainsParams{})
		if err != nil {
			return mapControlPlaneError(err)
		}
		output, _ := cmd.Flags().GetString("output")
		if output == "table" {
			return writeProjectDomainsTable(cmd.OutOrStdout(), response.Domains)
		}
		if err := validateOutputMode(output, "table", "json", "yaml"); err != nil {
			return err
		}
		return writeStructuredOutput(output, response)
	},
}

var projectDomainAddCmd = &cobra.Command{
	Use:          "add <hostname>",
	Short:        "Add a custom domain",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname := strings.TrimSpace(strings.ToLower(args[0]))
		if hostname == "" {
			return errors.New("hostname is required")
		}
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		domain, err := client.CreateProjectDomain(cmd.Context(), projectID, controlplane.CreateProjectDomainRequest{Hostname: hostname})
		if err != nil {
			return mapControlPlaneError(err)
		}
		return writeProjectResourceResult(cmd, domain)
	},
}

var projectDomainRemoveCmd = &cobra.Command{
	Use:          "remove <hostname>",
	Aliases:      []string{"rm", "delete"},
	Short:        "Remove a custom domain",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		domainID, err := resolveProjectDomainID(cmd.Context(), client, projectID, args[0])
		if err != nil {
			return mapControlPlaneError(err)
		}
		domain, err := client.DeleteProjectDomain(cmd.Context(), projectID, domainID)
		if err != nil {
			return mapControlPlaneError(err)
		}
		return writeProjectResourceResult(cmd, domain)
	},
}

var projectDomainVerifyCmd = &cobra.Command{
	Use:          "verify <hostname>",
	Short:        "Verify a custom domain",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		domainID, err := resolveProjectDomainID(cmd.Context(), client, projectID, args[0])
		if err != nil {
			return mapControlPlaneError(err)
		}
		domain, err := client.VerifyProjectDomain(cmd.Context(), projectID, domainID)
		if err != nil {
			return mapControlPlaneError(err)
		}
		return writeProjectResourceResult(cmd, domain)
	},
}

var projectTCPAddressCmd = &cobra.Command{
	Use:          "tcp-address",
	Short:        "Manage reserved TCP addresses",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var projectTCPAddressListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List reserved TCP addresses",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		response, err := client.ListProjectTCPAddresses(cmd.Context(), projectID)
		if err != nil {
			return mapControlPlaneError(err)
		}
		output, _ := cmd.Flags().GetString("output")
		if output == "table" {
			return writeProjectTCPAddressesTable(cmd.OutOrStdout(), response.Addresses)
		}
		if err := validateOutputMode(output, "table", "json", "yaml"); err != nil {
			return err
		}
		return writeStructuredOutput(output, response)
	},
}

var projectTCPAddressReserveCmd = &cobra.Command{
	Use:          "reserve",
	Short:        "Reserve a TCP address",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		address, err := client.ReserveProjectTCPAddress(cmd.Context(), projectID)
		if err != nil {
			return mapControlPlaneError(err)
		}
		return writeProjectResourceResult(cmd, address)
	},
}

var projectTCPAddressReleaseCmd = &cobra.Command{
	Use:          "release <port>",
	Aliases:      []string{"rm", "delete"},
	Short:        "Release a reserved TCP address",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value, err := strconv.ParseUint(args[0], 10, 16)
		if err != nil || value == 0 {
			return fmt.Errorf("invalid TCP port %q", args[0])
		}
		client, projectID, err := projectResourceClient(cmd)
		if err != nil {
			return err
		}
		addressID, err := resolveProjectTCPAddressID(cmd.Context(), client, projectID, uint32(value))
		if err != nil {
			return mapControlPlaneError(err)
		}
		response, err := client.ReleaseProjectTCPAddress(cmd.Context(), projectID, addressID)
		if err != nil {
			return mapControlPlaneError(err)
		}
		return writeProjectResourceResult(cmd, response)
	},
}

func init() {
	projectDomainCmd.AddCommand(projectDomainListCmd, projectDomainAddCmd, projectDomainRemoveCmd, projectDomainVerifyCmd)
	projectTCPAddressCmd.AddCommand(projectTCPAddressListCmd, projectTCPAddressReserveCmd, projectTCPAddressReleaseCmd)
	for _, command := range []*cobra.Command{projectDomainListCmd, projectDomainAddCmd, projectDomainRemoveCmd, projectDomainVerifyCmd, projectTCPAddressListCmd, projectTCPAddressReserveCmd, projectTCPAddressReleaseCmd} {
		command.Flags().SortFlags = false
		command.Flags().String("project-id", "", "project ID (defaults to the current context)")
		command.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	}
	projectCmd.AddCommand(projectDomainCmd, projectTCPAddressCmd)
}

func projectResourceClient(cmd *cobra.Command) (*controlplane.Client, string, error) {
	runtime, err := resolveRuntime(cmd, false, true)
	if err != nil {
		return nil, "", err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	if err := client.RequireToken(); err != nil {
		return nil, "", err
	}
	projectID, _ := cmd.Flags().GetString("project-id")
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		return client, projectID, nil
	}
	if runtime.Resolved.Context == nil || strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint) == "" {
		return nil, "", errors.New("project is required (select a project context or pass --project-id)")
	}
	project, err := client.ResolveProjectByEndpoint(cmd.Context(), runtime.Resolved.Context.ProjectEndpoint)
	if err != nil {
		return nil, "", mapControlPlaneError(err)
	}
	return client, project.ID, nil
}

func resolveProjectDomainID(ctx context.Context, client *controlplane.Client, projectID string, hostname string) (string, error) {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	response, err := client.ListProjectDomains(ctx, projectID, controlplane.ListProjectDomainsParams{Query: hostname})
	if err != nil {
		return "", err
	}
	if id, ok := findProjectDomainID(response.Domains, hostname); ok {
		return id, nil
	}
	return "", fmt.Errorf("custom domain %q not found", hostname)
}

func resolveProjectTCPAddressID(ctx context.Context, client *controlplane.Client, projectID string, port uint32) (string, error) {
	response, err := client.ListProjectTCPAddresses(ctx, projectID)
	if err != nil {
		return "", err
	}
	if id, ok := findProjectTCPAddressID(response.Addresses, port); ok {
		return id, nil
	}
	return "", fmt.Errorf("reserved TCP port %d not found", port)
}

func findProjectDomainID(domains []controlplane.ProjectDomain, hostname string) (string, bool) {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	for _, domain := range domains {
		if strings.ToLower(mapString(domain, "hostname")) == hostname {
			id := mapString(domain, "id")
			return id, id != ""
		}
	}
	return "", false
}

func findProjectTCPAddressID(addresses []controlplane.ProjectTCPAddress, port uint32) (string, bool) {
	for _, address := range addresses {
		if address.Port == port {
			return address.ID, address.ID != ""
		}
	}
	return "", false
}

func writeProjectResourceResult(cmd *cobra.Command, value any) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		switch resource := value.(type) {
		case controlplane.ProjectTCPAddress:
			_, _ = fmt.Fprintln(w, "ADDRESS\tID")
			_, _ = fmt.Fprintf(w, "%s\t%s\n", terminalSafeDefault(resource.Address), terminalSafeDefault(resource.ID))
		case controlplane.ReleaseProjectTCPAddressResponse:
			_, _ = fmt.Fprintln(w, "ID\tREUSABLE AT")
			_, _ = fmt.Fprintf(w, "%s\t%s\n", terminalSafeDefault(resource.ID), terminalSafeDefault(resource.ReusableAt))
		case controlplane.ProjectDomain:
			_, _ = fmt.Fprintln(w, "HOSTNAME\tSTATUS\tID")
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", terminalSafeDefault(mapString(resource, "hostname")), terminalSafeDefault(mapString(resource, "status")), terminalSafeDefault(mapString(resource, "id")))
		default:
			return fmt.Errorf("unsupported table output type %T", value)
		}
		return w.Flush()
	}
	if err := validateOutputMode(output, "table", "json", "yaml"); err != nil {
		return err
	}
	return writeStructuredOutput(output, value)
}

func writeProjectTCPAddressesTable(out io.Writer, addresses []controlplane.ProjectTCPAddress) error {
	rows := append([]controlplane.ProjectTCPAddress(nil), addresses...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Port < rows[j].Port })
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ADDRESS\tPORT\tID")
	for _, address := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\n", terminalSafeDefault(address.Address), address.Port, terminalSafeDefault(address.ID))
	}
	return w.Flush()
}

func writeProjectDomainsTable(out io.Writer, domains []controlplane.ProjectDomain) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "HOSTNAME\tSTATUS\tID")
	for _, domain := range domains {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", terminalSafeDefault(mapString(domain, "hostname")), terminalSafeDefault(mapString(domain, "status")), terminalSafeDefault(mapString(domain, "id")))
	}
	return w.Flush()
}

func mapString(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

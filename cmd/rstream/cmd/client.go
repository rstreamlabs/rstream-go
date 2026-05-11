// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

var (
	clientListFilter string
	clientListOutput string
	clientListQuiet  bool
)

var clientCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "client",
	Short:        "Manage clients",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var clientListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List clients",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return err
		}
		params, err := buildClientListParams(clientListFilter)
		if err != nil {
			return fmt.Errorf("invalid --filter: %w", err)
		}
		list, err := client.ListClients(cmd.Context(), params)
		if err != nil {
			return fmt.Errorf("failed to list clients: %w", err)
		}
		if clientListQuiet {
			for _, cl := range *list {
				if cl.ID != "" {
					fmt.Fprintln(os.Stdout, terminalSafeDefault(cl.ID))
				}
			}
			return nil
		}
		switch clientListOutput {
		case "json":
			return printClientsJSON(os.Stdout, list)
		case "table":
			return printClientsTable(os.Stdout, list)
		default:
			return fmt.Errorf("invalid --output %q (valid: table, json)", clientListOutput)
		}
	},
}

func init() {
	clientCmd.Flags().SortFlags = false
	clientCmd.PersistentFlags().SortFlags = false
	clientCmd.AddCommand(clientListCmd)
	clientListCmd.Flags().SortFlags = false
	clientListCmd.Flags().StringVar(&clientListFilter, "filter", "", "Filter output, e.g. \"status=online,agent=rstream,labels.env=prod\"")
	clientListCmd.Flags().StringVarP(&clientListOutput, "output", "o", "table", "output mode (table, json)")
	clientListCmd.Flags().BoolVarP(&clientListQuiet, "quiet", "q", false, "Only display client IDs")
	clientListCmd.MarkFlagsMutuallyExclusive("output", "quiet")
	rootCmd.AddCommand(clientCmd)
}

func printClientsJSON(w io.Writer, list *rstream.ListClientsResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func printClientsTable(w io.Writer, list *rstream.ListClientsResponse) error {
	sort.SliceStable(*list, func(i, j int) bool {
		return (*list)[i].ID < (*list)[j].ID
	})
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tUSER\tAGENT\tCHANNEL\tVERSION\tOS\tARCH")
	for _, cl := range *list {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			terminalSafeDefault(cl.ID),
			terminalSafeDefault(cl.Status),
			terminalSafeDefault(str(cl.UserID)),
			terminalSafeDefault(str(cl.Agent)),
			terminalSafeDefault(str(cl.Channel)),
			terminalSafeDefault(str(cl.Version)),
			terminalSafeDefault(str(cl.OS)),
			terminalSafeDefault(str(cl.Arch)),
		)
	}
	return tw.Flush()
}

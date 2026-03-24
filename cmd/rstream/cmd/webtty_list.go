// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

var (
	webttyListOutput string
	webttyListQuiet  bool
)

var webttyListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List available WebTTY servers",
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
		list, err := listWebTTYServers(cmd.Context(), client)
		if err != nil {
			return fmt.Errorf("failed to list webtty servers: %w", err)
		}
		if webttyListQuiet {
			for _, server := range list {
				fmt.Fprintln(os.Stdout, server.RstreamURL)
			}
			return nil
		}
		switch strings.ToLower(webttyListOutput) {
		case "json":
			return printWebTTYServersJSON(os.Stdout, list)
		case "table":
			return printWebTTYServersTable(os.Stdout, list)
		default:
			return fmt.Errorf("invalid --output %q (valid: table, json)", webttyListOutput)
		}
	},
}

func init() {
	webttyListCmd.Flags().SortFlags = false
	webttyListCmd.PersistentFlags().SortFlags = false
	webttyListCmd.Flags().StringVarP(&webttyListOutput, "output", "o", "table", "output mode (table, json)")
	webttyListCmd.Flags().BoolVarP(&webttyListQuiet, "quiet", "q", false, "Only display rstream URLs")
	webttyListCmd.MarkFlagsMutuallyExclusive("output", "quiet")
	webttyCmd.AddCommand(webttyListCmd)
}

func listWebTTYServers(ctx context.Context, client *rstream.Client) ([]webtty.ServerInfo, error) {
	status := "online"
	applicationProtocol := webtty.WebTTYApplicationProtocol
	params := &rstream.ListTunnelsParams{
		Filters: &rstream.ListTunnelsFilters{
			Status: &status,
			Labels: map[string]*string{
				webtty.WebTTYApplicationProtocolKey: &applicationProtocol,
			},
		},
	}
	list, err := client.ListTunnels(ctx, params)
	if err != nil {
		return nil, err
	}
	if list == nil {
		return []webtty.ServerInfo{}, nil
	}
	servers := webtty.ParseServers(*list)
	sortWebTTYServers(servers)
	return servers, nil
}

func sortWebTTYServers(list []webtty.ServerInfo) {
	sort.SliceStable(list, func(i, j int) bool {
		left := list[i]
		right := list[j]
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		if left.Publish != right.Publish {
			return !left.Publish && right.Publish
		}
		return left.TunnelID < right.TunnelID
	})
}

func printWebTTYServersJSON(w io.Writer, list []webtty.ServerInfo) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func printWebTTYServersTable(w io.Writer, list []webtty.ServerInfo) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RSTREAM_URL\tHOSTNAME\tSYSTEM\tARCH\tDOMAIN/HOST")
	for _, server := range list {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\n",
			server.RstreamURL,
			webTTYValue(server.Hostname),
			webTTYSystem(server),
			webTTYValue(server.Arch),
			webTTYValue(server.Host),
		)
	}
	return tw.Flush()
}

func webTTYSystem(server webtty.ServerInfo) string {
	switch {
	case server.OSPrettyName != nil && *server.OSPrettyName != "":
		return *server.OSPrettyName
	case server.OSFamily != nil && *server.OSFamily != "":
		return *server.OSFamily
	default:
		return "-"
	}
}

func webTTYValue(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}

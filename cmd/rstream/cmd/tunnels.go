// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

var (
	tunnelFilter       string
	tunnelOutputFormat string // table | json
	tunnelQuiet        bool
)

var tunnelsCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "tunnels",
	Short:        "List tunnels",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newClientFromFlags(cmd)
		if err != nil {
			return err
		}
		params, err := buildListParams(tunnelFilter)
		if err != nil {
			return fmt.Errorf("invalid --filter: %w", err)
		}
		ctx := context.Background()
		list, err := client.ListTunnels(ctx, params)
		if err != nil {
			return fmt.Errorf("failed to list tunnels: %w", err)
		}
		if tunnelQuiet {
			for _, t := range *list {
				if t.ID != nil && *t.ID != "" {
					fmt.Fprintln(os.Stdout, *t.ID)
				}
			}
			return nil
		}
		switch strings.ToLower(tunnelOutputFormat) {
		case "json":
			return printTunnelsJSON(os.Stdout, list)
		case "table":
			return printTunnelsTable(os.Stdout, list)
		default:
			return fmt.Errorf("invalid --output %q (valid: table, json)", tunnelOutputFormat)
		}
	},
}

func init() {
	tunnelsCmd.Flags().SortFlags = false
	tunnelsCmd.PersistentFlags().SortFlags = false
	tunnelsCmd.Flags().StringVar(&tunnelFilter, "filter", "", "Filter output, e.g. \"status=online,protocol=http,labels.env=prod\"")
	tunnelsCmd.Flags().StringVarP(&tunnelOutputFormat, "output", "o", "table", "output mode (table, json)")
	tunnelsCmd.Flags().BoolVarP(&tunnelQuiet, "quiet", "q", false, "Only display tunnel IDs")
	tunnelsCmd.MarkFlagsMutuallyExclusive("output", "quiet")
	rootCmd.AddCommand(tunnelsCmd)
}

func buildListParams(filter string) (*rstream.ListTunnelsParams, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, nil
	}
	parts := splitCSV(filter)
	if len(parts) == 0 {
		return nil, nil
	}
	fp := &rstream.ListTunnelsFilters{
		Labels: make(map[string]*string),
	}
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("expected key=value, got %q", p)
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch {
		case key == "status":
			fp.Status = &val
		case key == "client_id":
			fp.ClientID = &val
		case key == "protocol":
			fp.Protocol = &val
		case key == "publish":
			b, err := parseBool(val)
			if err != nil {
				return nil, fmt.Errorf("publish: %w", err)
			}
			fp.Publish = &b
		case strings.HasPrefix(key, "labels.") || strings.HasPrefix(key, "label."):
			labelKey := strings.TrimPrefix(strings.TrimPrefix(key, "labels."), "label.")
			if labelKey == "" {
				return nil, fmt.Errorf("invalid label filter key %q", key)
			}
			if val == "" || val == "*" {
				fp.Labels[labelKey] = nil
			} else {
				v := val
				fp.Labels[labelKey] = &v
			}
		default:
			return nil, fmt.Errorf("unknown filter key %q", key)
		}
	}
	return &rstream.ListTunnelsParams{
		Filters: fp,
	}, nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "y":
		return true, nil
	case "0", "f", "false", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", s)
	}
}

func splitCSV(s string) []string {
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func printTunnelsJSON(w *os.File, list *rstream.ListTunnelsResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func printTunnelsTable(w *os.File, list *rstream.ListTunnelsResponse) error {
	sort.SliceStable(*list, func(i, j int) bool {
		ti := (*list)[i]
		tj := (*list)[j]
		di := time.Time{}
		dj := time.Time{}
		if ti.CreationDate != nil {
			di = *ti.CreationDate
		}
		if tj.CreationDate != nil {
			dj = *tj.CreationDate
		}
		if !di.Equal(dj) {
			return di.After(dj)
		}
		si := ""
		sj := ""
		if ti.ID != nil {
			si = *ti.ID
		}
		if tj.ID != nil {
			sj = *tj.ID
		}
		return si < sj
	})
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tPROTOCOL\tPUBLISH\tDOMAIN/HOST\tHTTP_VERSION\tHTTP_TLS")
	for _, t := range *list {
		id := str(t.ID)
		name := str(t.Name)
		tt := "-"
		if t.Type != nil {
			tt = string(*t.Type)
		}
		proto := "-"
		if t.Protocol != nil {
			proto = string(*t.Protocol)
		}
		pub := "-"
		if t.Publish != nil {
			pub = strconv.FormatBool(*t.Publish)
		}
		domainOrHost := "-"
		if t.Domain != nil && *t.Domain != "" {
			domainOrHost = *t.Domain
		} else if t.Host != nil && *t.Host != "" {
			domainOrHost = *t.Host
		}
		httpv := "-"
		if t.HTTPVersion != nil {
			httpv = string(*t.HTTPVersion)
		}
		httptls := "-"
		if t.HTTPUseTLS != nil {
			httptls = strconv.FormatBool(*t.HTTPUseTLS)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			id, name, tt, proto, pub, domainOrHost, httpv, httptls)
	}
	return tw.Flush()
}

func str(p *string) string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return "-"
	}
	return *p
}

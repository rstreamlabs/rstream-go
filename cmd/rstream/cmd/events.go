// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

var (
	eventsFilter             []string
	eventsTransport          string // sse | websocket
	eventsClientFilter       string
	eventsTunnelFilter       string
	eventsForwardTo          string
	eventsForwardInsecureTLS bool
)

const maxEventsForwardErrorBody = 4096

var eventsCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "events",
	Short:        "Watches and forwards webhook events",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return err
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		if output != "json" && output != "ndjson" {
			return validateOutputMode(output, "json", "ndjson")
		}
		typeFilter := map[string]struct{}{}
		for _, t := range eventsFilter {
			for _, s := range strings.Split(t, ",") {
				if s = strings.TrimSpace(s); s != "" {
					typeFilter[s] = struct{}{}
				}
			}
		}
		clientParams, err := buildClientListParams(eventsClientFilter)
		if err != nil {
			return fmt.Errorf("invalid --client-filter: %w", err)
		}
		tunnelParams, err := buildTunnelListParams(eventsTunnelFilter)
		if err != nil {
			return fmt.Errorf("invalid --tunnel-filter: %w", err)
		}
		var watchParams *rstream.WatchParams
		if clientParams != nil || tunnelParams != nil {
			watchParams = &rstream.WatchParams{}
			if clientParams != nil {
				watchParams.Clients = clientParams.Filters
			}
			if tunnelParams != nil {
				watchParams.Tunnels = tunnelParams.Filters
			}
		}
		var forward func(ctx context.Context, body []byte) error
		if strings.TrimSpace(eventsForwardTo) != "" {
			dst, err := url.Parse(eventsForwardTo)
			if err != nil {
				return fmt.Errorf("invalid --forward-to: %w", err)
			}
			cl := &http.Client{
				Timeout:       10 * time.Second,
				CheckRedirect: sameHostRedirectPolicy,
			}
			if dst.Scheme == "https" && eventsForwardInsecureTLS {
				cl.Transport = &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				}
			}
			forward = func(ctx context.Context, body []byte) error {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, dst.String(), strings.NewReader(string(body)))
				if err != nil {
					return err
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := cl.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					b, _ := io.ReadAll(io.LimitReader(resp.Body, maxEventsForwardErrorBody))
					return fmt.Errorf("forward failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
				}
				return nil
			}
		}
		handler := func(event rstream.Event) error {
			if len(typeFilter) > 0 {
				if _, ok := typeFilter[event.Type]; !ok {
					return nil
				}
			}
			b, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if forward != nil {
				return forward(ctx, b)
			}
			_, err = os.Stdout.Write(append(b, '\n'))
			return err
		}
		err = client.Watch(ctx, strings.ToLower(strings.TrimSpace(eventsTransport)), watchParams, handler)
		if err != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

func init() {
	eventsCmd.Flags().SortFlags = false
	eventsCmd.PersistentFlags().SortFlags = false
	eventsCmd.Flags().StringSliceVar(&eventsFilter, "events", []string{}, "Comma-separated list of events to listen for (e.g. tunnel.created,tunnel.updated)")
	eventsCmd.Flags().StringVar(&eventsTransport, "transport", "websocket", "Transport to use (sse, websocket)")
	eventsCmd.Flags().StringVar(&eventsClientFilter, "client-filter", "", "Server-side client filters, e.g. \"status=online,agent=rstream\"")
	eventsCmd.Flags().StringVar(&eventsTunnelFilter, "tunnel-filter", "", "Server-side tunnel filters, e.g. \"name=ssh-prod-01,labels.env=prod\"")
	eventsCmd.Flags().StringVar(&eventsForwardTo, "forward-to", "", "URL to forward the webhook events to")
	eventsCmd.Flags().BoolVar(&eventsForwardInsecureTLS, "forward-insecure-tls", false, "Skip TLS verification when forwarding events")
	eventsCmd.Flags().StringP("output", "o", "json", "output mode (json, ndjson)")
	rootCmd.AddCommand(eventsCmd)
}

func sameHostRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return http.ErrUseLastResponse
	}
	return nil
}

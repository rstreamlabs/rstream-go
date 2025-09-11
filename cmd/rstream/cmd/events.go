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
	eventsForwardTo          string
	eventsForwardInsecureTLS bool
)

var eventsCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "events",
	Short:        "Watches and forwards webhook events",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, err := newClientFromFlags(cmd)
		if err != nil {
			return err
		}
		typeFilter := map[string]struct{}{}
		for _, t := range eventsFilter {
			for _, s := range strings.Split(t, ",") {
				if s = strings.TrimSpace(s); s != "" {
					typeFilter[s] = struct{}{}
				}
			}
		}
		var forward func(ctx context.Context, body []byte) error
		if strings.TrimSpace(eventsForwardTo) != "" {
			dst, err := url.Parse(eventsForwardTo)
			if err != nil {
				return fmt.Errorf("invalid --forward-to: %w", err)
			}
			forward = func(ctx context.Context, body []byte) error {
				cl := &http.Client{Timeout: 10 * time.Second}
				if dst.Scheme == "https" && eventsForwardInsecureTLS {
					cl.Transport = &http.Transport{
						TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
					}
				}
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
					b, _ := io.ReadAll(resp.Body)
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
		err = client.Watch(ctx, strings.ToLower(strings.TrimSpace(eventsTransport)), handler)
		if err != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	},
}

func init() {
	eventsCmd.Flags().SortFlags = false
	eventsCmd.PersistentFlags().SortFlags = false
	eventsCmd.Flags().StringSlice("events", []string{}, "Comma-separated list of events to listen for (e.g. tunnel.created,tunnel.updated)")
	eventsCmd.Flags().StringVar(&eventsTransport, "transport", "websocket", "Transport to use (sse, websocket)")
	eventsCmd.Flags().StringVar(&eventsForwardTo, "forward-to", "", "URL to forward the webhook events to")
	eventsCmd.Flags().BoolVar(&eventsForwardInsecureTLS, "forward-insecure-tls", false, "Skip TLS verification when forwarding events")
	rootCmd.AddCommand(eventsCmd)
}

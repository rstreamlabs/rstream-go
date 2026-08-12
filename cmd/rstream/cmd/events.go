// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

var (
	eventsFilter                []string
	eventsTransport             string // sse | websocket
	eventsClientFilter          string
	eventsTunnelFilter          string
	eventsForwardTo             string
	eventsForwardInsecureTLS    bool
	eventsWebhookMode           bool
	eventsWebhookSecret         string
	eventsWebhookID             string
	eventsIncludeWebhookHeaders bool
)

const maxEventsForwardErrorBody = 4096

type eventsForwarder func(ctx context.Context, body []byte, headers http.Header) error

type eventsHandlerOptions struct {
	TypeFilter     map[string]struct{}
	WebhookMode    bool
	WebhookSecret  string
	WebhookID      string
	IncludeHeaders bool
	Forward        eventsForwarder
	Stdout         io.Writer
	Now            func() time.Time
	NewID          func(prefix string) (string, error)
}

type eventsWebhookOutput struct {
	Headers map[string]string    `json:"headers"`
	Body    rstream.WebhookEvent `json:"body"`
}

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
		defer closeRstreamClientLogged(client, slog.Default())
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
		if eventsWebhookMode {
			for eventType := range typeFilter {
				if !rstream.IsWebhookDeliverableEventType(eventType) {
					return fmt.Errorf("%q is not deliverable as a webhook event", eventType)
				}
			}
		}
		webhookSecret := strings.TrimSpace(eventsWebhookSecret)
		if eventsWebhookMode && webhookSecret == "" {
			webhookSecret, err = rstream.GenerateWebhookSigningSecret()
			if err != nil {
				return fmt.Errorf("generate webhook signing secret: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Webhook signing secret: %s\n", webhookSecret)
		}
		webhookID := strings.TrimSpace(eventsWebhookID)
		if eventsWebhookMode && webhookID == "" {
			webhookID, err = newEventsCLIID("cli_we")
			if err != nil {
				return fmt.Errorf("generate webhook id: %w", err)
			}
		}
		var forward eventsForwarder
		if strings.TrimSpace(eventsForwardTo) != "" {
			forward, err = newEventsForwarder(eventsForwardTo, eventsForwardInsecureTLS)
			if err != nil {
				return err
			}
			if !eventsWebhookMode {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: --forward-to sends raw watch events. Use --webhook to test webhook-compatible payloads and signatures.")
			}
		}
		handler, err := newEventsHandler(ctx, eventsHandlerOptions{
			TypeFilter:     typeFilter,
			WebhookMode:    eventsWebhookMode,
			WebhookSecret:  webhookSecret,
			WebhookID:      webhookID,
			IncludeHeaders: eventsIncludeWebhookHeaders,
			Forward:        forward,
			Stdout:         os.Stdout,
			Now:            time.Now,
			NewID:          newEventsCLIID,
		})
		if err != nil {
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
	eventsCmd.Flags().StringVar(&eventsForwardTo, "forward-to", "", "URL to forward events to")
	eventsCmd.Flags().BoolVar(&eventsForwardInsecureTLS, "forward-insecure-tls", false, "Skip TLS verification when forwarding events")
	eventsCmd.Flags().BoolVar(&eventsWebhookMode, "webhook", false, "Emit and forward webhook-compatible payloads with signed headers")
	eventsCmd.Flags().StringVar(&eventsWebhookSecret, "webhook-secret", "", "Webhook signing secret to use in --webhook mode")
	eventsCmd.Flags().StringVar(&eventsWebhookID, "webhook-id", "", "Webhook ID to use in --webhook mode headers")
	eventsCmd.Flags().BoolVar(&eventsIncludeWebhookHeaders, "include-webhook-headers", false, "Include webhook headers in stdout when using --webhook without --forward-to")
	eventsCmd.Flags().StringP("output", "o", "json", "output mode (json, ndjson)")
	rootCmd.AddCommand(eventsCmd)
}

func newEventsHandler(ctx context.Context, opts eventsHandlerOptions) (func(rstream.Event) error, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewID == nil {
		opts.NewID = newEventsCLIID
	}
	if opts.WebhookMode {
		if strings.TrimSpace(opts.WebhookSecret) == "" {
			return nil, errors.New("webhook signing secret is required")
		}
		if strings.TrimSpace(opts.WebhookID) == "" {
			return nil, errors.New("webhook id is required")
		}
	}
	return func(event rstream.Event) error {
		if len(opts.TypeFilter) > 0 {
			if _, ok := opts.TypeFilter[event.Type]; !ok {
				return nil
			}
		}
		if opts.WebhookMode {
			return handleWebhookModeEvent(ctx, opts, event)
		}
		body, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if opts.Forward != nil {
			return opts.Forward(ctx, body, nil)
		}
		_, err = opts.Stdout.Write(append(body, '\n'))
		return err
	}, nil
}

func handleWebhookModeEvent(ctx context.Context, opts eventsHandlerOptions, event rstream.Event) error {
	if !rstream.IsWebhookDeliverableEventType(event.Type) {
		return nil
	}
	fallbackID := ""
	if strings.TrimSpace(event.ID) == "" {
		var err error
		fallbackID, err = opts.NewID("evt_cli")
		if err != nil {
			return err
		}
	}
	webhookEvent, err := rstream.EventToWebhookEvent(event, fallbackID, opts.Now())
	if err != nil {
		return err
	}
	body, err := json.Marshal(webhookEvent)
	if err != nil {
		return err
	}
	deliveryID, err := opts.NewID("cli_del")
	if err != nil {
		return err
	}
	headerValues, err := rstream.BuildWebhookHeaderValues(body, webhookEvent, opts.WebhookSecret, rstream.WebhookHeaderOptions{
		WebhookID:  opts.WebhookID,
		DeliveryID: deliveryID,
		Timestamp:  opts.Now(),
	})
	if err != nil {
		return err
	}
	if opts.Forward != nil {
		return opts.Forward(ctx, body, eventsHTTPHeaders(headerValues))
	}
	if opts.IncludeHeaders {
		body, err = json.Marshal(eventsWebhookOutput{Headers: eventsHeaderMap(headerValues), Body: webhookEvent})
		if err != nil {
			return err
		}
	}
	_, err = opts.Stdout.Write(append(body, '\n'))
	return err
}

func newEventsForwarder(rawURL string, insecureTLS bool) (eventsForwarder, error) {
	dst, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid --forward-to: %w", err)
	}
	if dst.Scheme != "http" && dst.Scheme != "https" {
		return nil, fmt.Errorf("invalid --forward-to: expected http or https URL, got %q", dst.Scheme)
	}
	cl := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: sameHostRedirectPolicy,
	}
	if dst.Scheme == "https" && insecureTLS {
		cl.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return func(ctx context.Context, body []byte, headers http.Header) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, dst.String(), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for key, values := range headers {
			req.Header.Del(key)
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
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
	}, nil
}

func eventsHTTPHeaders(values rstream.WebhookHeaderValues) http.Header {
	headers := http.Header{}
	values.ApplyTo(headers)
	return headers
}

func eventsHeaderMap(values rstream.WebhookHeaderValues) map[string]string {
	return map[string]string{
		rstream.WebhookSignatureHeader:  values.Signature,
		rstream.WebhookEventIDHeader:    values.EventID,
		rstream.WebhookEventTypeHeader:  values.EventType,
		rstream.WebhookIDHeader:         values.WebhookID,
		rstream.WebhookDeliveryIDHeader: values.DeliveryID,
	}
}

func newEventsCLIID(prefix string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.TrimRight(prefix, "_") + "_" + hex.EncodeToString(buf), nil
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

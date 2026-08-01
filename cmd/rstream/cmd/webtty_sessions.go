// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var webttySessionsCmd = &cobra.Command{
	Use:          "sessions",
	Short:        "Inspect managed WebTTY sessions",
	GroupID:      "webtty-managed-session",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var webttySessionsListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List managed WebTTY sessions",
	GroupID:      "webtty-session-primary",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		params, err := webTTYSessionsListParamsFromFlags(cmd)
		if err != nil {
			return err
		}
		sessions, err := client.ListWebTTYSessions(cmd.Context(), params)
		if err != nil {
			return fmt.Errorf("failed to list WebTTY sessions: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "table":
			return printWebTTYSessionsTable(cmd.OutOrStdout(), sessions)
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), sessions)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), sessions)
		default:
			return validateOutputMode(output, "table", "json", "yaml")
		}
	},
}

var webttySessionsShowCmd = &cobra.Command{
	Use:          "show <session-id>",
	Short:        "Show a managed WebTTY session",
	GroupID:      "webtty-session-primary",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		session, err := client.GetWebTTYSession(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("failed to read WebTTY session: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), session)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), session)
		default:
			return validateOutputMode(output, "json", "yaml")
		}
	},
}

var webttySessionsEventsCmd = &cobra.Command{
	Use:          "events <session-id>",
	Short:        "Read managed WebTTY session events",
	GroupID:      "webtty-session-advanced",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		params, err := webTTYSessionEventsParamsFromFlags(cmd)
		if err != nil {
			return err
		}
		events, err := client.ListWebTTYSessionEvents(cmd.Context(), args[0], params)
		if err != nil {
			return fmt.Errorf("failed to read WebTTY session events: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), events)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), events)
		default:
			return validateOutputMode(output, "json", "yaml")
		}
	},
}

var webttySessionsExportCmd = &cobra.Command{
	Use:          "export <session-id>",
	Short:        "Export a managed WebTTY session recording",
	GroupID:      "webtty-session-primary",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYSessionExport(cmd, args[0])
	},
}

var webttySessionsParticipantsCmd = &cobra.Command{
	Use:          "participants <session-id>",
	Short:        "List managed WebTTY session participants",
	GroupID:      "webtty-session-advanced",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		participants, err := client.ListWebTTYParticipants(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("failed to list WebTTY participants: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "table":
			return printWebTTYParticipantsTable(cmd.OutOrStdout(), participants)
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), participants)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), participants)
		default:
			return validateOutputMode(output, "table", "json", "yaml")
		}
	},
}

var webttySessionsJoinCmd = &cobra.Command{
	Use:          "join <session-id>",
	Short:        "Join a live managed WebTTY session",
	GroupID:      "webtty-session-primary",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYSessionAttach(cmd, args[0])
	},
}

var webttySessionsControlRequestsCmd = &cobra.Command{
	Use:          "control-requests <session-id>",
	Short:        "List WebTTY control requests",
	GroupID:      "webtty-session-advanced",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		params, err := webTTYControlRequestsParamsFromFlags(cmd)
		if err != nil {
			return err
		}
		requests, err := client.ListWebTTYControlRequests(cmd.Context(), args[0], params)
		if err != nil {
			return fmt.Errorf("failed to list WebTTY control requests: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "table":
			return printWebTTYControlRequestsTable(cmd.OutOrStdout(), requests)
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), requests)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), requests)
		default:
			return validateOutputMode(output, "table", "json", "yaml")
		}
	},
}

var webttySessionsRequestControlCmd = &cobra.Command{
	Use:          "request-control <session-id>",
	Short:        "Request control of a live WebTTY session",
	GroupID:      "webtty-session-advanced",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		capabilities, err := client.GetWebTTYCapabilities(cmd.Context())
		if err != nil {
			return fmt.Errorf("failed to read WebTTY capabilities: %w", err)
		}
		if err := validateWebTTYSessionControlCapabilities(capabilities); err != nil {
			return err
		}
		participantID, _ := cmd.Flags().GetString("participant-id")
		participantID = strings.TrimSpace(participantID)
		if participantID == "" {
			return fmt.Errorf("--participant-id is required")
		}
		reason, _ := cmd.Flags().GetString("reason")
		request, err := client.CreateWebTTYControlRequest(cmd.Context(), args[0], rstream.CreateWebTTYControlRequest{
			ParticipantID: participantID,
			Reason:        strings.TrimSpace(reason),
		})
		if err != nil {
			return fmt.Errorf("failed to request WebTTY control: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), request)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), request)
		default:
			return validateOutputMode(output, "json", "yaml")
		}
	},
}

var webttySessionsResolveControlCmd = &cobra.Command{
	Use:          "resolve-control <session-id> <request-id>",
	Short:        "Resolve a WebTTY control request",
	GroupID:      "webtty-session-advanced",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := webTTYAPIClientFromCommand(cmd)
		if err != nil {
			return err
		}
		capabilities, err := client.GetWebTTYCapabilities(cmd.Context())
		if err != nil {
			return fmt.Errorf("failed to read WebTTY capabilities: %w", err)
		}
		if err := validateWebTTYSessionControlCapabilities(capabilities); err != nil {
			return err
		}
		params, err := webTTYResolveControlRequestFromFlags(cmd)
		if err != nil {
			return err
		}
		request, err := client.ResolveWebTTYControlRequest(cmd.Context(), args[0], args[1], params)
		if err != nil {
			return fmt.Errorf("failed to resolve WebTTY control request: %w", err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "json":
			return printWebTTYJSON(cmd.OutOrStdout(), request)
		case "yaml":
			return printWebTTYYAML(cmd.OutOrStdout(), request)
		default:
			return validateOutputMode(output, "json", "yaml")
		}
	},
}

func init() {
	webttySessionsCmd.Flags().SortFlags = false
	webttySessionsCmd.PersistentFlags().SortFlags = false
	webttySessionsCmd.AddGroup(&cobra.Group{ID: "webtty-session-primary", Title: "Session Commands:"})
	webttySessionsCmd.AddGroup(&cobra.Group{ID: "webtty-session-advanced", Title: "Advanced Commands:"})
	webttySessionsListCmd.Flags().SortFlags = false
	webttySessionsListCmd.Flags().String("server-id", "", "filter by registered WebTTY server ID")
	webttySessionsListCmd.Flags().String("tunnel-id", "", "filter by tunnel ID")
	webttySessionsListCmd.Flags().String("user-id", "", "filter by initiating user ID")
	webttySessionsListCmd.Flags().String("group-id", "", "filter by session group ID")
	webttySessionsListCmd.Flags().String("origin", "", "filter by origin (human, codex, api, automation, ci, scheduled-job)")
	webttySessionsListCmd.Flags().String("status", "", "filter by status (opening, active, closing, closed, errored)")
	webttySessionsListCmd.Flags().String("started-from", "", "filter sessions started at or after an RFC3339 timestamp")
	webttySessionsListCmd.Flags().String("started-to", "", "filter sessions started at or before an RFC3339 timestamp")
	webttySessionsListCmd.Flags().Int("limit", 0, "maximum number of sessions to return")
	webttySessionsListCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	webttySessionsShowCmd.Flags().SortFlags = false
	webttySessionsShowCmd.Flags().StringP("output", "o", "json", "output mode (json, yaml)")
	webttySessionsEventsCmd.Flags().SortFlags = false
	webttySessionsEventsCmd.Flags().String("from-seq", "", "first event sequence cursor to read")
	webttySessionsEventsCmd.Flags().Int("limit", 0, "maximum number of events to return")
	webttySessionsEventsCmd.Flags().StringP("output", "o", "json", "output mode (json, yaml)")
	webttySessionsExportCmd.Flags().SortFlags = false
	webttySessionsExportCmd.Flags().String("format", "text", "export format (text, json)")
	webttySessionsExportCmd.Flags().String("file", "", "write export to a file instead of stdout")
	webttySessionsExportCmd.Flags().String("from-seq", "", "first event sequence cursor to export")
	webttySessionsExportCmd.Flags().Int("max-events", 0, "maximum number of events to export")
	webttySessionsExportCmd.Flags().Bool("include-stdin", false, "include stdin payloads in text exports")
	webttySessionsExportCmd.Flags().Bool("include-stdout", true, "include stdout payloads in text exports")
	webttySessionsExportCmd.Flags().Bool("include-stderr", true, "include stderr payloads in text exports")
	webttySessionsExportCmd.Flags().Bool("include-timestamps", false, "include event timestamps in text exports")
	webttySessionsExportCmd.Flags().Bool("include-resize-markers", false, "include terminal resize markers in text exports")
	webttySessionsExportCmd.Flags().Bool("terminal-mode-markers", true, "include terminal mode markers in text exports")
	webttySessionsExportCmd.Flags().Bool("active-alternate-screen", true, "include the active alternate-screen snapshot when the session is still in alternate screen")
	webttySessionsParticipantsCmd.Flags().SortFlags = false
	webttySessionsParticipantsCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	webttySessionsJoinCmd.Flags().SortFlags = false
	webttySessionsJoinCmd.Flags().BoolP("interactive", "i", false, "forward stdin to the attached session")
	webttySessionsJoinCmd.Flags().BoolP("tty", "t", false, "enable terminal raw mode and resize forwarding")
	webttySessionsJoinCmd.Flags().Bool("request-control", false, "request session control after attaching and forward stdin while attached")
	webttySessionsJoinCmd.Flags().String("reason", "", "control request reason")
	webttySessionsControlRequestsCmd.Flags().SortFlags = false
	webttySessionsControlRequestsCmd.Flags().String("status", "", "filter by status (pending, granted, refused, revoked, expired)")
	webttySessionsControlRequestsCmd.Flags().Int("limit", 0, "maximum number of control requests to return")
	webttySessionsControlRequestsCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	webttySessionsRequestControlCmd.Flags().SortFlags = false
	webttySessionsRequestControlCmd.Flags().String("participant-id", "", "attached participant requesting control")
	webttySessionsRequestControlCmd.Flags().String("reason", "", "control request reason")
	webttySessionsRequestControlCmd.Flags().StringP("output", "o", "json", "output mode (json, yaml)")
	webttySessionsResolveControlCmd.Flags().SortFlags = false
	webttySessionsResolveControlCmd.Flags().String("action", "", "resolution action (grant, refuse, revoke)")
	webttySessionsResolveControlCmd.Flags().String("approver-participant-id", "", "controller participant granting the request; omit for permission-based approval")
	webttySessionsResolveControlCmd.Flags().String("reason", "", "resolution reason")
	webttySessionsResolveControlCmd.Flags().StringP("output", "o", "json", "output mode (json, yaml)")
	webttySessionsCmd.AddCommand(webttySessionsListCmd, webttySessionsShowCmd, webttySessionsEventsCmd, webttySessionsExportCmd, webttySessionsParticipantsCmd, webttySessionsJoinCmd, webttySessionsControlRequestsCmd, webttySessionsRequestControlCmd, webttySessionsResolveControlCmd)
	webttyCmd.AddCommand(webttySessionsCmd)
}

func webTTYAPIClientFromCommand(cmd *cobra.Command) (*rstream.Client, error) {
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return nil, err
	}
	return newClientFromResolved(runtime.Resolved)
}

func webTTYSessionsListParamsFromFlags(cmd *cobra.Command) (*rstream.ListWebTTYSessionsParams, error) {
	filters := &rstream.ListWebTTYSessionsFilters{}
	setStringFilter := func(flag string, dst **string) {
		value, _ := cmd.Flags().GetString(flag)
		value = strings.TrimSpace(value)
		if value != "" {
			*dst = &value
		}
	}
	setStringFilter("server-id", &filters.ServerID)
	setStringFilter("tunnel-id", &filters.TunnelID)
	setStringFilter("user-id", &filters.UserID)
	setStringFilter("group-id", &filters.GroupID)
	setStringFilter("origin", &filters.Origin)
	setStringFilter("status", &filters.Status)
	if startedFrom, err := webTTYTimeFilterFromFlag(cmd, "started-from"); err != nil {
		return nil, err
	} else if startedFrom != nil {
		filters.StartedFrom = startedFrom
	}
	if startedTo, err := webTTYTimeFilterFromFlag(cmd, "started-to"); err != nil {
		return nil, err
	} else if startedTo != nil {
		filters.StartedTo = startedTo
	}
	if filters.StartedFrom != nil && filters.StartedTo != nil && filters.StartedFrom.After(*filters.StartedTo) {
		return nil, fmt.Errorf("--started-from must be before --started-to")
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be greater than zero")
	}
	params := &rstream.ListWebTTYSessionsParams{}
	if limit > 0 {
		params.Limit = &limit
	}
	if filters.ServerID != nil || filters.TunnelID != nil || filters.UserID != nil || filters.GroupID != nil || filters.Origin != nil || filters.Status != nil || filters.StartedFrom != nil || filters.StartedTo != nil {
		params.Filters = filters
	}
	return params, nil
}

func webTTYTimeFilterFromFlag(cmd *cobra.Command, name string) (*time.Time, error) {
	value, _ := cmd.Flags().GetString(name)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("--%s must be an RFC3339 timestamp: %w", name, err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func webTTYSessionEventsParamsFromFlags(cmd *cobra.Command) (*rstream.ListWebTTYSessionEventsParams, error) {
	fromSeq, _ := cmd.Flags().GetString("from-seq")
	fromSeq = strings.TrimSpace(fromSeq)
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be greater than zero")
	}
	params := &rstream.ListWebTTYSessionEventsParams{}
	if fromSeq != "" {
		params.FromSeq = &fromSeq
	}
	if limit > 0 {
		params.Limit = &limit
	}
	return params, nil
}

func webTTYControlRequestsParamsFromFlags(cmd *cobra.Command) (*rstream.ListWebTTYControlRequestsParams, error) {
	status, _ := cmd.Flags().GetString("status")
	status = strings.TrimSpace(status)
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be greater than zero")
	}
	params := &rstream.ListWebTTYControlRequestsParams{}
	if limit > 0 {
		params.Limit = &limit
	}
	if status != "" {
		params.Filters = &rstream.ListWebTTYControlRequestsFilters{Status: &status}
	}
	return params, nil
}

func webTTYResolveControlRequestFromFlags(cmd *cobra.Command) (rstream.ResolveWebTTYControlRequest, error) {
	action, _ := cmd.Flags().GetString("action")
	action = strings.TrimSpace(action)
	if action == "" {
		return rstream.ResolveWebTTYControlRequest{}, fmt.Errorf("--action is required")
	}
	approverParticipantID, _ := cmd.Flags().GetString("approver-participant-id")
	approverParticipantID = strings.TrimSpace(approverParticipantID)
	reason, _ := cmd.Flags().GetString("reason")
	return rstream.ResolveWebTTYControlRequest{
		Action:                action,
		ApproverParticipantID: approverParticipantID,
		Reason:                strings.TrimSpace(reason),
	}, nil
}

const webTTYSessionExportPageSize = 1000

type webTTYSessionExportFormat string

const (
	webTTYSessionExportFormatText webTTYSessionExportFormat = "text"
	webTTYSessionExportFormatJSON webTTYSessionExportFormat = "json"
)

type webTTYSessionExportOptions struct {
	ActiveAlternateScreen bool
	Format                webTTYSessionExportFormat
	FromSeq               uint64
	IncludeResizeMarkers  bool
	IncludeStderr         bool
	IncludeStdin          bool
	IncludeStdout         bool
	IncludeTerminalModes  bool
	IncludeTimestamps     bool
	MaxEvents             int
	Path                  string
}

type webTTYSessionJSONExport struct {
	ExportVersion int                        `json:"export_version"`
	GeneratedAt   time.Time                  `json:"generated_at"`
	Session       *rstream.WebTTYSession     `json:"session"`
	Events        []webTTYSessionExportEvent `json:"events"`
	Warnings      []string                   `json:"warnings,omitempty"`
}

type webTTYSessionExportEvent struct {
	rstream.WebTTYSessionEvent
	PayloadPlaintext []byte `json:"payload_plaintext,omitempty"`
}

type webTTYSessionTextExport struct {
	DetectedAlternateScreen bool
	Text                    string
	Warnings                []string
}

func runWebTTYSessionExport(cmd *cobra.Command, sessionID string) error {
	options, err := webTTYSessionExportOptionsFromFlags(cmd)
	if err != nil {
		return err
	}
	client, err := webTTYAPIClientFromCommand(cmd)
	if err != nil {
		return err
	}
	session, err := client.GetWebTTYSession(cmd.Context(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to read WebTTY session: %w", err)
	}
	events, err := webTTYReadAllSessionEvents(cmd.Context(), client, sessionID, options.FromSeq, options.MaxEvents)
	if err != nil {
		return err
	}
	attachCrypto, err := webTTYSessionAttachPayloadCrypto(cmd.Context(), nil, client, session)
	if err != nil {
		return err
	}
	payloadCrypto := webTTYSessionAttachPayloadCryptoValue(attachCrypto)
	exported, err := webTTYDecryptSessionExportEvents(cmd.Context(), client, session, events, payloadCrypto)
	if err != nil {
		return err
	}
	w, closeOut, err := webTTYSessionExportWriter(cmd, options.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = closeOut()
	}()
	switch options.Format {
	case webTTYSessionExportFormatText:
		textExport := webTTYRenderSessionTextExport(events, exported, options)
		_, err := io.WriteString(w, textExport.Text)
		return err
	case webTTYSessionExportFormatJSON:
		jsonExport := webTTYSessionJSONExport{
			ExportVersion: 1,
			GeneratedAt:   time.Now().UTC(),
			Session:       session,
			Events:        exported,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonExport)
	default:
		return validateOutputMode(string(options.Format), string(webTTYSessionExportFormatText), string(webTTYSessionExportFormatJSON))
	}
}

func webTTYSessionExportOptionsFromFlags(cmd *cobra.Command) (webTTYSessionExportOptions, error) {
	format, _ := cmd.Flags().GetString("format")
	format = strings.TrimSpace(strings.ToLower(format))
	if format == "" {
		format = string(webTTYSessionExportFormatText)
	}
	if format != string(webTTYSessionExportFormatText) && format != string(webTTYSessionExportFormatJSON) {
		return webTTYSessionExportOptions{}, validateOutputMode(format, string(webTTYSessionExportFormatText), string(webTTYSessionExportFormatJSON))
	}
	fromSeqText, _ := cmd.Flags().GetString("from-seq")
	fromSeq, err := webTTYParseSequenceCursor(fromSeqText, "--from-seq")
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	maxEvents, _ := cmd.Flags().GetInt("max-events")
	if maxEvents < 0 {
		return webTTYSessionExportOptions{}, fmt.Errorf("--max-events must be zero or greater")
	}
	path, _ := cmd.Flags().GetString("file")
	includeStdin, _ := cmd.Flags().GetBool("include-stdin")
	includeStdout, _ := cmd.Flags().GetBool("include-stdout")
	includeStderr, _ := cmd.Flags().GetBool("include-stderr")
	includeTimestamps, _ := cmd.Flags().GetBool("include-timestamps")
	includeResizeMarkers, _ := cmd.Flags().GetBool("include-resize-markers")
	includeTerminalModes, _ := cmd.Flags().GetBool("terminal-mode-markers")
	activeAlternateScreen, _ := cmd.Flags().GetBool("active-alternate-screen")
	return webTTYSessionExportOptions{
		ActiveAlternateScreen: activeAlternateScreen,
		Format:                webTTYSessionExportFormat(format),
		FromSeq:               fromSeq,
		IncludeResizeMarkers:  includeResizeMarkers,
		IncludeStderr:         includeStderr,
		IncludeStdin:          includeStdin,
		IncludeStdout:         includeStdout,
		IncludeTerminalModes:  includeTerminalModes,
		IncludeTimestamps:     includeTimestamps,
		MaxEvents:             maxEvents,
		Path:                  strings.TrimSpace(path),
	}, nil
}

func webTTYParseSequenceCursor(value string, field string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned integer: %w", field, err)
	}
	return parsed, nil
}

func webTTYReadAllSessionEvents(ctx context.Context, client *rstream.Client, sessionID string, fromSeq uint64, maxEvents int) ([]rstream.WebTTYSessionEvent, error) {
	if client == nil {
		return nil, fmt.Errorf("rstream client is required")
	}
	var events []rstream.WebTTYSessionEvent
	nextSeq := fromSeq
	for {
		limit := webTTYSessionExportPageSize
		if maxEvents > 0 {
			remaining := maxEvents - len(events)
			if remaining <= 0 {
				break
			}
			if remaining < limit {
				limit = remaining
			}
		}
		fromSeqText := strconv.FormatUint(nextSeq, 10)
		page, err := client.ListWebTTYSessionEvents(ctx, sessionID, &rstream.ListWebTTYSessionEventsParams{
			FromSeq: &fromSeqText,
			Limit:   &limit,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to read WebTTY session events: %w", err)
		}
		if page == nil || len(*page) == 0 {
			break
		}
		events = append(events, *page...)
		lastSeq, err := webTTYParseSequenceCursor((*page)[len(*page)-1].Seq, "event seq")
		if err != nil {
			return nil, err
		}
		if lastSeq == ^uint64(0) {
			break
		}
		nextSeq = lastSeq + 1
		if len(*page) < limit {
			break
		}
	}
	return events, nil
}

func webTTYDecryptSessionExportEvents(ctx context.Context, client *rstream.Client, session *rstream.WebTTYSession, events []rstream.WebTTYSessionEvent, payloadCrypto *webtty.PayloadCrypto) ([]webTTYSessionExportEvent, error) {
	out := make([]webTTYSessionExportEvent, 0, len(events))
	for _, event := range events {
		item := webTTYSessionExportEvent{WebTTYSessionEvent: event}
		payload, ok, err := webTTYDecryptSessionEventPayload(ctx, client, session, event, payloadCrypto)
		if err != nil {
			return nil, err
		}
		if ok {
			item.PayloadPlaintext = payload
		}
		out = append(out, item)
	}
	return out, nil
}

func webTTYDecryptSessionEventPayload(ctx context.Context, client *rstream.Client, session *rstream.WebTTYSession, event rstream.WebTTYSessionEvent, payloadCrypto *webtty.PayloadCrypto) ([]byte, bool, error) {
	if event.Type != rstream.WebTTYSessionEventData {
		return nil, false, nil
	}
	if len(event.PayloadPlaintext) > 0 {
		return append([]byte(nil), event.PayloadPlaintext...), true, nil
	}
	if len(event.PayloadCiphertext) == 0 {
		return nil, false, nil
	}
	if payloadCrypto == nil {
		return nil, false, webTTYSessionExportDecryptUnavailableError(ctx, client, session)
	}
	encrypted, err := webTTYEncryptedPayloadFromSessionEvent(event)
	if err != nil {
		return nil, false, err
	}
	decrypt, err := webTTYSessionEventDecryptHook(payloadCrypto, event.StreamType)
	if err != nil {
		return nil, false, err
	}
	plaintext, err := decrypt(ctx, encrypted)
	if err != nil {
		return nil, false, fmt.Errorf("decrypt WebTTY session event %s seq %s: %w", event.ID, event.Seq, err)
	}
	return plaintext, true, nil
}

func webTTYSessionExportDecryptUnavailableError(ctx context.Context, client *rstream.Client, session *rstream.WebTTYSession) error {
	if session != nil && session.EncryptionMode == rstream.WebTTYEncryptionModeE2E {
		recipientKinds, err := webTTYSessionKeyGrantRecipientKinds(ctx, client, session.ID)
		if err != nil {
			return err
		}
		return webTTYSessionE2EDecryptUnavailableError(session, recipientKinds, "recording payload")
	}
	if session != nil && session.EncryptionMode == rstream.WebTTYEncryptionModeManaged {
		return fmt.Errorf("WebTTY session %s uses server-managed recording encryption; local CLI export cannot decrypt it without an engine-side decrypt API", session.ID)
	}
	if session != nil {
		return fmt.Errorf("WebTTY session %s recording payload is encrypted but no local decrypt material is available", session.ID)
	}
	return fmt.Errorf("WebTTY recording payload is encrypted but no local decrypt material is available")
}

func webTTYEncryptedPayloadFromSessionEvent(event rstream.WebTTYSessionEvent) (*webtty.EncryptedPayload, error) {
	if event.Crypto == nil {
		return nil, fmt.Errorf("WebTTY session event %s seq %s is missing crypto metadata", event.ID, event.Seq)
	}
	if event.PayloadLength < 0 || uint64(event.PayloadLength) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("WebTTY session event %s seq %s has invalid payload length %d", event.ID, event.Seq, event.PayloadLength)
	}
	payloadCrypto, err := webTTYPayloadCryptoMetadataFromMetadata(*event.Crypto)
	if err != nil {
		return nil, fmt.Errorf("decode WebTTY session event %s seq %s crypto metadata: %w", event.ID, event.Seq, err)
	}
	return &webtty.EncryptedPayload{
		Ciphertext:      append([]byte(nil), event.PayloadCiphertext...),
		PlaintextLength: uint32(event.PayloadLength),
		PayloadCrypto:   payloadCrypto,
	}, nil
}

func webTTYSessionEventDecryptHook(payloadCrypto *webtty.PayloadCrypto, stream rstream.WebTTYStreamType) (webtty.PayloadDecryptFunc, error) {
	if payloadCrypto == nil {
		return nil, fmt.Errorf("WebTTY payload crypto is required")
	}
	switch stream {
	case rstream.WebTTYStreamTypeStdin:
		if payloadCrypto.DecryptStdin != nil {
			return payloadCrypto.DecryptStdin, nil
		}
	case rstream.WebTTYStreamTypeStdout:
		if payloadCrypto.DecryptStdout != nil {
			return payloadCrypto.DecryptStdout, nil
		}
	case rstream.WebTTYStreamTypeStderr:
		if payloadCrypto.DecryptStderr != nil {
			return payloadCrypto.DecryptStderr, nil
		}
	default:
		return nil, fmt.Errorf("unsupported WebTTY stream type %q", stream)
	}
	return nil, fmt.Errorf("missing WebTTY recording decrypt hook for %s", stream)
}

func webTTYSessionExportWriter(cmd *cobra.Command, path string) (io.Writer, func() error, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return cmd.OutOrStdout(), func() error { return nil }, nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open WebTTY session export file: %w", err)
	}
	return file, file.Close, nil
}

func webTTYRenderSessionTextExport(events []rstream.WebTTYSessionEvent, exported []webTTYSessionExportEvent, options webTTYSessionExportOptions) webTTYSessionTextExport {
	warnings := map[string]struct{}{}
	main := &webTTYTerminalTextBuffer{lines: [][]rune{{}}}
	var alternate *webTTYTerminalTextBuffer
	detectedAlternate := false
	for i, event := range events {
		if event.Type == rstream.WebTTYSessionEventResize {
			if options.IncludeResizeMarkers {
				main.Write(webTTYTextExportMarker(&event, webTTYSessionResizeMarker(event), options))
			}
			continue
		}
		if event.Type != rstream.WebTTYSessionEventData || i >= len(exported) || len(exported[i].PayloadPlaintext) == 0 {
			continue
		}
		stream := event.StreamType
		if !webTTYTextExportIncludesStream(stream, options) {
			continue
		}
		rawText := string(exported[i].PayloadPlaintext)
		for _, segment := range webTTYTerminalTextSegments(rawText) {
			if segment.kind == webTTYTerminalSegmentEnterAlternate {
				detectedAlternate = true
				warnings["alternate-screen"] = struct{}{}
				alternate = &webTTYTerminalTextBuffer{lines: [][]rune{{}}}
				if options.IncludeTerminalModes {
					main.Write(webTTYTextExportMarker(&event, "terminal alternate screen entered", options))
				}
				continue
			}
			if segment.kind == webTTYTerminalSegmentLeaveAlternate {
				detectedAlternate = true
				warnings["alternate-screen"] = struct{}{}
				alternate = nil
				if options.IncludeTerminalModes {
					main.Write(webTTYTextExportMarker(&event, "terminal alternate screen left", options))
				}
				continue
			}
			if segment.value == "" {
				continue
			}
			target := main
			if alternate != nil {
				target = alternate
			}
			if options.IncludeTimestamps {
				target.Write(webTTYTextExportDataPrefix(event, stream))
			}
			target.Write(segment.value)
		}
	}
	text := main.String()
	if alternate != nil && options.ActiveAlternateScreen {
		if options.IncludeTerminalModes {
			text += webTTYTextExportMarker(nil, "terminal alternate screen active", options)
		}
		text += alternate.String()
	}
	return webTTYSessionTextExport{
		DetectedAlternateScreen: detectedAlternate,
		Text:                    webTTYStripANSI(text),
		Warnings:                webTTYSortedWarningKeys(warnings),
	}
}

func webTTYTextExportIncludesStream(stream rstream.WebTTYStreamType, options webTTYSessionExportOptions) bool {
	switch stream {
	case rstream.WebTTYStreamTypeStdin:
		return options.IncludeStdin
	case rstream.WebTTYStreamTypeStdout:
		return options.IncludeStdout
	case rstream.WebTTYStreamTypeStderr:
		return options.IncludeStderr
	default:
		return false
	}
}

func webTTYTextExportMarker(event *rstream.WebTTYSessionEvent, message string, options webTTYSessionExportOptions) string {
	prefix := ""
	if options.IncludeTimestamps && event != nil && !event.CreatedAt.IsZero() {
		prefix = event.CreatedAt.UTC().Format(time.RFC3339Nano) + " "
	}
	return prefix + "[webtty] " + message + "\n"
}

func webTTYTextExportDataPrefix(event rstream.WebTTYSessionEvent, stream rstream.WebTTYStreamType) string {
	prefix := ""
	if !event.CreatedAt.IsZero() {
		prefix = event.CreatedAt.UTC().Format(time.RFC3339Nano) + " "
	}
	return prefix + "[" + string(stream) + "] "
}

func webTTYSessionResizeMarker(event rstream.WebTTYSessionEvent) string {
	var metadata struct {
		TerminalSize struct {
			Row uint32 `json:"row"`
			Col uint32 `json:"col"`
		} `json:"terminal_size"`
	}
	if len(event.Metadata) > 0 && json.Unmarshal(event.Metadata, &metadata) == nil && metadata.TerminalSize.Row > 0 && metadata.TerminalSize.Col > 0 {
		return fmt.Sprintf("terminal resized to %dx%d", metadata.TerminalSize.Col, metadata.TerminalSize.Row)
	}
	return "terminal resized"
}

func webTTYSortedWarningKeys(warnings map[string]struct{}) []string {
	out := make([]string, 0, len(warnings))
	for key := range warnings {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func runWebTTYSessionAttach(cmd *cobra.Command, sessionID string) error {
	runtime, err := resolveRuntime(cmd, true, true)
	if err != nil {
		return err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return err
	}
	capabilities, err := client.GetWebTTYCapabilities(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to read WebTTY capabilities: %w", err)
	}
	if err := validateWebTTYSessionJoinCapabilities(capabilities); err != nil {
		return err
	}
	requestControl, _ := cmd.Flags().GetBool("request-control")
	if requestControl {
		if err := validateWebTTYSessionControlCapabilities(capabilities); err != nil {
			return err
		}
	}
	session, err := client.GetWebTTYSession(cmd.Context(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to read WebTTY session: %w", err)
	}
	if err := validateWebTTYSessionAttachSupport(session); err != nil {
		return err
	}
	attachCrypto, err := webTTYSessionAttachPayloadCrypto(cmd.Context(), runtime, client, session)
	if err != nil {
		return err
	}
	participant, err := client.AttachWebTTYParticipant(cmd.Context(), sessionID, webTTYSessionAttachRequest(attachCrypto))
	if err != nil {
		return fmt.Errorf("failed to attach WebTTY participant: %w", err)
	}
	defer func() {
		ctx, cancel := webTTYDetachContext(cmd.Context())
		defer cancel()
		_, _ = client.DetachWebTTYParticipant(ctx, sessionID, participant.ID, rstream.DetachWebTTYParticipantRequest{Reason: "client disconnected"})
	}()
	streamURL, err := webTTYParticipantStreamURL(client, sessionID, participant.ID)
	if err != nil {
		return err
	}
	authToken := webTTYClientAuthToken(client)
	interactive, _ := cmd.Flags().GetBool("interactive")
	allocateTTY, _ := cmd.Flags().GetBool("tty")
	reason, _ := cmd.Flags().GetString("reason")
	attachCapabilities := []webtty.AttachCapability{webtty.AttachCapabilityReadStream}
	if requestControl {
		attachCapabilities = append(attachCapabilities, webtty.AttachCapabilityRequestControl, webtty.AttachCapabilityReceiveControl)
	}
	openDeadline := 10 * time.Second
	closeDeadline := 5 * time.Second
	var stdinReady chan struct{}
	if requestControl {
		stdinReady = make(chan struct{})
	}
	cfg := &webtty.ClientConfig{
		URL:               streamURL,
		Transport:         webtty.WebTTYTransportWebSocket,
		DialTLSContext:    newWebTTYEngineDialTLSContext(client),
		Interactive:       interactive || requestControl,
		AllocateTTY:       allocateTTY,
		Stdin:             os.Stdin,
		Stdout:            cmd.OutOrStdout(),
		Stderr:            cmd.ErrOrStderr(),
		AuthToken:         authToken,
		OpenDeadline:      &openDeadline,
		CloseDeadline:     &closeDeadline,
		SendHeartbeat:     true,
		Attach:            webTTYAttachConfigFromParticipant(session, participant, attachCapabilities),
		PayloadCrypto:     webTTYSessionAttachPayloadCryptoValue(attachCrypto),
		EndpointIdentity:  webTTYSessionAttachEndpointIdentity(attachCrypto),
		ClientCredential:  webTTYSessionAttachClientCredential(attachCrypto),
		ClientPrincipalID: webTTYSessionAttachClientPrincipalID(attachCrypto),
		ClientDeviceID:    webTTYSessionAttachDeviceID(attachCrypto),
		ClientBrowserID:   webTTYSessionAttachBrowserID(attachCrypto),
		TLSConfig:         nil,
		ReadBufferSize:    nil,
		WriteBufferSize:   nil,
	}
	if requestControl {
		return runWebTTYSessionAttachWithControl(cmd.Context(), client, sessionID, participant.ID, strings.TrimSpace(reason), cfg, stdinReady)
	}
	exitCode, err := webtty.RunClient(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	if exitCode != 0 && exitCode != -1 {
		return &commandExitError{code: exitCode}
	}
	return nil
}

type webTTYSessionAttachRunResult struct {
	exitCode int
	err      error
}

func runWebTTYSessionAttachWithControl(ctx context.Context, client *rstream.Client, sessionID string, participantID string, reason string, cfg *webtty.ClientConfig, stdinReady chan struct{}) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	cfgCopy := *cfg
	cfgCopy.Stdin = &webTTYReadyFileReader{ctx: runCtx, file: os.Stdin, ready: stdinReady}
	resultCh := make(chan webTTYSessionAttachRunResult, 1)
	go func() {
		exitCode, err := webtty.RunClient(runCtx, &cfgCopy)
		resultCh <- webTTYSessionAttachRunResult{exitCode: exitCode, err: err}
	}()
	if result, err := waitForWebTTYParticipantLive(runCtx, client, sessionID, participantID, resultCh); result != nil || err != nil {
		if result != nil {
			return webTTYSessionAttachRunResultError(*result)
		}
		cancelRun()
		return webTTYSessionAttachAbort(resultCh, err)
	}
	request, err := client.CreateWebTTYControlRequest(runCtx, sessionID, rstream.CreateWebTTYControlRequest{
		ParticipantID: participantID,
		Reason:        reason,
	})
	if err != nil {
		cancelRun()
		return webTTYSessionAttachAbort(resultCh, fmt.Errorf("failed to request WebTTY control: %w", err))
	}
	if result, err := waitForWebTTYControlGrant(runCtx, client, sessionID, request.ID, resultCh); result != nil || err != nil {
		if result != nil {
			return webTTYSessionAttachRunResultError(*result)
		}
		cancelRun()
		return webTTYSessionAttachAbort(resultCh, err)
	}
	close(stdinReady)
	result := <-resultCh
	return webTTYSessionAttachRunResultError(result)
}

func waitForWebTTYParticipantLive(ctx context.Context, client *rstream.Client, sessionID string, participantID string, resultCh <-chan webTTYSessionAttachRunResult) (*webTTYSessionAttachRunResult, error) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case result := <-resultCh:
			return &result, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if lastErr != nil {
				return nil, fmt.Errorf("timed out waiting for WebTTY participant stream to become live: %w", lastErr)
			}
			return nil, fmt.Errorf("timed out waiting for WebTTY participant stream to become live")
		case <-ticker.C:
			participants, err := client.ListWebTTYParticipants(ctx, sessionID)
			if err != nil {
				lastErr = err
				continue
			}
			for _, participant := range *participants {
				if participant.ID != participantID {
					continue
				}
				if participant.DetachedAt != nil {
					return nil, fmt.Errorf("WebTTY participant detached before the live stream was ready")
				}
				if participant.Live.Connected {
					return nil, nil
				}
			}
		}
	}
}

func waitForWebTTYControlGrant(ctx context.Context, client *rstream.Client, sessionID string, requestID string, resultCh <-chan webTTYSessionAttachRunResult) (*webTTYSessionAttachRunResult, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case result := <-resultCh:
			return &result, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			requests, err := client.ListWebTTYControlRequests(ctx, sessionID, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to read WebTTY control requests: %w", err)
			}
			for _, request := range *requests {
				if request.ID != requestID {
					continue
				}
				switch request.Status {
				case rstream.WebTTYControlRequestGranted:
					return nil, nil
				case rstream.WebTTYControlRequestRefused, rstream.WebTTYControlRequestRevoked, rstream.WebTTYControlRequestExpired:
					return nil, fmt.Errorf("WebTTY control request %s was %s", request.ID, request.Status)
				}
			}
		}
	}
}

func webTTYSessionAttachAbort(resultCh <-chan webTTYSessionAttachRunResult, err error) error {
	select {
	case <-resultCh:
	case <-time.After(5 * time.Second):
	}
	return err
}

func webTTYSessionAttachRunResultError(result webTTYSessionAttachRunResult) error {
	if result.err != nil {
		return result.err
	}
	if result.exitCode != 0 && result.exitCode != -1 {
		return &commandExitError{code: result.exitCode}
	}
	return nil
}

type webTTYReadyFileReader struct {
	ctx   context.Context
	file  *os.File
	ready <-chan struct{}
	once  sync.Once
	err   error
}

func (r *webTTYReadyFileReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		select {
		case <-r.ctx.Done():
			r.err = r.ctx.Err()
		case <-r.ready:
		}
	})
	if r.err != nil {
		return 0, r.err
	}
	return r.file.Read(p)
}

func (r *webTTYReadyFileReader) Fd() uintptr {
	return r.file.Fd()
}

func webTTYDetachContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
}

func validateWebTTYSessionJoinCapabilities(capabilities *rstream.WebTTYCapabilities) error {
	if capabilities == nil || !capabilities.LiveAttach {
		return fmt.Errorf("WebTTY live session join is not available on this engine")
	}
	return nil
}

func validateWebTTYSessionControlCapabilities(capabilities *rstream.WebTTYCapabilities) error {
	if capabilities == nil || !capabilities.ControlTransfer {
		return fmt.Errorf("WebTTY control transfer is not available on this engine")
	}
	return nil
}

func validateWebTTYSessionAttachSupport(session *rstream.WebTTYSession) error {
	if session == nil {
		return fmt.Errorf("WebTTY session is required")
	}
	switch session.Status {
	case rstream.WebTTYSessionStatusOpening, rstream.WebTTYSessionStatusActive:
	default:
		return fmt.Errorf("WebTTY session %s is not active", session.ID)
	}
	if !session.Live.Available {
		return fmt.Errorf("WebTTY session %s is not live on this engine", session.ID)
	}
	if !session.Live.Attachable {
		return fmt.Errorf("WebTTY session %s cannot be joined from this engine", session.ID)
	}
	if !session.Live.HasUpstream {
		return fmt.Errorf("WebTTY session %s is still opening and has no upstream stream yet", session.ID)
	}
	return nil
}

type webTTYSessionAttachCrypto struct {
	PayloadCrypto     *webtty.PayloadCrypto
	EndpointIdentity  *webtty.WebTTYEndpointIdentity
	ClientCredential  []byte
	ClientPrincipalID string
	DeviceID          string
	BrowserID         string
}

func webTTYSessionAttachRequest(attachCrypto *webTTYSessionAttachCrypto) rstream.AttachWebTTYParticipantRequest {
	req := rstream.AttachWebTTYParticipantRequest{
		Role:      string(rstream.WebTTYParticipantRoleSpectator),
		Transport: rstream.WebTTYTransportWebSocket,
	}
	if attachCrypto != nil {
		req.DeviceID = strings.TrimSpace(attachCrypto.DeviceID)
		req.BrowserID = strings.TrimSpace(attachCrypto.BrowserID)
	}
	return req
}

func webTTYSessionAttachPayloadCryptoValue(attachCrypto *webTTYSessionAttachCrypto) *webtty.PayloadCrypto {
	if attachCrypto == nil {
		return nil
	}
	return attachCrypto.PayloadCrypto
}

func webTTYSessionAttachEndpointIdentity(attachCrypto *webTTYSessionAttachCrypto) *webtty.WebTTYEndpointIdentity {
	if attachCrypto == nil {
		return nil
	}
	return attachCrypto.EndpointIdentity
}

func webTTYSessionAttachClientCredential(attachCrypto *webTTYSessionAttachCrypto) []byte {
	if attachCrypto == nil {
		return nil
	}
	return append([]byte(nil), attachCrypto.ClientCredential...)
}

func webTTYSessionAttachClientPrincipalID(attachCrypto *webTTYSessionAttachCrypto) string {
	if attachCrypto == nil {
		return ""
	}
	return strings.TrimSpace(attachCrypto.ClientPrincipalID)
}

func webTTYSessionAttachDeviceID(attachCrypto *webTTYSessionAttachCrypto) string {
	if attachCrypto == nil {
		return ""
	}
	return strings.TrimSpace(attachCrypto.DeviceID)
}

func webTTYSessionAttachBrowserID(attachCrypto *webTTYSessionAttachCrypto) string {
	if attachCrypto == nil {
		return ""
	}
	return strings.TrimSpace(attachCrypto.BrowserID)
}

func webTTYSessionAttachPayloadCrypto(ctx context.Context, runtime *resolvedRuntime, client *rstream.Client, session *rstream.WebTTYSession) (*webTTYSessionAttachCrypto, error) {
	if session == nil {
		return nil, fmt.Errorf("WebTTY session is required")
	}
	if session.EncryptionMode != rstream.WebTTYEncryptionModeE2E {
		return nil, nil
	}
	attachCrypto, deviceErr := webTTYSessionPayloadCryptoFromWorkspaceDevices(ctx, runtime, client, session)
	if attachCrypto != nil {
		return attachCrypto, nil
	}
	if payloadCrypto, err := webTTYSessionPayloadCryptoFromEndpointIdentities(ctx, client, session); err != nil {
		return nil, err
	} else if payloadCrypto != nil {
		return &webTTYSessionAttachCrypto{PayloadCrypto: payloadCrypto}, nil
	}
	recipientKinds, err := webTTYSessionKeyGrantRecipientKinds(ctx, client, session.ID)
	if err != nil {
		return nil, err
	}
	if deviceErr != nil && webTTYSessionGrantKindsContainWorkspaceDevice(recipientKinds) {
		return nil, deviceErr
	}
	return nil, webTTYSessionE2EDecryptUnavailableError(session, recipientKinds, "session key grant")
}

func webTTYSessionKeyGrantRecipientKinds(ctx context.Context, client *rstream.Client, sessionID string) (map[string]struct{}, error) {
	if client == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	grants, err := client.ListWebTTYKeyGrants(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read WebTTY session key grants: %w", err)
	}
	if grants == nil {
		return nil, nil
	}
	kinds := make(map[string]struct{}, len(*grants))
	for _, grant := range *grants {
		kind := strings.TrimSpace(grant.RecipientKind)
		if kind != "" {
			kinds[kind] = struct{}{}
		}
	}
	return kinds, nil
}

func webTTYSessionE2EDecryptUnavailableError(session *rstream.WebTTYSession, recipientKinds map[string]struct{}, target string) error {
	sessionID := "unknown"
	workspaceID := ""
	if session != nil {
		sessionID = strings.TrimSpace(session.ID)
		workspaceID = strings.TrimSpace(session.WorkspaceID)
	}
	if sessionID == "" {
		sessionID = "unknown"
	}
	target = strings.TrimSpace(target)
	if target == "" {
		target = "recording"
	}
	if webTTYSessionGrantKindsContainWorkspaceDevice(recipientKinds) {
		message := fmt.Sprintf("WebTTY session %s uses workspace-managed end-to-end encryption and no local trusted workspace device can decrypt its %s", sessionID, target)
		if workspaceID != "" {
			message += "; " + workspaceDeviceEnrollmentHint(workspaceID)
		}
		return errors.New(message)
	}
	if webTTYSessionGrantKindsContainExplicitEndpoint(recipientKinds) {
		return fmt.Errorf("WebTTY session %s uses explicit-key end-to-end encryption and no local WebTTY endpoint identity can decrypt its %s; import or select the authorized endpoint identity, then retry", sessionID, target)
	}
	message := fmt.Sprintf("WebTTY session %s is end-to-end encrypted and no local trusted workspace device or WebTTY endpoint identity can decrypt its %s", sessionID, target)
	if workspaceID != "" {
		message += "; " + workspaceDeviceEnrollmentHint(workspaceID)
	}
	return errors.New(message)
}

func webTTYSessionGrantKindsContainWorkspaceDevice(recipientKinds map[string]struct{}) bool {
	if len(recipientKinds) == 0 {
		return false
	}
	_, ok := recipientKinds[webtty.E2ERecipientKindWorkspaceDevice]
	return ok
}

func webTTYSessionGrantKindsContainExplicitEndpoint(recipientKinds map[string]struct{}) bool {
	if len(recipientKinds) == 0 {
		return false
	}
	for _, kind := range []string{webtty.E2ERecipientKindPublicKey, webtty.E2ERecipientKindServer} {
		if _, ok := recipientKinds[kind]; ok {
			return true
		}
	}
	return false
}

type webTTYSessionWorkspaceDeviceGrantUnavailableError struct {
	sessionID string
}

func (e *webTTYSessionWorkspaceDeviceGrantUnavailableError) Error() string {
	sessionID := strings.TrimSpace(e.sessionID)
	if sessionID == "" {
		sessionID = "unknown"
	}
	return fmt.Sprintf("WebTTY session %s uses workspace-managed end-to-end encryption, but the local trusted workspace device did not receive a key grant for this session; use the trusted browser or device that opened or joined the session, or join the session from this device before exporting it", sessionID)
}

func webTTYSessionPayloadCryptoFromWorkspaceDevices(ctx context.Context, runtime *resolvedRuntime, client *rstream.Client, session *rstream.WebTTYSession) (*webTTYSessionAttachCrypto, error) {
	workspaceID := strings.TrimSpace(session.WorkspaceID)
	if workspaceID == "" {
		return nil, nil
	}
	devices, err := loadWorkspaceDeviceFiles(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace devices for WebTTY session decrypt: %w", err)
	}
	var lastErr error
	var activeWebTTYDevice bool
	for _, item := range devices {
		device := item.device
		if !workspaceDeviceIsActive(device) {
			continue
		}
		if strings.TrimSpace(device.WebTTYPublicKey) == "" || strings.TrimSpace(device.WebTTYKeyID) == "" {
			continue
		}
		activeWebTTYDevice = true
		identity, err := loadWorkspaceDeviceWebTTYIdentity(device)
		if err != nil {
			lastErr = err
			continue
		}
		kind := webtty.E2ERecipientKindWorkspaceDevice
		recipientID := strings.TrimSpace(device.DeviceKeyID)
		payloadCrypto, err := webTTYSessionPayloadCryptoFromGrantRecipient(ctx, client, session.ID, recipientID, kind, *identity)
		if err != nil {
			lastErr = err
			continue
		}
		if payloadCrypto != nil {
			attachCrypto := &webTTYSessionAttachCrypto{
				PayloadCrypto: payloadCrypto,
				DeviceID:      recipientID,
			}
			if runtime != nil {
				if err := webTTYSessionAttachWorkspaceCredential(ctx, runtime, session, recipientID, attachCrypto); err != nil {
					return nil, err
				}
			}
			return attachCrypto, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if activeWebTTYDevice {
		return nil, &webTTYSessionWorkspaceDeviceGrantUnavailableError{sessionID: session.ID}
	}
	return nil, nil
}

func webTTYSessionAttachWorkspaceCredential(ctx context.Context, runtime *resolvedRuntime, session *rstream.WebTTYSession, deviceKeyID string, attachCrypto *webTTYSessionAttachCrypto) error {
	if runtime == nil || session == nil || attachCrypto == nil {
		return nil
	}
	workspaceID := strings.TrimSpace(session.WorkspaceID)
	projectID := strings.TrimSpace(session.ProjectID)
	serverID := strings.TrimSpace(session.ServerID)
	if workspaceID == "" || projectID == "" || serverID == "" {
		return nil
	}
	proofItems, err := workspaceDeviceAccessProofsWithDevices(workspaceID, 8)
	if err != nil {
		return fmt.Errorf("load workspace device proof for WebTTY session attach: %w", err)
	}
	proofs := make([]controlplane.WorkspaceDeviceAccessProof, 0, len(proofItems))
	localDevices := make([]workspaceDeviceFile, 0, len(proofItems))
	for _, item := range proofItems {
		proofs = append(proofs, item.proof)
		localDevices = append(localDevices, item.device)
	}
	controlClient := newRuntimeControlPlaneClient(runtime.Resolved)
	resolved, err := controlClient.ResolveWebTTYServerClient(ctx, projectID, serverID, controlplane.ResolveWebTTYServerClientRequest{
		DeviceProofs: proofs,
	})
	if err != nil {
		if errors.Is(err, controlplane.ErrForbidden) {
			return fmt.Errorf("workspace-managed WebTTY E2E requires this machine to be a trusted workspace device; %s", workspaceDeviceEnrollmentHint(workspaceID))
		}
		return mapControlPlaneError(err)
	}
	if resolved.CurrentDevice == nil || strings.TrimSpace(resolved.CurrentDevice.DeviceKeyID) == "" {
		return nil
	}
	if deviceKeyID != "" && resolved.CurrentDevice.DeviceKeyID != deviceKeyID {
		return fmt.Errorf("workspace-managed WebTTY resolved trusted device %s does not match local session grant device %s", resolved.CurrentDevice.DeviceKeyID, deviceKeyID)
	}
	_, endpointIdentity, credential, err := webTTYCurrentWorkspaceDeviceRecipient(resolved, localDevices)
	if err != nil {
		return err
	}
	attachCrypto.EndpointIdentity = endpointIdentity
	attachCrypto.ClientCredential = append([]byte(nil), credential...)
	attachCrypto.ClientPrincipalID = resolved.CurrentDevice.DeviceKeyID
	attachCrypto.DeviceID = resolved.CurrentDevice.DeviceKeyID
	return nil
}

func webTTYSessionPayloadCryptoFromEndpointIdentities(ctx context.Context, client *rstream.Client, session *rstream.WebTTYSession) (*webtty.PayloadCrypto, error) {
	root, err := defaultRstreamHomeDir()
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "webtty", "identities", "*.identity.json"))
	if err != nil {
		return nil, fmt.Errorf("scan WebTTY identities: %w", err)
	}
	var lastErr error
	for _, path := range paths {
		identity, err := webtty.LoadWebTTYEndpointIdentityFile(path)
		if err != nil {
			continue
		}
		recipientID := webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID)
		payloadCrypto, err := webTTYSessionPayloadCryptoFromGrantRecipient(ctx, client, session.ID, recipientID, webtty.E2ERecipientKindPublicKey, identity.Encryption)
		if err != nil {
			lastErr = err
			continue
		}
		if payloadCrypto != nil {
			return payloadCrypto, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func webTTYSessionPayloadCryptoFromGrantRecipient(ctx context.Context, client *rstream.Client, sessionID string, recipientID string, recipientKind string, identity webtty.E2EIdentity) (*webtty.PayloadCrypto, error) {
	recipientID = strings.TrimSpace(recipientID)
	recipientKind = strings.TrimSpace(recipientKind)
	if recipientID == "" || recipientKind == "" {
		return nil, nil
	}
	grants, err := client.ListWebTTYKeyGrantDecryptMaterial(ctx, sessionID, &rstream.ListWebTTYKeyGrantDecryptMaterialParams{
		RecipientID:   &recipientID,
		RecipientKind: &recipientKind,
	})
	if err != nil {
		return nil, fmt.Errorf("read WebTTY session key grants: %w", err)
	}
	if grants == nil || len(*grants) == 0 {
		return nil, nil
	}
	var lastErr error
	for _, grant := range *grants {
		sessionKeyGrant, err := webTTYSessionKeyGrantEnvelopeFromGrant(grant)
		if err != nil {
			lastErr = err
			continue
		}
		payloadCrypto, err := webtty.NewE2EServerPayloadCrypto(sessionKeyGrant, identity)
		if err != nil {
			lastErr = err
			continue
		}
		return payloadCrypto, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("decrypt WebTTY session key grant: %w", lastErr)
	}
	return nil, nil
}

func webTTYSessionKeyGrantEnvelopeFromGrant(grant rstream.WebTTYKeyGrantDecryptMaterial) (*webtty.SessionKeyGrant, error) {
	sessionKeyGrant, err := webTTYSessionKeyGrantFromMetadata(grant.Crypto)
	if err != nil {
		return nil, err
	}
	if len(sessionKeyGrant.KeyEnvelopes) == 1 && len(sessionKeyGrant.KeyEnvelopes[0].WrappedKey) == 0 && len(grant.WrappedKey) > 0 {
		sessionKeyGrant.KeyEnvelopes[0].WrappedKey = append([]byte(nil), grant.WrappedKey...)
	}
	return sessionKeyGrant, nil
}

func webTTYSessionKeyGrantFromMetadata(crypto rstream.WebTTYCryptoMetadata) (*webtty.SessionKeyGrant, error) {
	payloadSuite, err := webTTYPayloadCipherSuiteFromAPI(crypto.PayloadSuite)
	if err != nil {
		return nil, err
	}
	keyEnvelopeSuite, err := webTTYKeyEnvelopeSuiteFromAPI(crypto.KeyEnvelopeSuite)
	if err != nil {
		return nil, err
	}
	payloadKeyID, err := webtty.DecodeE2EKeyMaterial(crypto.PayloadKeyID, webtty.E2EPayloadKeyIDSize, "WebTTY payload key id")
	if err != nil {
		return nil, err
	}
	keyContext, err := webTTYKeyContextFromAPIMetadata(crypto)
	if err != nil {
		return nil, err
	}
	keyEnvelopes, err := webTTYKeyEnvelopesFromAPIMetadata(crypto.KeyEnvelopes)
	if err != nil {
		return nil, err
	}
	return &webtty.SessionKeyGrant{
		PayloadSuite:     payloadSuite,
		PayloadKeyID:     payloadKeyID,
		KeyEnvelopes:     keyEnvelopes,
		KeyContext:       keyContext,
		KeyEnvelopeSuite: keyEnvelopeSuite,
	}, nil
}

func webTTYPayloadCryptoMetadataFromMetadata(crypto rstream.WebTTYCryptoMetadata) (*webtty.PayloadCryptoMetadata, error) {
	payloadSuite, err := webTTYPayloadCipherSuiteFromAPI(crypto.PayloadSuite)
	if err != nil {
		return nil, err
	}
	payloadKeyID, err := webtty.DecodeE2EKeyMaterial(crypto.PayloadKeyID, webtty.E2EPayloadKeyIDSize, "WebTTY payload key id")
	if err != nil {
		return nil, err
	}
	nonce, err := decodeOptionalWebTTYKeyMaterial(crypto.Nonce, "WebTTY payload nonce")
	if err != nil {
		return nil, err
	}
	keyContext, err := webTTYKeyContextFromAPIMetadata(crypto)
	if err != nil {
		return nil, err
	}
	return &webtty.PayloadCryptoMetadata{
		PayloadSuite: payloadSuite,
		PayloadKeyID: payloadKeyID,
		Nonce:        nonce,
		AADContext:   keyContext,
	}, nil
}

func webTTYPayloadCipherSuiteFromAPI(value string) (webtty.PayloadCipherSuite, error) {
	switch strings.TrimSpace(value) {
	case "aes-256-gcm":
		return webtty.PayloadCipherSuiteAES256GCM, nil
	default:
		return 0, fmt.Errorf("unsupported WebTTY payload cipher suite %q", value)
	}
}

func webTTYKeyEnvelopeSuiteFromAPI(value string) (webtty.KeyEnvelopeSuite, error) {
	switch strings.TrimSpace(value) {
	case "hpke-x25519-hkdf-sha256-aes-256-gcm":
		return webtty.KeyEnvelopeSuiteHPKEX25519HKDFSHA256AES256GCM, nil
	default:
		return 0, fmt.Errorf("unsupported WebTTY key envelope suite %q", value)
	}
}

func webTTYKeyContextFromAPIMetadata(crypto rstream.WebTTYCryptoMetadata) ([]byte, error) {
	if strings.TrimSpace(crypto.KeyContextRaw) != "" {
		return webtty.DecodeE2EKeyMaterial(crypto.KeyContextRaw, 0, "WebTTY key context")
	}
	if len(crypto.KeyContext) == 0 {
		return nil, nil
	}
	var encoded struct {
		Encoding string `json:"encoding"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(crypto.KeyContext, &encoded); err == nil && encoded.Encoding == "base64" {
		if encoded.Value == "" {
			return nil, nil
		}
		return decodeBase64WebTTYKeyContext(encoded.Value)
	}
	return append([]byte(nil), crypto.KeyContext...), nil
}

func decodeBase64WebTTYKeyContext(value string) ([]byte, error) {
	data, err := webtty.DecodeE2EKeyMaterial(value, 0, "WebTTY key context")
	if err == nil {
		return data, nil
	}
	return nil, err
}

func webTTYKeyEnvelopesFromAPIMetadata(raw json.RawMessage) ([]webtty.KeyEnvelope, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var encoded []struct {
		RecipientKeyID  string `json:"recipient_key_id"`
		EncapsulatedKey string `json:"encapsulated_key"`
		WrappedKey      string `json:"wrapped_key"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode WebTTY key envelopes: %w", err)
	}
	out := make([]webtty.KeyEnvelope, 0, len(encoded))
	for _, item := range encoded {
		recipientKeyID, err := webtty.DecodeE2EKeyMaterial(item.RecipientKeyID, webtty.E2EPayloadKeyIDSize, "WebTTY recipient key id")
		if err != nil {
			return nil, err
		}
		encapsulatedKey, err := decodeOptionalWebTTYKeyMaterial(item.EncapsulatedKey, "WebTTY encapsulated key")
		if err != nil {
			return nil, err
		}
		wrappedKey, err := decodeOptionalWebTTYKeyMaterial(item.WrappedKey, "WebTTY wrapped key")
		if err != nil {
			return nil, err
		}
		out = append(out, webtty.KeyEnvelope{
			RecipientKeyID:  recipientKeyID,
			EncapsulatedKey: encapsulatedKey,
			WrappedKey:      wrappedKey,
		})
	}
	return out, nil
}

func decodeOptionalWebTTYKeyMaterial(value string, field string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return webtty.DecodeE2EKeyMaterial(value, 0, field)
}

func webTTYParticipantStreamURL(client *rstream.Client, sessionID string, participantID string) (string, error) {
	if client == nil || client.EngineURL == nil || strings.TrimSpace(*client.EngineURL) == "" {
		return "", fmt.Errorf("engine URL is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("WebTTY session ID is required")
	}
	participantID = strings.TrimSpace(participantID)
	if participantID == "" {
		return "", fmt.Errorf("WebTTY participant ID is required")
	}
	engine := strings.TrimSpace(*client.EngineURL)
	host := engine
	if strings.Contains(engine, "://") {
		parsed, err := url.Parse(engine)
		if err != nil {
			return "", fmt.Errorf("invalid engine URL: %w", err)
		}
		host = parsed.Host
	}
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("engine host is required")
	}
	u := url.URL{
		Scheme: "wss",
		Host:   host,
	}
	u.Path = "/api/webtty/sessions/" + sessionID + "/participants/" + participantID + "/stream"
	u.RawPath = "/api/webtty/sessions/" + url.PathEscape(sessionID) + "/participants/" + url.PathEscape(participantID) + "/stream"
	return u.String(), nil
}

func webTTYClientAuthToken(client *rstream.Client) *string {
	if client == nil || client.Token == nil {
		return nil
	}
	token := strings.TrimSpace(*client.Token)
	if token == "" {
		return nil
	}
	return &token
}

func newWebTTYEngineDialTLSContext(client *rstream.Client) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _ string, addr string) (net.Conn, error) {
		if client == nil {
			return nil, fmt.Errorf("rstream client is required")
		}
		return client.DialEngineHTTP1(ctx, addr)
	}
}

func webTTYAttachConfigFromParticipant(session *rstream.WebTTYSession, participant *rstream.WebTTYParticipant, capabilities []webtty.AttachCapability) *webtty.AttachConfig {
	if participant == nil {
		return nil
	}
	cfg := &webtty.AttachConfig{
		SessionID:     participant.SessionID,
		ParticipantID: participant.ID,
		AttachGrant:   append([]byte(nil), participant.AttachGrant...),
		RequestedRole: webTTYAttachRoleFromParticipant(participant.Role),
		Transport:     webtty.WebTTYTransportWebSocket,
		Capabilities:  append([]webtty.AttachCapability(nil), capabilities...),
		DeviceID:      participant.DeviceID,
		BrowserID:     participant.BrowserID,
	}
	if session != nil {
		cfg.WorkspaceID = strings.TrimSpace(session.WorkspaceID)
		cfg.ProjectID = strings.TrimSpace(session.ProjectID)
		cfg.ServerID = strings.TrimSpace(session.ServerID)
	}
	return cfg
}

func webTTYAttachRoleFromParticipant(role rstream.WebTTYParticipantRole) webtty.AttachRole {
	switch role {
	case rstream.WebTTYParticipantRoleController:
		return webtty.AttachRoleController
	default:
		return webtty.AttachRoleSpectator
	}
}

type webTTYTerminalSegmentKind int

const (
	webTTYTerminalSegmentText webTTYTerminalSegmentKind = iota
	webTTYTerminalSegmentEnterAlternate
	webTTYTerminalSegmentLeaveAlternate
)

type webTTYTerminalTextSegment struct {
	kind  webTTYTerminalSegmentKind
	value string
}

type webTTYTerminalTextBuffer struct {
	col      int
	lines    [][]rune
	row      int
	savedCol int
	savedRow int
}

func webTTYTerminalTextSegments(value string) []webTTYTerminalTextSegment {
	out := []webTTYTerminalTextSegment{}
	offset := 0
	for offset < len(value) {
		idx := strings.Index(value[offset:], "\x1b[?")
		if idx < 0 {
			break
		}
		idx += offset
		end := idx + len("\x1b[?")
		for end < len(value) && ((value[end] >= '0' && value[end] <= '9') || value[end] == ';' || value[end] == ':') {
			end++
		}
		if end >= len(value) || (value[end] != 'h' && value[end] != 'l') {
			offset = idx + 2
			continue
		}
		modes := value[idx+len("\x1b[?") : end]
		if !webTTYContainsAlternateScreenMode(modes) {
			offset = end + 1
			continue
		}
		if idx > offset {
			out = append(out, webTTYTerminalTextSegment{kind: webTTYTerminalSegmentText, value: value[offset:idx]})
		}
		if value[end] == 'h' {
			out = append(out, webTTYTerminalTextSegment{kind: webTTYTerminalSegmentEnterAlternate})
		} else {
			out = append(out, webTTYTerminalTextSegment{kind: webTTYTerminalSegmentLeaveAlternate})
		}
		offset = end + 1
	}
	if offset < len(value) {
		out = append(out, webTTYTerminalTextSegment{kind: webTTYTerminalSegmentText, value: value[offset:]})
	}
	if len(out) == 0 {
		out = append(out, webTTYTerminalTextSegment{kind: webTTYTerminalSegmentText, value: value})
	}
	return out
}

func webTTYContainsAlternateScreenMode(value string) bool {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ':' }) {
		if part == "47" || part == "1047" || part == "1049" {
			return true
		}
	}
	return false
}

func (b *webTTYTerminalTextBuffer) Write(value string) {
	if b == nil {
		return
	}
	for i := 0; i < len(value); {
		if value[i] == '\x1b' {
			i = b.consumeEscape(value, i) + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if r == '\r' {
			b.col = 0
			i += size
			continue
		}
		if r == '\n' {
			b.row++
			b.col = 0
			b.ensureRow()
			i += size
			continue
		}
		if r == '\b' {
			if b.col > 0 {
				b.col--
			}
			i += size
			continue
		}
		if r == '\t' {
			spaces := 8 - (b.col % 8)
			for j := 0; j < spaces; j++ {
				b.writePrintable(' ')
			}
			i += size
			continue
		}
		if r >= ' ' {
			b.writePrintable(r)
		}
		i += size
	}
}

func (b *webTTYTerminalTextBuffer) String() string {
	if b == nil {
		return ""
	}
	var out strings.Builder
	for i, line := range b.lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(string(line))
	}
	return out.String()
}

func (b *webTTYTerminalTextBuffer) consumeEscape(value string, offset int) int {
	if offset+1 >= len(value) {
		return offset
	}
	switch value[offset+1] {
	case '[':
		end := webTTYFindCSIEnd(value, offset+2)
		if end < 0 {
			return len(value) - 1
		}
		b.applyCSI(value[offset+2:end], value[end])
		return end
	case ']':
		return webTTYFindOSCEnd(value, offset+2)
	default:
		return offset + 1
	}
}

func webTTYFindCSIEnd(value string, offset int) int {
	for i := offset; i < len(value); i++ {
		if value[i] >= 0x40 && value[i] <= 0x7e {
			return i
		}
	}
	return -1
}

func webTTYFindOSCEnd(value string, offset int) int {
	for i := offset; i < len(value); i++ {
		if value[i] == '\a' {
			return i
		}
		if value[i] == '\x1b' && i+1 < len(value) && value[i+1] == '\\' {
			return i + 1
		}
	}
	return len(value) - 1
}

func (b *webTTYTerminalTextBuffer) applyCSI(rawParams string, final byte) {
	if strings.HasPrefix(rawParams, "?") {
		return
	}
	params := webTTYParseCSIParams(rawParams)
	first := 0
	if len(params) > 0 {
		first = params[0]
	}
	switch final {
	case 'A':
		b.row = max(0, b.row-max(first, 1))
	case 'B':
		b.row += max(first, 1)
		b.ensureRow()
	case 'C':
		b.col += max(first, 1)
	case 'D':
		b.col = max(0, b.col-max(first, 1))
	case 'G':
		b.col = max(0, max(first, 1)-1)
	case 'H', 'f':
		row := 1
		col := 1
		if len(params) > 0 {
			row = max(params[0], 1)
		}
		if len(params) > 1 {
			col = max(params[1], 1)
		}
		b.row = row - 1
		b.col = col - 1
		b.ensureRow()
	case 'J':
		b.eraseDisplay(first)
	case 'K':
		b.eraseLine(first)
	case 's':
		b.savedRow = b.row
		b.savedCol = b.col
	case 'u':
		b.row = b.savedRow
		b.col = b.savedCol
		b.ensureRow()
	}
}

func webTTYParseCSIParams(value string) []int {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ':' })
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil {
			parsed = 0
		}
		out = append(out, parsed)
	}
	return out
}

func (b *webTTYTerminalTextBuffer) eraseDisplay(mode int) {
	b.ensureRow()
	if mode == 2 || mode == 3 {
		b.lines = [][]rune{{}}
		b.row = 0
		b.col = 0
		return
	}
	if mode == 0 {
		line := b.lines[b.row]
		if b.col < len(line) {
			b.lines[b.row] = line[:b.col]
		}
		b.lines = b.lines[:b.row+1]
		return
	}
	if mode == 1 {
		for i := 0; i < b.row; i++ {
			b.lines[i] = nil
		}
		line := b.lines[b.row]
		out := make([]rune, b.col)
		copy(out, line[:min(b.col, len(line))])
		if b.col < len(line) {
			out = append(out, line[b.col:]...)
		}
		b.lines[b.row] = out
	}
}

func (b *webTTYTerminalTextBuffer) eraseLine(mode int) {
	b.ensureRow()
	line := b.lines[b.row]
	if mode == 2 {
		b.lines[b.row] = nil
		b.col = 0
		return
	}
	if mode == 1 {
		out := make([]rune, b.col)
		copy(out, line[:min(b.col, len(line))])
		if b.col < len(line) {
			out = append(out, line[b.col:]...)
		}
		b.lines[b.row] = out
		return
	}
	if b.col < len(line) {
		b.lines[b.row] = line[:b.col]
	}
}

func (b *webTTYTerminalTextBuffer) ensureRow() {
	for len(b.lines) <= b.row {
		b.lines = append(b.lines, nil)
	}
}

func (b *webTTYTerminalTextBuffer) writePrintable(r rune) {
	b.ensureRow()
	line := b.lines[b.row]
	for len(line) < b.col {
		line = append(line, ' ')
	}
	if b.col < len(line) {
		line[b.col] = r
	} else {
		line = append(line, r)
	}
	b.lines[b.row] = line
	b.col++
}

func webTTYStripANSI(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '\x1b' {
			if i+1 < len(value) && value[i+1] == '[' {
				end := webTTYFindCSIEnd(value, i+2)
				if end < 0 {
					break
				}
				i = end + 1
				continue
			}
			if i+1 < len(value) && value[i+1] == ']' {
				i = webTTYFindOSCEnd(value, i+2) + 1
				continue
			}
			if i+1 < len(value) {
				i += 2
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		out.WriteRune(r)
		i += size
	}
	return out.String()
}

func printWebTTYSessionsTable(w io.Writer, sessions *rstream.ListWebTTYSessionsResponse) error {
	list := []rstream.WebTTYSession{}
	if sessions != nil {
		list = append(list, (*sessions)...)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].StartedAt.Equal(list[j].StartedAt) {
			return list[i].StartedAt.After(list[j].StartedAt)
		}
		return list[i].ID < list[j].ID
	})
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tSERVER\tSTATUS\tMODE\tRECORDING\tENCRYPTION\tTRANSPORT\tSTARTED")
	for _, session := range list {
		transport := string(session.DownTransport)
		if session.UpTransport != "" && session.UpTransport != session.DownTransport {
			transport = transport + "->" + string(session.UpTransport)
		}
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			terminalSafeDefault(session.ID),
			terminalSafeDefault(webTTYDash(session.ServerID)),
			terminalSafeDefault(string(session.Status)),
			terminalSafeDefault(string(session.SessionMode)),
			terminalSafeDefault(string(session.RecordingMode)),
			terminalSafeDefault(string(session.EncryptionMode)),
			terminalSafeDefault(webTTYDash(transport)),
			terminalSafeDefault(webTTYTime(session.StartedAt)),
		)
	}
	return tw.Flush()
}

func printWebTTYControlRequestsTable(w io.Writer, requests *rstream.ListWebTTYControlRequestsResponse) error {
	list := []rstream.WebTTYControlRequest{}
	if requests != nil {
		list = append(list, (*requests)...)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		}
		return list[i].ID < list[j].ID
	})
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REQUEST\tSTATUS\tREQUESTER\tAPPROVER\tCREATED\tRESOLVED")
	for _, request := range list {
		resolved := "-"
		if request.ResolvedAt != nil {
			resolved = webTTYTime(*request.ResolvedAt)
		}
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			terminalSafeDefault(request.ID),
			terminalSafeDefault(string(request.Status)),
			terminalSafeDefault(webTTYDash(request.RequesterParticipantID)),
			terminalSafeDefault(webTTYDash(request.ApproverParticipantID)),
			terminalSafeDefault(webTTYTime(request.CreatedAt)),
			terminalSafeDefault(resolved),
		)
	}
	return tw.Flush()
}

func printWebTTYParticipantsTable(w io.Writer, participants *rstream.ListWebTTYParticipantsResponse) error {
	list := []rstream.WebTTYParticipant{}
	if participants != nil {
		list = append(list, (*participants)...)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].AttachedAt.Equal(list[j].AttachedAt) {
			return list[i].AttachedAt.Before(list[j].AttachedAt)
		}
		return list[i].ID < list[j].ID
	})
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PARTICIPANT\tUSER\tROLE\tCONTROLLER\tATTACHED\tDETACHED")
	for _, participant := range list {
		detached := "-"
		if participant.DetachedAt != nil {
			detached = webTTYTime(*participant.DetachedAt)
		}
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%t\t%s\t%s\n",
			terminalSafeDefault(participant.ID),
			terminalSafeDefault(webTTYDash(participant.UserID)),
			terminalSafeDefault(string(participant.Role)),
			participant.Controller,
			terminalSafeDefault(webTTYTime(participant.AttachedAt)),
			terminalSafeDefault(detached),
		)
	}
	return tw.Flush()
}

func printWebTTYJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func printWebTTYYAML(w io.Writer, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func webTTYTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func webTTYDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

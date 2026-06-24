// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func mcpWebTTYControlPlaneProject(ctx context.Context, args map[string]json.RawMessage) (*resolvedRuntime, *controlplane.Client, controlplane.Project, error) {
	runtime, err := resolveMCPControlPlaneRuntime(true)
	if err != nil {
		return nil, nil, controlplane.Project{}, err
	}
	client := controlplane.NewClient(runtime.Resolved.APIURL, runtime.Resolved.Token)
	project, err := mcpResolveRuntimeProject(ctx, client, runtime.Config, args)
	if err != nil {
		return nil, nil, controlplane.Project{}, err
	}
	return runtime, client, project, nil
}

func mcpWebTTYEngineClient(ctx context.Context, args map[string]json.RawMessage) (*resolvedRuntime, *rstream.Client, error) {
	runtime, err := resolveMCPRuntimeForArgs(ctx, args)
	if err != nil {
		return nil, nil, err
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil {
		return nil, nil, err
	}
	return runtime, client, nil
}

func mcpWebTTYServersList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	serverType, err := mcpOptionalWebTTYServerType(args, "all")
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"surface": "cli", "type": serverType}
	if serverType == "all" || serverType == "registered" {
		_, client, project, err := mcpWebTTYControlPlaneProject(ctx, args)
		if err != nil {
			if serverType == "registered" {
				return nil, err
			}
			payload["registered_error"] = err.Error()
		} else {
			registered, err := mcpListRegisteredWebTTYServers(ctx, client, project.ID, args)
			if err != nil {
				if serverType == "registered" {
					return nil, err
				}
				payload["registered_error"] = err.Error()
			} else {
				payload["registered"] = registered
			}
		}
	}
	if serverType == "all" || serverType == "lightweight" {
		_, client, err := mcpWebTTYEngineClient(ctx, args)
		if err != nil {
			if serverType == "lightweight" {
				return nil, err
			}
			payload["lightweight_error"] = err.Error()
		} else {
			filter, nameFilter, err := mcpWebTTYFilterArgs(args)
			if err != nil {
				return nil, err
			}
			servers, err := listWebTTYServers(ctx, client, filter)
			if err != nil {
				if serverType == "lightweight" {
					return nil, err
				}
				payload["lightweight_error"] = err.Error()
			} else {
				payload["lightweight"] = filterMCPWebTTYServers(servers, nameFilter)
			}
		}
	}
	return mcpJSONResult(payload, false)
}

func mcpWebTTYServerGet(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	serverType, err := mcpOptionalWebTTYServerType(args, "")
	if err != nil {
		return nil, err
	}
	serverID, err := mcpOptionalStringArg(args, "server_id", "")
	if err != nil {
		return nil, err
	}
	if serverType == "registered" || strings.TrimSpace(serverID) != "" {
		_, client, project, err := mcpWebTTYControlPlaneProject(ctx, args)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(serverID) == "" {
			name, err := mcpRequiredStringArg(args, "name")
			if err != nil {
				return nil, err
			}
			server, err := mcpFindRegisteredWebTTYServer(ctx, client, project.ID, name)
			if err != nil {
				return nil, err
			}
			return mcpJSONResult(map[string]any{"surface": "cli", "type": "registered", "server": server}, false)
		}
		server, err := client.GetWebTTYServer(ctx, project.ID, strings.TrimSpace(serverID))
		if err != nil {
			return nil, mapWebTTYServerReadError(err)
		}
		return mcpJSONResult(map[string]any{"surface": "cli", "type": "registered", "server": server}, false)
	}
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	filter, nameFilter, err := mcpWebTTYFilterArgs(args)
	if err != nil {
		return nil, err
	}
	tunnelID, err := mcpOptionalStringArg(args, "tunnel_id", "")
	if err != nil {
		return nil, err
	}
	if nameFilter == "" && tunnelID == "" {
		name, _ := mcpOptionalStringArg(args, "name", "")
		nameFilter = name
	}
	servers, err := listWebTTYServers(ctx, client, filter)
	if err != nil {
		return nil, err
	}
	servers = mcpFilterLightweightWebTTYServers(servers, nameFilter, tunnelID)
	if len(servers) == 0 {
		return nil, fmt.Errorf("no lightweight WebTTY server matches the requested selector")
	}
	if len(servers) > 1 {
		return nil, fmt.Errorf("multiple lightweight WebTTY servers match the requested selector; refine name or tunnel_id")
	}
	return mcpJSONResult(map[string]any{"surface": "cli", "type": "lightweight", "server": servers[0]}, false)
}

func mcpWebTTYServerCreate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, client, project, err := mcpWebTTYControlPlaneProject(ctx, args)
	if err != nil {
		return nil, err
	}
	name, err := mcpRequiredStringArg(args, "name")
	if err != nil {
		return nil, err
	}
	description, err := mcpOptionalStringPtrArg(args, "description")
	if err != nil {
		return nil, err
	}
	recordingPolicy, err := mcpOptionalStringArg(args, "recording_policy", webTTYServerRecordingPolicyRecorded)
	if err != nil {
		return nil, err
	}
	encryptionPolicy, err := mcpOptionalStringArg(args, "encryption_policy", webTTYServerEncryptionPolicyDisabled)
	if err != nil {
		return nil, err
	}
	accessPolicy, err := mcpOptionalStringArg(args, "access_policy", webTTYServerAccessPolicyProjectMembers)
	if err != nil {
		return nil, err
	}
	labels, err := mcpOptionalLabelMap(args)
	if err != nil {
		return nil, err
	}
	request := controlplane.CreateWebTTYServerRequest{
		Name:             strings.TrimSpace(name),
		Description:      description,
		RecordingPolicy:  strings.TrimSpace(recordingPolicy),
		EncryptionPolicy: strings.TrimSpace(encryptionPolicy),
		AccessPolicy:     strings.TrimSpace(accessPolicy),
		Labels:           nilIfEmptyStringMap(labels),
	}
	response, err := client.CreateWebTTYServer(ctx, project.ID, request)
	if err != nil {
		return nil, mapWebTTYServerCreateError(err, project.ID, name)
	}
	projectRef := webTTYRegisteredServerProject{ID: project.ID, Source: "mcp", ProjectEndpoint: project.Endpoint, WorkspaceID: project.WorkspaceID}
	return mcpJSONResult(map[string]any{
		"surface": "cli",
		"server":  response.Server,
		"commands": map[string]any{
			"enroll": webTTYServerEnrollCommand(runtime, project.ID, response.Server.ID),
			"run":    "rstream webtty server -v --server-id " + response.Server.ID + " --login-user <os-user>",
		},
		"project": projectRef,
	}, false)
}

func mcpWebTTYServerUpdate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, project, err := mcpWebTTYControlPlaneProject(ctx, args)
	if err != nil {
		return nil, err
	}
	serverID, err := mcpRequiredStringArg(args, "server_id")
	if err != nil {
		return nil, err
	}
	request := controlplane.UpdateWebTTYServerRequest{}
	request.Name, err = mcpOptionalStringPtrArg(args, "name")
	if err != nil {
		return nil, err
	}
	request.Description, err = mcpOptionalStringPtrArg(args, "description")
	if err != nil {
		return nil, err
	}
	request.Status, err = mcpOptionalStringPtrArg(args, "status")
	if err != nil {
		return nil, err
	}
	request.RecordingPolicy, err = mcpOptionalStringPtrArg(args, "recording_policy")
	if err != nil {
		return nil, err
	}
	request.AccessPolicy, err = mcpOptionalStringPtrArg(args, "access_policy")
	if err != nil {
		return nil, err
	}
	if _, ok := args["labels"]; ok {
		labels, err := mcpOptionalLabelMap(args)
		if err != nil {
			return nil, err
		}
		request.Labels = labels
	}
	if request.Name == nil && request.Description == nil && request.Status == nil && request.RecordingPolicy == nil && request.AccessPolicy == nil && request.Labels == nil {
		return nil, fmt.Errorf("no registered WebTTY server fields were provided to update")
	}
	server, err := client.UpdateWebTTYServer(ctx, project.ID, strings.TrimSpace(serverID), request)
	if err != nil {
		return nil, mapWebTTYServerWriteError(err)
	}
	return mcpJSONResult(map[string]any{"surface": "cli", "server": server}, false)
}

func mcpWebTTYServerDelete(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, project, err := mcpWebTTYControlPlaneProject(ctx, args)
	if err != nil {
		return nil, err
	}
	serverID, err := mcpRequiredStringArg(args, "server_id")
	if err != nil {
		return nil, err
	}
	serverID = strings.TrimSpace(serverID)
	if err := client.DeleteWebTTYServer(ctx, project.ID, serverID); err != nil {
		return nil, mapWebTTYServerWriteError(err)
	}
	return mcpJSONResult(map[string]any{"deleted": true, "server_id": serverID, "project_id": project.ID}, false)
}

func mcpWebTTYServerEnrollmentGet(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, client, project, err := mcpWebTTYControlPlaneProject(ctx, args)
	if err != nil {
		return nil, err
	}
	serverID, err := mcpRequiredStringArg(args, "server_id")
	if err != nil {
		return nil, err
	}
	server, err := client.GetWebTTYServer(ctx, project.ID, strings.TrimSpace(serverID))
	if err != nil {
		return nil, mapWebTTYServerReadError(err)
	}
	response := controlplane.CreateWebTTYServerResponse{Server: server}
	projectRef := webTTYRegisteredServerProject{ID: project.ID, Source: "mcp", ProjectEndpoint: project.Endpoint, WorkspaceID: project.WorkspaceID}
	return mcpJSONResult(map[string]any{
		"surface":  "cli",
		"server":   server,
		"commands": webTTYServerEnrollmentCommandOutput(runtime, projectRef, response),
	}, false)
}

func mcpWebTTYSessionsList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	params, err := mcpWebTTYSessionsListParams(args)
	if err != nil {
		return nil, err
	}
	sessions, err := client.ListWebTTYSessions(ctx, params)
	if err != nil {
		return nil, err
	}
	if sessions == nil {
		return mcpJSONResult(map[string]any{"sessions": []rstream.WebTTYSession{}}, false)
	}
	return mcpJSONResult(map[string]any{"sessions": *sessions}, false)
}

func mcpWebTTYSessionGet(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	session, err := client.GetWebTTYSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"session": session}, false)
}

func mcpWebTTYSessionEvents(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	params, err := mcpWebTTYSessionEventsParams(args)
	if err != nil {
		return nil, err
	}
	events, err := client.ListWebTTYSessionEvents(ctx, sessionID, params)
	if err != nil {
		return nil, err
	}
	if events == nil {
		return mcpJSONResult(map[string]any{"events": []rstream.WebTTYSessionEvent{}}, false)
	}
	return mcpJSONResult(map[string]any{"events": *events}, false)
}

func mcpWebTTYSessionExport(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	runtime, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	options, err := mcpWebTTYSessionExportOptions(args)
	if err != nil {
		return nil, err
	}
	session, err := client.GetWebTTYSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to read WebTTY session: %w", err)
	}
	events, err := webTTYReadAllSessionEvents(ctx, client, sessionID, options.FromSeq, options.MaxEvents)
	if err != nil {
		return nil, err
	}
	attachCrypto, err := webTTYSessionAttachPayloadCrypto(ctx, runtime, client, session)
	if err != nil {
		return nil, err
	}
	exported, err := webTTYDecryptSessionExportEvents(ctx, client, session, events, webTTYSessionAttachPayloadCryptoValue(attachCrypto))
	if err != nil {
		return nil, err
	}
	switch options.Format {
	case webTTYSessionExportFormatText:
		textExport := webTTYRenderSessionTextExport(events, exported, options)
		return mcpJSONResult(map[string]any{
			"format":                    "text",
			"session_id":                sessionID,
			"text":                      textExport.Text,
			"warnings":                  textExport.Warnings,
			"detected_alternate_screen": textExport.DetectedAlternateScreen,
		}, false)
	case webTTYSessionExportFormatJSON:
		return mcpJSONResult(map[string]any{
			"format": "raw",
			"export": webTTYSessionJSONExport{
				ExportVersion: 1,
				GeneratedAt:   time.Now().UTC(),
				Session:       session,
				Events:        exported,
			},
		}, false)
	default:
		return nil, validateOutputMode(string(options.Format), string(webTTYSessionExportFormatText), "raw")
	}
}

func mcpWebTTYSessionParticipants(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	participants, err := client.ListWebTTYParticipants(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if participants == nil {
		return mcpJSONResult(map[string]any{"participants": []rstream.WebTTYParticipant{}}, false)
	}
	return mcpJSONResult(map[string]any{"participants": *participants}, false)
}

func mcpWebTTYControlRequestsList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	params, err := mcpWebTTYControlRequestsParams(args)
	if err != nil {
		return nil, err
	}
	requests, err := client.ListWebTTYControlRequests(ctx, sessionID, params)
	if err != nil {
		return nil, err
	}
	if requests == nil {
		return mcpJSONResult(map[string]any{"control_requests": []rstream.WebTTYControlRequest{}}, false)
	}
	return mcpJSONResult(map[string]any{"control_requests": *requests}, false)
}

func mcpWebTTYControlRequestCreate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	request, err := mcpWebTTYCreateControlRequest(args)
	if err != nil {
		return nil, err
	}
	created, err := client.CreateWebTTYControlRequest(ctx, sessionID, request)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"control_request": created}, false)
}

func mcpWebTTYControlRequestResolve(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	_, client, err := mcpWebTTYEngineClient(ctx, args)
	if err != nil {
		return nil, err
	}
	sessionID, err := mcpRequiredStringArg(args, "session_id")
	if err != nil {
		return nil, err
	}
	requestID, err := mcpRequiredStringArg(args, "request_id")
	if err != nil {
		return nil, err
	}
	action, err := mcpRequiredStringArg(args, "action")
	if err != nil {
		return nil, err
	}
	approverParticipantID, err := mcpOptionalStringArg(args, "approver_participant_id", "")
	if err != nil {
		return nil, err
	}
	reason, err := mcpOptionalStringArg(args, "reason", "")
	if err != nil {
		return nil, err
	}
	resolved, err := client.ResolveWebTTYControlRequest(ctx, sessionID, requestID, rstream.ResolveWebTTYControlRequest{
		Action:                strings.TrimSpace(action),
		ApproverParticipantID: strings.TrimSpace(approverParticipantID),
		Reason:                strings.TrimSpace(reason),
	})
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(map[string]any{"control_request": resolved}, false)
}

func mcpListRegisteredWebTTYServers(ctx context.Context, client *controlplane.Client, projectID string, args map[string]json.RawMessage) (controlplane.ListWebTTYServersResponse, error) {
	params := controlplane.ListWebTTYServersParams{}
	var err error
	params.Query, err = mcpOptionalStringArg(args, "q", "")
	if err != nil {
		return controlplane.ListWebTTYServersResponse{}, err
	}
	params.Status, err = mcpOptionalStringArg(args, "status", "")
	if err != nil {
		return controlplane.ListWebTTYServersResponse{}, err
	}
	params.Page, err = mcpOptionalIntArg(args, "page")
	if err != nil {
		return controlplane.ListWebTTYServersResponse{}, err
	}
	params.PageSize, err = mcpOptionalIntArg(args, "page_size")
	if err != nil {
		return controlplane.ListWebTTYServersResponse{}, err
	}
	response, err := client.ListWebTTYServers(ctx, projectID, params)
	if err != nil {
		return controlplane.ListWebTTYServersResponse{}, mapWebTTYServerReadError(err)
	}
	return response, nil
}

func mcpFindRegisteredWebTTYServer(ctx context.Context, client *controlplane.Client, projectID string, name string) (controlplane.WebTTYServer, error) {
	pageSize := 100
	response, err := client.ListWebTTYServers(ctx, projectID, controlplane.ListWebTTYServersParams{Query: strings.TrimSpace(name), PageSize: &pageSize})
	if err != nil {
		return controlplane.WebTTYServer{}, mapWebTTYServerReadError(err)
	}
	matches := make([]controlplane.WebTTYServer, 0, 1)
	for _, server := range response.Servers {
		if strings.EqualFold(strings.TrimSpace(server.Name), strings.TrimSpace(name)) || strings.TrimSpace(server.ID) == strings.TrimSpace(name) {
			matches = append(matches, server)
		}
	}
	if len(matches) == 0 {
		return controlplane.WebTTYServer{}, fmt.Errorf("no registered WebTTY server matches %q", name)
	}
	if len(matches) > 1 {
		return controlplane.WebTTYServer{}, fmt.Errorf("multiple registered WebTTY servers match %q; use server_id", name)
	}
	return matches[0], nil
}

func mcpFilterLightweightWebTTYServers(servers []webtty.ServerInfo, nameFilter string, tunnelID string) []webtty.ServerInfo {
	servers = filterMCPWebTTYServers(servers, nameFilter)
	tunnelID = strings.TrimSpace(tunnelID)
	if tunnelID == "" {
		return servers
	}
	filtered := make([]webtty.ServerInfo, 0, len(servers))
	for _, server := range servers {
		if server.TunnelID == tunnelID {
			filtered = append(filtered, server)
		}
	}
	return filtered
}

func mcpOptionalWebTTYServerType(args map[string]json.RawMessage, fallback string) (string, error) {
	value, err := mcpOptionalStringArg(args, "type", fallback)
	if err != nil {
		return "", err
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	switch value {
	case "all", "lightweight", "registered":
		return value, nil
	default:
		return "", fmt.Errorf("invalid WebTTY server type %q (valid: all, lightweight, registered)", value)
	}
}

func mcpWebTTYSessionsListParams(args map[string]json.RawMessage) (*rstream.ListWebTTYSessionsParams, error) {
	params := &rstream.ListWebTTYSessionsParams{}
	var err error
	params.Limit, err = mcpOptionalIntArg(args, "limit")
	if err != nil {
		return nil, err
	}
	filters := &rstream.ListWebTTYSessionsFilters{}
	if filters.ServerID, err = mcpOptionalStringPtrArg(args, "server_id"); err != nil {
		return nil, err
	}
	if filters.TunnelID, err = mcpOptionalStringPtrArg(args, "tunnel_id"); err != nil {
		return nil, err
	}
	if filters.UserID, err = mcpOptionalStringPtrArg(args, "user_id"); err != nil {
		return nil, err
	}
	if filters.GroupID, err = mcpOptionalStringPtrArg(args, "group_id"); err != nil {
		return nil, err
	}
	if filters.Origin, err = mcpOptionalStringPtrArg(args, "origin"); err != nil {
		return nil, err
	}
	if filters.Status, err = mcpOptionalStringPtrArg(args, "status"); err != nil {
		return nil, err
	}
	if filters.StartedFrom, err = mcpOptionalTimePtrArg(args, "started_after"); err != nil {
		return nil, err
	}
	if filters.StartedTo, err = mcpOptionalTimePtrArg(args, "started_before"); err != nil {
		return nil, err
	}
	if filters.ServerID != nil || filters.TunnelID != nil || filters.UserID != nil || filters.GroupID != nil || filters.Origin != nil || filters.Status != nil || filters.StartedFrom != nil || filters.StartedTo != nil {
		params.Filters = filters
	}
	return params, nil
}

func mcpWebTTYSessionEventsParams(args map[string]json.RawMessage) (*rstream.ListWebTTYSessionEventsParams, error) {
	params := &rstream.ListWebTTYSessionEventsParams{}
	var err error
	params.FromSeq, err = mcpOptionalStringPtrArg(args, "from_seq")
	if err != nil {
		return nil, err
	}
	params.Limit, err = mcpOptionalIntArg(args, "limit")
	if err != nil {
		return nil, err
	}
	return params, nil
}

func mcpWebTTYSessionExportOptions(args map[string]json.RawMessage) (webTTYSessionExportOptions, error) {
	format, err := mcpOptionalStringArg(args, "format", string(webTTYSessionExportFormatText))
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "raw" {
		format = string(webTTYSessionExportFormatJSON)
	}
	if format != string(webTTYSessionExportFormatText) && format != string(webTTYSessionExportFormatJSON) {
		return webTTYSessionExportOptions{}, validateOutputMode(format, string(webTTYSessionExportFormatText), "raw")
	}
	fromSeqText, err := mcpOptionalStringArg(args, "from_seq", "")
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	fromSeq, err := webTTYParseSequenceCursor(fromSeqText, "from_seq")
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	maxEvents, err := mcpOptionalIntArg(args, "max_events")
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	maxEventsValue := 0
	if maxEvents != nil {
		if *maxEvents < 0 {
			return webTTYSessionExportOptions{}, fmt.Errorf("max_events must be zero or greater")
		}
		maxEventsValue = *maxEvents
	}
	includeStdin, err := mcpOptionalBoolArg(args, "include_stdin", false)
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	includeStdout, err := mcpOptionalBoolArg(args, "include_stdout", true)
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	includeStderr, err := mcpOptionalBoolArg(args, "include_stderr", true)
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	includeTimestamps, err := mcpOptionalBoolArg(args, "include_timestamps", false)
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	includeResizeMarkers, err := mcpOptionalBoolArg(args, "include_resize_markers", false)
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	includeTerminalModes, err := mcpOptionalBoolArg(args, "terminal_mode_markers", true)
	if err != nil {
		return webTTYSessionExportOptions{}, err
	}
	return webTTYSessionExportOptions{
		ActiveAlternateScreen: true,
		Format:                webTTYSessionExportFormat(format),
		FromSeq:               fromSeq,
		IncludeResizeMarkers:  includeResizeMarkers,
		IncludeStderr:         includeStderr,
		IncludeStdin:          includeStdin,
		IncludeStdout:         includeStdout,
		IncludeTerminalModes:  includeTerminalModes,
		IncludeTimestamps:     includeTimestamps,
		MaxEvents:             maxEventsValue,
	}, nil
}

func mcpWebTTYControlRequestsParams(args map[string]json.RawMessage) (*rstream.ListWebTTYControlRequestsParams, error) {
	params := &rstream.ListWebTTYControlRequestsParams{}
	var err error
	params.Limit, err = mcpOptionalIntArg(args, "limit")
	if err != nil {
		return nil, err
	}
	filters := &rstream.ListWebTTYControlRequestsFilters{}
	if filters.Status, err = mcpOptionalStringPtrArg(args, "status"); err != nil {
		return nil, err
	}
	if filters.RequesterUserID, err = mcpOptionalStringPtrArg(args, "requester_user_id"); err != nil {
		return nil, err
	}
	if filters.Status != nil || filters.RequesterUserID != nil {
		params.Filters = filters
	}
	return params, nil
}

func mcpWebTTYCreateControlRequest(args map[string]json.RawMessage) (rstream.CreateWebTTYControlRequest, error) {
	participantID, err := mcpRequiredStringArg(args, "participant_id")
	if err != nil {
		return rstream.CreateWebTTYControlRequest{}, err
	}
	reason, err := mcpOptionalStringArg(args, "reason", "")
	if err != nil {
		return rstream.CreateWebTTYControlRequest{}, err
	}
	expiresAt, err := mcpOptionalTimePtrArg(args, "expires_at")
	if err != nil {
		return rstream.CreateWebTTYControlRequest{}, err
	}
	return rstream.CreateWebTTYControlRequest{
		ParticipantID: strings.TrimSpace(participantID),
		Reason:        strings.TrimSpace(reason),
		ExpiresAt:     expiresAt,
	}, nil
}

func mcpOptionalTimePtrArg(args map[string]json.RawMessage, name string) (*time.Time, error) {
	value, err := mcpOptionalStringArg(args, name, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("argument %q must be RFC3339: %w", name, err)
	}
	return &parsed, nil
}

func nilIfEmptyStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	return value
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

func TestWebTTYSessionsListParamsFromFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("server-id", "", "")
	cmd.Flags().String("tunnel-id", "", "")
	cmd.Flags().String("user-id", "", "")
	cmd.Flags().String("group-id", "", "")
	cmd.Flags().String("origin", "", "")
	cmd.Flags().String("status", "", "")
	cmd.Flags().String("started-from", "", "")
	cmd.Flags().String("started-to", "", "")
	cmd.Flags().Int("limit", 0, "")
	if err := cmd.Flags().Set("server-id", " prod-shell "); err != nil {
		t.Fatalf("set server-id: %v", err)
	}
	if err := cmd.Flags().Set("origin", "codex"); err != nil {
		t.Fatalf("set origin: %v", err)
	}
	if err := cmd.Flags().Set("status", "active"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := cmd.Flags().Set("limit", "25"); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	if err := cmd.Flags().Set("started-from", "2026-06-08T10:00:00Z"); err != nil {
		t.Fatalf("set started-from: %v", err)
	}
	if err := cmd.Flags().Set("started-to", "2026-06-08T11:00:00Z"); err != nil {
		t.Fatalf("set started-to: %v", err)
	}
	params, err := webTTYSessionsListParamsFromFlags(cmd)
	if err != nil {
		t.Fatalf("webTTYSessionsListParamsFromFlags() error = %v", err)
	}
	if params.Limit == nil || *params.Limit != 25 {
		t.Fatalf("limit = %#v", params.Limit)
	}
	if params.Filters == nil || params.Filters.ServerID == nil || *params.Filters.ServerID != "prod-shell" {
		t.Fatalf("server filter = %#v", params.Filters)
	}
	if params.Filters.Origin == nil || *params.Filters.Origin != "codex" || params.Filters.Status == nil || *params.Filters.Status != "active" {
		t.Fatalf("origin/status filter = %#v", params.Filters)
	}
	if params.Filters.StartedFrom == nil || params.Filters.StartedFrom.UTC().Format(time.RFC3339) != "2026-06-08T10:00:00Z" {
		t.Fatalf("started_from = %#v", params.Filters.StartedFrom)
	}
	if params.Filters.StartedTo == nil || params.Filters.StartedTo.UTC().Format(time.RFC3339) != "2026-06-08T11:00:00Z" {
		t.Fatalf("started_to = %#v", params.Filters.StartedTo)
	}
}

func TestWebTTYSessionsExposeJoinCommand(t *testing.T) {
	var hasJoin bool
	for _, child := range webttySessionsCmd.Commands() {
		switch child.Name() {
		case "join":
			hasJoin = true
		case "attach":
			t.Fatalf("sessions attach must not be exposed as a public command")
		}
	}
	if !hasJoin {
		t.Fatalf("sessions join command is not registered")
	}
}

func TestWebTTYSessionsHelpSeparatesPrimaryAndAdvancedCommands(t *testing.T) {
	var out bytes.Buffer
	webttySessionsCmd.SetOut(&out)
	defer webttySessionsCmd.SetOut(nil)
	if err := webttySessionsCmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Session Commands:") || !strings.Contains(text, "Advanced Commands:") {
		t.Fatalf("sessions help does not group commands: %s", text)
	}
	if strings.Index(text, "Session Commands:") > strings.Index(text, "Advanced Commands:") {
		t.Fatalf("primary commands should be shown before advanced commands: %s", text)
	}
	if strings.Contains(text, "Aliases:") || strings.Contains(text, "ls") {
		t.Fatalf("sessions help should not expose command aliases: %s", text)
	}
}

func TestWebTTYSessionsParamsRejectNegativeLimit(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("server-id", "", "")
	cmd.Flags().String("tunnel-id", "", "")
	cmd.Flags().String("user-id", "", "")
	cmd.Flags().String("group-id", "", "")
	cmd.Flags().String("origin", "", "")
	cmd.Flags().String("status", "", "")
	cmd.Flags().String("started-from", "", "")
	cmd.Flags().String("started-to", "", "")
	cmd.Flags().Int("limit", 0, "")
	if err := cmd.Flags().Set("limit", "-1"); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	if _, err := webTTYSessionsListParamsFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("expected limit error, got %v", err)
	}
}

func TestWebTTYSessionsListParamsRejectStartedRange(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("server-id", "", "")
	cmd.Flags().String("tunnel-id", "", "")
	cmd.Flags().String("user-id", "", "")
	cmd.Flags().String("group-id", "", "")
	cmd.Flags().String("origin", "", "")
	cmd.Flags().String("status", "", "")
	cmd.Flags().String("started-from", "", "")
	cmd.Flags().String("started-to", "", "")
	cmd.Flags().Int("limit", 0, "")
	if err := cmd.Flags().Set("started-from", "2026-06-08T11:00:00Z"); err != nil {
		t.Fatalf("set started-from: %v", err)
	}
	if err := cmd.Flags().Set("started-to", "2026-06-08T10:00:00Z"); err != nil {
		t.Fatalf("set started-to: %v", err)
	}
	if _, err := webTTYSessionsListParamsFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "--started-from") {
		t.Fatalf("expected started range error, got %v", err)
	}
}

func TestWebTTYSessionEventsParamsFromFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("from-seq", "", "")
	cmd.Flags().Int("limit", 0, "")
	if err := cmd.Flags().Set("from-seq", " 42 "); err != nil {
		t.Fatalf("set from-seq: %v", err)
	}
	if err := cmd.Flags().Set("limit", "10"); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	params, err := webTTYSessionEventsParamsFromFlags(cmd)
	if err != nil {
		t.Fatalf("webTTYSessionEventsParamsFromFlags() error = %v", err)
	}
	if params.FromSeq == nil || *params.FromSeq != "42" || params.Limit == nil || *params.Limit != 10 {
		t.Fatalf("params = %#v", params)
	}
}

func TestWebTTYSessionExportOptionsFromFlags(t *testing.T) {
	cmd := newWebTTYSessionExportTestCommand()
	if err := cmd.Flags().Set("format", " json "); err != nil {
		t.Fatalf("set format: %v", err)
	}
	if err := cmd.Flags().Set("from-seq", "42"); err != nil {
		t.Fatalf("set from-seq: %v", err)
	}
	if err := cmd.Flags().Set("max-events", "100"); err != nil {
		t.Fatalf("set max-events: %v", err)
	}
	if err := cmd.Flags().Set("file", "/tmp/session.json"); err != nil {
		t.Fatalf("set file: %v", err)
	}
	options, err := webTTYSessionExportOptionsFromFlags(cmd)
	if err != nil {
		t.Fatalf("webTTYSessionExportOptionsFromFlags() error = %v", err)
	}
	if options.Format != webTTYSessionExportFormatJSON || options.FromSeq != 42 || options.MaxEvents != 100 || options.Path != "/tmp/session.json" {
		t.Fatalf("options = %#v", options)
	}
}

func TestWebTTYSessionExportOptionsRejectInvalidValues(t *testing.T) {
	cmd := newWebTTYSessionExportTestCommand()
	if err := cmd.Flags().Set("format", "csv"); err != nil {
		t.Fatalf("set format: %v", err)
	}
	if _, err := webTTYSessionExportOptionsFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("expected format error, got %v", err)
	}
	cmd = newWebTTYSessionExportTestCommand()
	if err := cmd.Flags().Set("from-seq", "-1"); err != nil {
		t.Fatalf("set from-seq: %v", err)
	}
	if _, err := webTTYSessionExportOptionsFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "--from-seq") {
		t.Fatalf("expected from-seq error, got %v", err)
	}
	cmd = newWebTTYSessionExportTestCommand()
	if err := cmd.Flags().Set("max-events", "-1"); err != nil {
		t.Fatalf("set max-events: %v", err)
	}
	if _, err := webTTYSessionExportOptionsFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "--max-events") {
		t.Fatalf("expected max-events error, got %v", err)
	}
}

func TestWebTTYSessionE2EDecryptUnavailableErrorIsSourceAware(t *testing.T) {
	session := &rstream.WebTTYSession{
		ID:          "session-1",
		WorkspaceID: "workspace-1",
	}
	workspaceErr := webTTYSessionE2EDecryptUnavailableError(session, map[string]struct{}{
		webtty.E2ERecipientKindWorkspaceDevice: {},
	}, "recording")
	if workspaceErr == nil ||
		!strings.Contains(workspaceErr.Error(), "workspace-managed") ||
		!strings.Contains(workspaceErr.Error(), "workspace device enroll") {
		t.Fatalf("workspace-managed error is not actionable: %v", workspaceErr)
	}
	explicitErr := webTTYSessionE2EDecryptUnavailableError(session, map[string]struct{}{
		webtty.E2ERecipientKindPublicKey: {},
	}, "recording")
	if explicitErr == nil ||
		!strings.Contains(explicitErr.Error(), "explicit-key") ||
		strings.Contains(explicitErr.Error(), "workspace device enroll") {
		t.Fatalf("explicit-key error is not source-aware: %v", explicitErr)
	}
}

func TestWebTTYSessionPayloadCryptoFromWorkspaceDevicesSkipsInactiveLocalDevices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name         string
		status       string
		withEnvelope bool
	}{
		{name: "revoked with envelope", status: workspaceDeviceStatusRevoked, withEnvelope: true},
		{name: "pending with envelope", status: workspaceDeviceStatusPending, withEnvelope: true},
		{name: "active without envelope", status: workspaceDeviceStatusActive, withEnvelope: false},
		{name: "empty status with envelope", status: "", withEnvelope: true},
	} {
		material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, tc.name)
		if err != nil {
			t.Fatalf("generateWorkspaceDeviceMaterial(%q) error = %v", tc.name, err)
		}
		device := material.file
		device.DeviceKeyID = strings.ReplaceAll(tc.name, " ", "-")
		device.Status = tc.status
		device.CreatedAt = now
		device.UpdatedAt = now
		if tc.withEnvelope {
			_, _, _, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
			device.DeviceEnvelope = &envelope
		}
		writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	}
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		http.Error(w, "inactive local device must not request decrypt material", http.StatusBadRequest)
	}))
	defer server.Close()
	engine := strings.TrimPrefix(server.URL, "https://")
	token := "token"
	client := &rstream.Client{
		EngineURL: &engine,
		Token:     &token,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS12,
		},
	}
	attachCrypto, err := webTTYSessionPayloadCryptoFromWorkspaceDevices(t.Context(), nil, client, &rstream.WebTTYSession{
		ID:          "session-1",
		WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatalf("webTTYSessionPayloadCryptoFromWorkspaceDevices() error = %v", err)
	}
	if attachCrypto != nil {
		t.Fatalf("expected no crypto from inactive devices, got %#v", attachCrypto)
	}
	if called {
		t.Fatal("inactive local workspace devices triggered a decrypt-material API call")
	}
}

func TestWebTTYSessionAttachPayloadCryptoExplainsTrustedDeviceWithoutSessionGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	_, _, _, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	var decryptMaterialRequested bool
	var keyGrantsRequested bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.EscapedPath()
		switch {
		case strings.HasSuffix(path, "/webtty/sessions/session-1/key-grants/decrypt-material"):
			decryptMaterialRequested = true
			_ = json.NewEncoder(w).Encode(rstream.ListWebTTYKeyGrantDecryptMaterialResponse{})
		case strings.HasSuffix(path, "/webtty/sessions/session-1/key-grants"):
			keyGrantsRequested = true
			_ = json.NewEncoder(w).Encode(rstream.ListWebTTYKeyGrantsResponse{
				{
					ID:            "grant-1",
					SessionID:     "session-1",
					RecipientID:   "other-device",
					RecipientKind: webtty.E2ERecipientKindWorkspaceDevice,
					CreatedAt:     time.Now().UTC(),
				},
			})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	engine := strings.TrimPrefix(server.URL, "https://")
	token := "token"
	client := &rstream.Client{
		EngineURL: &engine,
		Token:     &token,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS12,
		},
	}
	attachCrypto, err := webTTYSessionAttachPayloadCrypto(t.Context(), nil, client, &rstream.WebTTYSession{
		EncryptionMode: rstream.WebTTYEncryptionModeE2E,
		ID:             "session-1",
		WorkspaceID:    workspaceID,
	})
	if err == nil {
		t.Fatal("expected missing session grant error")
	}
	if attachCrypto != nil {
		t.Fatalf("expected no attach crypto, got %#v", attachCrypto)
	}
	if !strings.Contains(err.Error(), "did not receive a key grant") ||
		strings.Contains(err.Error(), "workspace device enroll") {
		t.Fatalf("error is not specific to missing session grants: %v", err)
	}
	if !decryptMaterialRequested || !keyGrantsRequested {
		t.Fatalf("decryptMaterialRequested=%v keyGrantsRequested=%v", decryptMaterialRequested, keyGrantsRequested)
	}
}

func newWebTTYSessionExportTestCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("format", "text", "")
	cmd.Flags().String("file", "", "")
	cmd.Flags().String("from-seq", "", "")
	cmd.Flags().Int("max-events", 0, "")
	cmd.Flags().Bool("include-stdin", false, "")
	cmd.Flags().Bool("include-stdout", true, "")
	cmd.Flags().Bool("include-stderr", true, "")
	cmd.Flags().Bool("include-timestamps", false, "")
	cmd.Flags().Bool("include-resize-markers", false, "")
	cmd.Flags().Bool("terminal-mode-markers", true, "")
	cmd.Flags().Bool("active-alternate-screen", true, "")
	return cmd
}

func TestWebTTYControlRequestsParamsFromFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("status", "", "")
	cmd.Flags().Int("limit", 0, "")
	if err := cmd.Flags().Set("status", " pending "); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := cmd.Flags().Set("limit", "5"); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	params, err := webTTYControlRequestsParamsFromFlags(cmd)
	if err != nil {
		t.Fatalf("webTTYControlRequestsParamsFromFlags() error = %v", err)
	}
	if params.Limit == nil || *params.Limit != 5 || params.Filters == nil || params.Filters.Status == nil || *params.Filters.Status != "pending" {
		t.Fatalf("params = %#v", params)
	}
}

func TestWebTTYResolveControlRequestFromFlagsAllowsPermissionBasedResolution(t *testing.T) {
	cmd := newWebTTYResolveControlTestCommand()
	if err := cmd.Flags().Set("action", " grant "); err != nil {
		t.Fatalf("set action: %v", err)
	}
	if err := cmd.Flags().Set("reason", " reviewed "); err != nil {
		t.Fatalf("set reason: %v", err)
	}
	params, err := webTTYResolveControlRequestFromFlags(cmd)
	if err != nil {
		t.Fatalf("webTTYResolveControlRequestFromFlags() error = %v", err)
	}
	if params.Action != "grant" || params.ApproverParticipantID != "" || params.Reason != "reviewed" {
		t.Fatalf("params = %#v", params)
	}
}

func TestWebTTYResolveControlRequestFromFlagsAcceptsControllerParticipant(t *testing.T) {
	cmd := newWebTTYResolveControlTestCommand()
	if err := cmd.Flags().Set("action", " grant "); err != nil {
		t.Fatalf("set action: %v", err)
	}
	if err := cmd.Flags().Set("approver-participant-id", " controller-1 "); err != nil {
		t.Fatalf("set approver-participant-id: %v", err)
	}
	if err := cmd.Flags().Set("reason", " reviewed "); err != nil {
		t.Fatalf("set reason: %v", err)
	}
	params, err := webTTYResolveControlRequestFromFlags(cmd)
	if err != nil {
		t.Fatalf("webTTYResolveControlRequestFromFlags() error = %v", err)
	}
	if params.Action != "grant" || params.ApproverParticipantID != "controller-1" || params.Reason != "reviewed" {
		t.Fatalf("params = %#v", params)
	}
}

func TestWebTTYResolveControlRequestFromFlagsRequiresAction(t *testing.T) {
	cmd := newWebTTYResolveControlTestCommand()
	if err := cmd.Flags().Set("approver-participant-id", "controller-1"); err != nil {
		t.Fatalf("set approver-participant-id: %v", err)
	}
	if _, err := webTTYResolveControlRequestFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "--action") {
		t.Fatalf("expected action error, got %v", err)
	}
}

func newWebTTYResolveControlTestCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("action", "", "")
	cmd.Flags().String("approver-participant-id", "", "")
	cmd.Flags().String("reason", "", "")
	return cmd
}

func TestWebTTYParticipantStreamURL(t *testing.T) {
	engine := "engine.example.com:443"
	client := &rstream.Client{EngineURL: &engine}
	got, err := webTTYParticipantStreamURL(client, "session/1", "participant 1")
	if err != nil {
		t.Fatalf("webTTYParticipantStreamURL() error = %v", err)
	}
	want := "wss://engine.example.com:443/api/webtty/sessions/session%2F1/participants/participant%201/stream"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestWebTTYParticipantStreamURLAcceptsScheme(t *testing.T) {
	engine := "https://engine.example.com"
	client := &rstream.Client{EngineURL: &engine}
	got, err := webTTYParticipantStreamURL(client, "session", "participant")
	if err != nil {
		t.Fatalf("webTTYParticipantStreamURL() error = %v", err)
	}
	want := "wss://engine.example.com/api/webtty/sessions/session/participants/participant/stream"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestWebTTYParticipantStreamURLRejectsMissingIDs(t *testing.T) {
	engine := "https://engine.example.com"
	client := &rstream.Client{EngineURL: &engine}
	if _, err := webTTYParticipantStreamURL(client, " ", "participant"); err == nil || !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("expected session ID error, got %v", err)
	}
	if _, err := webTTYParticipantStreamURL(client, "session", " "); err == nil || !strings.Contains(err.Error(), "participant ID") {
		t.Fatalf("expected participant ID error, got %v", err)
	}
}

func TestWebTTYReadyFileReaderWaitsForReadyAndPreservesFD(t *testing.T) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer readFile.Close()
	defer writeFile.Close()
	ready := make(chan struct{})
	reader := &webTTYReadyFileReader{ctx: t.Context(), file: readFile, ready: ready}
	if reader.Fd() != readFile.Fd() {
		t.Fatalf("Fd() = %d, want %d", reader.Fd(), readFile.Fd())
	}
	done := make(chan struct {
		n   int
		err error
		buf []byte
	}, 1)
	go func() {
		buf := make([]byte, 5)
		n, err := reader.Read(buf)
		done <- struct {
			n   int
			err error
			buf []byte
		}{n: n, err: err, buf: buf}
	}()
	if _, err := writeFile.Write([]byte("typed")); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	select {
	case result := <-done:
		t.Fatalf("Read completed before ready: n=%d err=%v buf=%q", result.n, result.err, string(result.buf))
	case <-time.After(150 * time.Millisecond):
	}
	close(ready)
	select {
	case result := <-done:
		if result.err != nil || result.n != 5 || string(result.buf) != "typed" {
			t.Fatalf("Read() = n=%d err=%v buf=%q", result.n, result.err, string(result.buf))
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not resume after ready was closed")
	}
}

func TestWebTTYReadyFileReaderUnblocksOnContextCancel(t *testing.T) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer readFile.Close()
	defer writeFile.Close()
	ctx, cancel := context.WithCancel(t.Context())
	reader := &webTTYReadyFileReader{ctx: ctx, file: readFile, ready: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := reader.Read(make([]byte, 1))
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock after context cancellation")
	}
}

func TestWebTTYAttachConfigFromParticipant(t *testing.T) {
	session := &rstream.WebTTYSession{
		WorkspaceID: "workspace-1",
		ProjectID:   "project-1",
		ServerID:    "server-1",
	}
	participant := &rstream.WebTTYParticipant{
		ID:          "participant-1",
		SessionID:   "session-1",
		Role:        rstream.WebTTYParticipantRoleController,
		AttachGrant: []byte("grant"),
		DeviceID:    "device-1",
		BrowserID:   "browser-1",
	}
	cfg := webTTYAttachConfigFromParticipant(session, participant, nil)
	if cfg.SessionID != "session-1" || cfg.WorkspaceID != "workspace-1" || cfg.ProjectID != "project-1" || cfg.ServerID != "server-1" ||
		cfg.ParticipantID != "participant-1" || cfg.RequestedRole != "controller" || string(cfg.AttachGrant) != "grant" {
		t.Fatalf("attach config = %#v", cfg)
	}
	cfg.AttachGrant[0] = 'G'
	if string(participant.AttachGrant) != "grant" {
		t.Fatalf("attach config should copy grant bytes")
	}
}

func TestWebTTYSessionAttachRequestPropagatesWorkspaceDevice(t *testing.T) {
	req := webTTYSessionAttachRequest(&webTTYSessionAttachCrypto{
		PayloadCrypto: &webtty.PayloadCrypto{},
		DeviceID:      " device-1 ",
		BrowserID:     " browser-1 ",
	})
	if req.Role != string(rstream.WebTTYParticipantRoleSpectator) || req.Transport != rstream.WebTTYTransportWebSocket {
		t.Fatalf("attach request role/transport = %#v", req)
	}
	if req.DeviceID != "device-1" || req.BrowserID != "browser-1" {
		t.Fatalf("attach request should propagate trusted identity: %#v", req)
	}
}

func TestWebTTYSessionAttachRequestAllowsDirectIdentity(t *testing.T) {
	req := webTTYSessionAttachRequest(&webTTYSessionAttachCrypto{PayloadCrypto: &webtty.PayloadCrypto{}})
	if req.DeviceID != "" || req.BrowserID != "" {
		t.Fatalf("direct WebTTY identity attach should not invent device metadata: %#v", req)
	}
}

func TestWebTTYDetachContextPreservesValuesAfterParentCancel(t *testing.T) {
	type contextKey string
	key := contextKey("trace")
	parent, cancelParent := context.WithCancel(context.WithValue(t.Context(), key, "trace-1"))
	cancelParent()
	ctx, cancel := webTTYDetachContext(parent)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("detach context should survive parent cancellation: %v", err)
	}
	if got := ctx.Value(key); got != "trace-1" {
		t.Fatalf("detach context value = %#v", got)
	}
}

func TestWebTTYDetachContextAllowsNilParent(t *testing.T) {
	ctx, cancel := webTTYDetachContext(nil)
	defer cancel()
	if ctx == nil {
		t.Fatal("detach context is nil")
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("detach context should have a deadline")
	}
}

func TestValidateWebTTYSessionAttachSupportAllowsLiveE2E(t *testing.T) {
	live := rstream.WebTTYSessionLive{Available: true, Attachable: true, HasUpstream: true}
	if err := validateWebTTYSessionAttachSupport(&rstream.WebTTYSession{ID: "session-1", Status: rstream.WebTTYSessionStatusActive, DownTransport: rstream.WebTTYTransportWebSocket, EncryptionMode: rstream.WebTTYEncryptionModeManaged, Live: live}); err != nil {
		t.Fatalf("managed attach support error = %v", err)
	}
	if err := validateWebTTYSessionAttachSupport(&rstream.WebTTYSession{ID: "session-1", Status: rstream.WebTTYSessionStatusActive, DownTransport: rstream.WebTTYTransportWebSocket, EncryptionMode: rstream.WebTTYEncryptionModeE2E, Live: live}); err != nil {
		t.Fatalf("E2E attach support error = %v", err)
	}
}

func TestValidateWebTTYSessionJoinCapabilities(t *testing.T) {
	if err := validateWebTTYSessionJoinCapabilities(&rstream.WebTTYCapabilities{LiveAttach: true}); err != nil {
		t.Fatalf("expected live attach capabilities to pass, got %v", err)
	}
	if err := validateWebTTYSessionJoinCapabilities(&rstream.WebTTYCapabilities{}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestValidateWebTTYSessionControlCapabilities(t *testing.T) {
	if err := validateWebTTYSessionControlCapabilities(&rstream.WebTTYCapabilities{ControlTransfer: true}); err != nil {
		t.Fatalf("expected control transfer capabilities to pass, got %v", err)
	}
	if err := validateWebTTYSessionControlCapabilities(&rstream.WebTTYCapabilities{}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestValidateWebTTYSessionAttachSupportRejectsUnsupportedLiveState(t *testing.T) {
	live := rstream.WebTTYSessionLive{Available: true, Attachable: true, HasUpstream: true}
	cases := []struct {
		name    string
		session *rstream.WebTTYSession
		want    string
	}{
		{
			name: "closed",
			session: &rstream.WebTTYSession{
				ID:            "session-1",
				Status:        rstream.WebTTYSessionStatusClosed,
				DownTransport: rstream.WebTTYTransportWebSocket,
				Live:          live,
			},
			want: "not active",
		},
		{
			name: "plain",
			session: &rstream.WebTTYSession{
				ID:            "session-1",
				Status:        rstream.WebTTYSessionStatusActive,
				DownTransport: rstream.WebTTYTransportPlain,
				Live:          live,
			},
			want: "websocket sessions only",
		},
		{
			name: "not live",
			session: &rstream.WebTTYSession{
				ID:            "session-1",
				Status:        rstream.WebTTYSessionStatusActive,
				DownTransport: rstream.WebTTYTransportWebSocket,
			},
			want: "not live",
		},
		{
			name: "not attachable",
			session: &rstream.WebTTYSession{
				ID:            "session-1",
				Status:        rstream.WebTTYSessionStatusActive,
				DownTransport: rstream.WebTTYTransportWebSocket,
				Live:          rstream.WebTTYSessionLive{Available: true, HasUpstream: true},
			},
			want: "cannot be joined",
		},
		{
			name: "opening without upstream",
			session: &rstream.WebTTYSession{
				ID:            "session-1",
				Status:        rstream.WebTTYSessionStatusOpening,
				DownTransport: rstream.WebTTYTransportWebSocket,
				Live:          rstream.WebTTYSessionLive{Available: true, Attachable: true},
			},
			want: "no upstream",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebTTYSessionAttachSupport(tc.session)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestWebTTYSessionKeyGrantEnvelopeFromGrantUsesRawKeyContext(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	keyContext := []byte(`{"z":2,"a":1}`)
	clientCrypto, err := webtty.NewE2EClientPayloadCrypto(webtty.E2EPayloadCryptoConfig{
		KeyContext: keyContext,
		Recipients: []webtty.E2ERecipient{{
			KeyID:     identity.KeyID,
			PublicKey: identity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	grant := rstream.WebTTYKeyGrantDecryptMaterial{
		Crypto: testWebTTYKeyGrantMetadata(t, clientCrypto.SessionKeyGrant),
	}
	grant.Crypto.KeyContext = json.RawMessage(`{"a":1,"z":2}`)
	grant.Crypto.KeyContextRaw = webtty.EncodeE2EKeyMaterial(keyContext)
	sessionKeyGrant, err := webTTYSessionKeyGrantEnvelopeFromGrant(grant)
	if err != nil {
		t.Fatalf("webTTYSessionKeyGrantEnvelopeFromGrant() error = %v", err)
	}
	if !bytes.Equal(sessionKeyGrant.KeyContext, keyContext) {
		t.Fatalf("key context = %q, want %q", sessionKeyGrant.KeyContext, keyContext)
	}
	if _, err := webtty.NewE2EServerPayloadCrypto(sessionKeyGrant, *identity); err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto() error = %v", err)
	}
}

func TestWebTTYSessionKeyGrantEnvelopeFromGrantRejectsReservedSuites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		crypto rstream.WebTTYCryptoMetadata
		want   string
	}{
		{
			name: "payload suite",
			crypto: rstream.WebTTYCryptoMetadata{
				PayloadSuite:     "chacha20-poly1305",
				KeyEnvelopeSuite: "hpke-x25519-hkdf-sha256-aes-256-gcm",
			},
			want: "unsupported WebTTY payload cipher suite",
		},
		{
			name: "key envelope suite",
			crypto: rstream.WebTTYCryptoMetadata{
				PayloadSuite:     "aes-256-gcm",
				KeyEnvelopeSuite: "hpke-x25519-hkdf-sha256-chacha20-poly1305",
			},
			want: "unsupported WebTTY key envelope suite",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := webTTYSessionKeyGrantEnvelopeFromGrant(rstream.WebTTYKeyGrantDecryptMaterial{Crypto: tc.crypto})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestWebTTYDecryptSessionEventPayloadUsesE2EGrantCrypto(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	clientCrypto, err := webtty.NewE2EClientPayloadCrypto(webtty.E2EPayloadCryptoConfig{
		Recipients: []webtty.E2ERecipient{{
			KeyID:     identity.KeyID,
			PublicKey: identity.PublicKey,
		}},
	})
	if err != nil {
		t.Fatalf("NewE2EClientPayloadCrypto() error = %v", err)
	}
	serverCrypto, err := webtty.NewE2EServerPayloadCrypto(clientCrypto.SessionKeyGrant, *identity)
	if err != nil {
		t.Fatalf("NewE2EServerPayloadCrypto() error = %v", err)
	}
	encrypted, err := clientCrypto.EncryptStdout(t.Context(), []byte("recorded stdout"))
	if err != nil {
		t.Fatalf("EncryptStdout() error = %v", err)
	}
	crypto := testWebTTYPayloadCryptoMetadata(t, encrypted.PayloadCrypto)
	decoded, err := webTTYPayloadCryptoMetadataFromMetadata(crypto)
	if err != nil {
		t.Fatalf("webTTYPayloadCryptoMetadataFromMetadata() error = %v", err)
	}
	if !bytes.Equal(decoded.AADContext, encrypted.PayloadCrypto.AADContext) {
		t.Fatalf("decoded aad context = %q, want %q", decoded.AADContext, encrypted.PayloadCrypto.AADContext)
	}
	event := rstream.WebTTYSessionEvent{
		ID:                "event-1",
		Seq:               "7",
		Type:              rstream.WebTTYSessionEventData,
		StreamType:        rstream.WebTTYStreamTypeStdout,
		PayloadCiphertext: encrypted.Ciphertext,
		PayloadLength:     int(encrypted.PlaintextLength),
		Crypto:            &crypto,
	}
	session := &rstream.WebTTYSession{ID: "session-1", EncryptionMode: rstream.WebTTYEncryptionModeE2E}
	plaintext, ok, err := webTTYDecryptSessionEventPayload(t.Context(), nil, session, event, serverCrypto)
	if err != nil {
		t.Fatalf("webTTYDecryptSessionEventPayload() error = %v", err)
	}
	if !ok || string(plaintext) != "recorded stdout" {
		t.Fatalf("plaintext = %q ok=%t", string(plaintext), ok)
	}
}

func TestWebTTYDecryptSessionEventPayloadUsesManagedPlaintext(t *testing.T) {
	event := rstream.WebTTYSessionEvent{
		ID:                "event-1",
		Seq:               "7",
		Type:              rstream.WebTTYSessionEventData,
		StreamType:        rstream.WebTTYStreamTypeStdout,
		PayloadCiphertext: []byte("ciphertext"),
		PayloadPlaintext:  []byte("managed stdout"),
	}
	session := &rstream.WebTTYSession{ID: "session-1", EncryptionMode: rstream.WebTTYEncryptionModeManaged}
	plaintext, ok, err := webTTYDecryptSessionEventPayload(t.Context(), nil, session, event, nil)
	if err != nil {
		t.Fatalf("webTTYDecryptSessionEventPayload() error = %v", err)
	}
	if !ok || string(plaintext) != "managed stdout" {
		t.Fatalf("plaintext = %q ok=%t", string(plaintext), ok)
	}
	event.PayloadPlaintext[0] = 'X'
	if string(plaintext) != "managed stdout" {
		t.Fatalf("plaintext should be cloned, got %q", string(plaintext))
	}
}

func TestWebTTYRenderSessionTextExportDropsClosedAlternateScreen(t *testing.T) {
	events := []rstream.WebTTYSessionEvent{{
		Type:       rstream.WebTTYSessionEventData,
		StreamType: rstream.WebTTYStreamTypeStdout,
	}}
	exported := []webTTYSessionExportEvent{{
		WebTTYSessionEvent: events[0],
		PayloadPlaintext: []byte(
			"prompt$ htop\r\n\x1b[?1049h\x1b[2J\x1b[HTUI SCREEN\nPID USER\n\x1b[?1049lprompt$ echo done\r\ndone\r\n",
		),
	}}
	log := webTTYRenderSessionTextExport(events, exported, webTTYSessionExportOptions{
		ActiveAlternateScreen: true,
		IncludeStderr:         true,
		IncludeStdout:         true,
		IncludeTerminalModes:  true,
	})
	if !log.DetectedAlternateScreen {
		t.Fatal("expected alternate-screen detection")
	}
	if !strings.Contains(log.Text, "prompt$ htop") || !strings.Contains(log.Text, "prompt$ echo done") || !strings.Contains(log.Text, "done") {
		t.Fatalf("missing main-screen output:\n%s", log.Text)
	}
	if strings.Contains(log.Text, "TUI SCREEN") || strings.Contains(log.Text, "PID USER") {
		t.Fatalf("closed alternate-screen content leaked:\n%s", log.Text)
	}
	if !strings.Contains(log.Text, "terminal alternate screen entered") || !strings.Contains(log.Text, "terminal alternate screen left") {
		t.Fatalf("missing alternate-screen markers:\n%s", log.Text)
	}
}

func TestWebTTYRenderSessionTextExportIncludesActiveAlternateScreen(t *testing.T) {
	events := []rstream.WebTTYSessionEvent{{
		Type:       rstream.WebTTYSessionEventData,
		StreamType: rstream.WebTTYStreamTypeStdout,
	}}
	exported := []webTTYSessionExportEvent{{
		WebTTYSessionEvent: events[0],
		PayloadPlaintext:   []byte("prompt$ htop\r\n\x1b[?1049h\x1b[2J\x1b[HCPU 42%\nPID USER\n"),
	}}
	log := webTTYRenderSessionTextExport(events, exported, webTTYSessionExportOptions{
		ActiveAlternateScreen: true,
		IncludeStdout:         true,
		IncludeTerminalModes:  true,
	})
	if !strings.Contains(log.Text, "terminal alternate screen active") || !strings.Contains(log.Text, "CPU 42%") || !strings.Contains(log.Text, "PID USER") {
		t.Fatalf("active alternate-screen snapshot missing:\n%s", log.Text)
	}
}

func TestWebTTYRenderSessionTextExportResizeMarkers(t *testing.T) {
	rawMetadata := json.RawMessage(`{"terminal_size":{"row":43,"col":132}}`)
	events := []rstream.WebTTYSessionEvent{{
		Metadata: rawMetadata,
		Type:     rstream.WebTTYSessionEventResize,
	}}
	log := webTTYRenderSessionTextExport(events, nil, webTTYSessionExportOptions{IncludeResizeMarkers: true})
	if !strings.Contains(log.Text, "terminal resized to 132x43") {
		t.Fatalf("resize marker missing:\n%s", log.Text)
	}
}

func testWebTTYKeyGrantMetadata(t *testing.T, grant *webtty.SessionKeyGrant) rstream.WebTTYCryptoMetadata {
	t.Helper()
	keyEnvelopes := make([]map[string]string, 0, len(grant.KeyEnvelopes))
	for _, keyEnvelope := range grant.KeyEnvelopes {
		keyEnvelopes = append(keyEnvelopes, map[string]string{
			"recipient_key_id": webtty.EncodeE2EKeyMaterial(keyEnvelope.RecipientKeyID),
			"encapsulated_key": webtty.EncodeE2EKeyMaterial(keyEnvelope.EncapsulatedKey),
			"wrapped_key":      webtty.EncodeE2EKeyMaterial(keyEnvelope.WrappedKey),
		})
	}
	rawKeyEnvelopes, err := json.Marshal(keyEnvelopes)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return rstream.WebTTYCryptoMetadata{
		PayloadSuite:     "aes-256-gcm",
		PayloadKeyID:     webtty.EncodeE2EKeyMaterial(grant.PayloadKeyID),
		KeyEnvelopeSuite: "hpke-x25519-hkdf-sha256-aes-256-gcm",
		KeyEnvelopes:     rawKeyEnvelopes,
		KeyContext:       json.RawMessage(`{"encoding":"base64","value":"` + webtty.EncodeE2EKeyMaterial(grant.KeyContext) + `"}`),
		KeyContextRaw:    webtty.EncodeE2EKeyMaterial(grant.KeyContext),
	}
}

func testWebTTYPayloadCryptoMetadata(t *testing.T, crypto *webtty.PayloadCryptoMetadata) rstream.WebTTYCryptoMetadata {
	t.Helper()
	return rstream.WebTTYCryptoMetadata{
		PayloadSuite:  "aes-256-gcm",
		PayloadKeyID:  webtty.EncodeE2EKeyMaterial(crypto.PayloadKeyID),
		Nonce:         webtty.EncodeE2EKeyMaterial(crypto.Nonce),
		KeyContext:    json.RawMessage(`{"encoding":"base64","value":"` + webtty.EncodeE2EKeyMaterial(crypto.AADContext) + `"}`),
		KeyContextRaw: webtty.EncodeE2EKeyMaterial(crypto.AADContext),
	}
}

func TestNewWebTTYEngineDialTLSContextRequiresClient(t *testing.T) {
	dial := newWebTTYEngineDialTLSContext(nil)
	if _, err := dial(context.Background(), "tcp", "engine.example.com:443"); err == nil || !strings.Contains(err.Error(), "rstream client") {
		t.Fatalf("expected client error, got %v", err)
	}
}

func TestWebTTYClientAuthTokenTrims(t *testing.T) {
	token := " token "
	got := webTTYClientAuthToken(&rstream.Client{Token: &token})
	if got == nil || *got != "token" {
		t.Fatalf("token = %#v", got)
	}
}

func TestPrintWebTTYSessionsTable(t *testing.T) {
	older := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	sessions := rstream.ListWebTTYSessionsResponse{
		{ID: "session-old", ServerID: "prod-shell", Status: rstream.WebTTYSessionStatusClosed, SessionMode: rstream.WebTTYSessionModeInteractive, RecordingMode: rstream.WebTTYRecordingModeRecorded, EncryptionMode: rstream.WebTTYEncryptionModeE2E, DownTransport: rstream.WebTTYTransportWebSocket, StartedAt: older},
		{ID: "session-new", Status: rstream.WebTTYSessionStatusActive, SessionMode: rstream.WebTTYSessionModeNonInteractive, RecordingMode: rstream.WebTTYRecordingModeRecorded, EncryptionMode: rstream.WebTTYEncryptionModeManaged, DownTransport: rstream.WebTTYTransportWebSocket, UpTransport: rstream.WebTTYTransportPlain, StartedAt: newer},
	}
	var out bytes.Buffer
	if err := printWebTTYSessionsTable(&out, &sessions); err != nil {
		t.Fatalf("printWebTTYSessionsTable() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"SESSION", "session-new", "websocket->plain", "session-old", "prod-shell"} {
		if !strings.Contains(text, want) {
			t.Fatalf("table output missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "session-new") > strings.Index(text, "session-old") {
		t.Fatalf("newer session should be listed first:\n%s", text)
	}
}

func TestPrintWebTTYControlRequestsTable(t *testing.T) {
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	resolved := created.Add(time.Minute)
	requests := rstream.ListWebTTYControlRequestsResponse{
		{ID: "request-old", RequesterParticipantID: "participant-1", Status: rstream.WebTTYControlRequestPending, CreatedAt: created},
		{ID: "request-new", RequesterParticipantID: "participant-2", ApproverParticipantID: "participant-3", Status: rstream.WebTTYControlRequestGranted, CreatedAt: created.Add(time.Hour), ResolvedAt: &resolved},
	}
	var out bytes.Buffer
	if err := printWebTTYControlRequestsTable(&out, &requests); err != nil {
		t.Fatalf("printWebTTYControlRequestsTable() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"REQUEST", "request-new", "granted", "participant-3", "request-old", "pending"} {
		if !strings.Contains(text, want) {
			t.Fatalf("control requests table missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "request-new") > strings.Index(text, "request-old") {
		t.Fatalf("newer request should be listed first:\n%s", text)
	}
}

func TestPrintWebTTYParticipantsTable(t *testing.T) {
	attached := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	detached := attached.Add(time.Hour)
	participants := rstream.ListWebTTYParticipantsResponse{
		{ID: "participant-2", UserID: "user-2", Role: rstream.WebTTYParticipantRoleSpectator, AttachedAt: attached.Add(time.Minute)},
		{ID: "participant-1", UserID: "user-1", Role: rstream.WebTTYParticipantRoleController, Controller: true, AttachedAt: attached, DetachedAt: &detached},
	}
	var out bytes.Buffer
	if err := printWebTTYParticipantsTable(&out, &participants); err != nil {
		t.Fatalf("printWebTTYParticipantsTable() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"PARTICIPANT", "participant-1", "controller", "true", "participant-2", "spectator"} {
		if !strings.Contains(text, want) {
			t.Fatalf("participants table missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "participant-1") > strings.Index(text, "participant-2") {
		t.Fatalf("participants should be ordered by attach time:\n%s", text)
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestUICaptureInputSessionDoesNotInterceptPrintableRunes(t *testing.T) {
	t.Parallel()
	handle := newUITestSessionHandle()
	app := &uiApp{app: tview.NewApplication(), activePage: uiPageSession, session: handle}
	event := tcell.NewEventKey(tcell.KeyRune, 'i', tcell.ModNone)
	got := app.captureInput(event)
	if got == nil {
		t.Fatalf("captureInput() returned nil")
	}
	if got.Rune() != 'i' {
		t.Fatalf("captureInput() rune = %q, want %q", got.Rune(), 'i')
	}
	if !handle.showInfo {
		t.Fatalf("captureInput() unexpectedly toggled session details")
	}
}

func TestUICaptureInputSessionSendsCtrlCToTerminal(t *testing.T) {
	t.Parallel()
	handle := newUITestSessionHandle()
	app := &uiApp{app: tview.NewApplication(), activePage: uiPageSession, session: handle}
	defer handle.view.term.Close()
	readCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		buffer := make([]byte, 8)
		n, err := handle.view.term.Read(buffer)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- append([]byte(nil), buffer[:n]...)
	}()
	got := app.captureInput(tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl))
	if got != nil {
		t.Fatalf("captureInput() = %v, want nil", got)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Read() error = %v", err)
	case data := <-readCh:
		if len(data) != 1 || data[0] != 0x03 {
			t.Fatalf("terminal bytes = %v, want [3]", data)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Read() timed out")
	}
}

func TestUICaptureInputSessionDoesNotInterceptMetaC(t *testing.T) {
	t.Parallel()
	handle := newUITestSessionHandle()
	app := &uiApp{app: tview.NewApplication(), activePage: uiPageSession, session: handle}
	event := tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModMeta)
	got := app.captureInput(event)
	if got != nil {
		t.Fatalf("captureInput() = %#v, want nil", got)
	}
}

func TestUICaptureInputSessionDoesNotForwardMetaV(t *testing.T) {
	t.Parallel()
	handle := newUITestSessionHandle()
	app := &uiApp{app: tview.NewApplication(), activePage: uiPageSession, session: handle}
	event := tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModMeta)
	got := app.captureInput(event)
	if got != nil {
		t.Fatalf("captureInput() = %#v, want nil", got)
	}
}

func TestUICaptureInputSessionTogglesDetailsOnLeaderD(t *testing.T) {
	t.Parallel()
	handle := newUITestSessionHandle()
	app := &uiApp{app: tview.NewApplication(), activePage: uiPageSession, session: handle}
	got := app.captureInput(tcell.NewEventKey(tcell.KeyCtrlG, 0, tcell.ModCtrl))
	if got != nil {
		t.Fatalf("captureInput() = %v, want nil", got)
	}
	if !app.sessionLeader {
		t.Fatalf("captureInput() did not enter leader mode")
	}
	got = app.captureInput(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))
	if got != nil {
		t.Fatalf("captureInput() = %v, want nil", got)
	}
	if handle.showInfo {
		t.Fatalf("captureInput() did not toggle session details")
	}
}

func TestUICaptureInputSessionLeaderCtrlGSendsLiteral(t *testing.T) {
	t.Parallel()
	handle := newUITestSessionHandle()
	app := &uiApp{app: tview.NewApplication(), activePage: uiPageSession, session: handle}
	defer handle.view.term.Close()
	readCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		buffer := make([]byte, 8)
		n, err := handle.view.term.Read(buffer)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- append([]byte(nil), buffer[:n]...)
	}()
	if got := app.captureInput(tcell.NewEventKey(tcell.KeyCtrlG, 0, tcell.ModCtrl)); got != nil {
		t.Fatalf("captureInput() = %v, want nil", got)
	}
	if got := app.captureInput(tcell.NewEventKey(tcell.KeyCtrlG, 0, tcell.ModCtrl)); got != nil {
		t.Fatalf("captureInput() = %v, want nil", got)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Read() error = %v", err)
	case data := <-readCh:
		if len(data) != 1 || data[0] != 0x07 {
			t.Fatalf("terminal bytes = %v, want [7]", data)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Read() timed out")
	}
}

func TestUICaptureInputHelpVisibleClosesOnF1(t *testing.T) {
	t.Parallel()
	app := &uiApp{app: tview.NewApplication(), pages: tview.NewPages(), table: tview.NewTable(), helpVisible: true}
	got := app.captureInput(tcell.NewEventKey(tcell.KeyF1, 0, tcell.ModNone))
	if got != nil {
		t.Fatalf("captureInput() = %v, want nil", got)
	}
	if app.helpVisible {
		t.Fatalf("captureInput() did not close help")
	}
}

func TestUIShowWebTTYIdentityPickerOwnsKeyboardInput(t *testing.T) {
	t.Parallel()
	app := &uiApp{
		app:        tview.NewApplication(),
		pages:      tview.NewPages(),
		table:      tview.NewTable(),
		activePage: uiPageInventory,
		state:      uiState{View: uiViewWebTTY},
	}
	app.showWebTTYIdentityPicker(
		webtty.ServerInfo{Target: "prod-shell"},
		&uiWebTTYIdentitySelection{
			knownServerName: "prod-shell",
			identities: []map[string]any{{
				"name":           "review-client",
				"signing_key_id": "signing-key",
			}},
		},
	)
	if app.activePage != uiPageIdentityPicker {
		t.Fatalf("active page = %q, want identity picker", app.activePage)
	}
	if got := app.captureInput(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)); got != nil {
		t.Fatalf("captureInput(q) = %v, want nil", got)
	}
	if app.activePage != uiPageInventory {
		t.Fatalf("active page after cancel = %q, want inventory", app.activePage)
	}
}

func TestUIShowWebTTYIdentityPickerEnterActivatesSelection(t *testing.T) {
	t.Parallel()
	app := &uiApp{
		app:        tview.NewApplication(),
		pages:      tview.NewPages(),
		table:      tview.NewTable(),
		activePage: uiPageInventory,
		state:      uiState{View: uiViewWebTTY},
	}
	app.showWebTTYIdentityPicker(
		webtty.ServerInfo{Target: "prod-shell"},
		&uiWebTTYIdentitySelection{knownServerName: "prod-shell"},
	)
	focus, ok := app.app.GetFocus().(*tview.List)
	if !ok {
		t.Fatalf("focused primitive = %T, want *tview.List", app.app.GetFocus())
	}
	handler := focus.InputHandler()
	if handler == nil {
		t.Fatalf("identity picker list has no input handler")
	}
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {
		app.app.SetFocus(p)
	})
	if app.activePage != uiPageInventory {
		t.Fatalf("active page after enter = %q, want inventory", app.activePage)
	}
	if !strings.Contains(app.state.Message, "Create an identity first") {
		t.Fatalf("message after enter = %q, want create-identity guidance", app.state.Message)
	}
}

func TestUIShowWebTTYIdentityPickerEnterUsesSelectedIdentity(t *testing.T) {
	t.Parallel()
	app := &uiApp{
		ctx:        t.Context(),
		app:        tview.NewApplication(),
		pages:      tview.NewPages(),
		table:      tview.NewTable(),
		activePage: uiPageInventory,
		state:      uiState{View: uiViewWebTTY},
	}
	app.pages.AddPage(uiPageInventory, tview.NewBox(), true, true)
	app.showWebTTYIdentityPicker(
		webtty.ServerInfo{Target: "prod-shell"},
		&uiWebTTYIdentitySelection{
			knownServerName: "prod-shell",
			identities: []map[string]any{{
				"name":           "review-client",
				"signing_key_id": "signing-key",
			}},
		},
	)
	focus, ok := app.app.GetFocus().(*tview.List)
	if !ok {
		t.Fatalf("focused primitive = %T, want *tview.List", app.app.GetFocus())
	}
	handler := focus.InputHandler()
	if handler == nil {
		t.Fatalf("identity picker list has no input handler")
	}
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {
		app.app.SetFocus(p)
	})
	if app.activePage != uiPageInventory {
		t.Fatalf("active page after enter = %q, want inventory", app.activePage)
	}
	if !strings.Contains(app.state.Message, "prod-shell is not online") {
		t.Fatalf("message after selecting identity = %q, want offline server handling", app.state.Message)
	}
}

func TestUIInventoryActionsTextUsesStableDetailLabel(t *testing.T) {
	t.Parallel()
	app := &uiApp{state: uiState{View: uiViewWebTTY, Detail: uiDetailModeSummary}}
	summary := app.inventoryActionsText()
	app.state.Detail = uiDetailModeJSON
	jsonText := app.inventoryActionsText()
	if summary != jsonText {
		t.Fatalf("inventoryActionsText() changed across detail modes:\nsummary=%q\njson=%q", summary, jsonText)
	}
	if !strings.Contains(summary, "Summary/JSON") {
		t.Fatalf("inventoryActionsText() = %q, want stable Summary/JSON label", summary)
	}
	app.switchingTo = "prod"
	if switching := app.inventoryActionsText(); switching != summary {
		t.Fatalf("switching changed inventory actions:\nnormal=%q\nswitching=%q", summary, switching)
	}
}

func TestUIInventoryPlaceholderResetsAndDisablesSelection(t *testing.T) {
	t.Parallel()
	app := &uiApp{
		app:   tview.NewApplication(),
		state: uiState{View: uiViewClients, Detail: uiDetailModeSummary},
	}
	app.buildInventoryPage()
	app.table.Select(42, 0)

	app.renderInventory()

	row, column := app.table.GetSelection()
	if row != 0 || column != 0 {
		t.Fatalf("empty inventory selection = (%d, %d), want (0, 0)", row, column)
	}
	rowsSelectable, columnsSelectable := app.table.GetSelectable()
	if rowsSelectable || columnsSelectable {
		t.Fatalf("empty inventory selectable = (%v, %v), want disabled", rowsSelectable, columnsSelectable)
	}
	assertUITableNavigationReturns(t, app.table)
}

func assertUITableNavigationReturns(t *testing.T, table *tview.Table) {
	t.Helper()
	handler := table.InputHandler()
	if handler == nil {
		t.Fatal("table has no input handler")
	}
	done := make(chan struct{})
	go func() {
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("table navigation did not return")
	}
}

func TestUIInventoryMetaTextIncludesContextAndEngine(t *testing.T) {
	t.Parallel()
	app := &uiApp{
		connection: uiConnectionInfo{
			ContextName: "prod",
			Engine:      "engine.prod:443",
		},
		snapshot: uiSnapshot{Connected: true},
	}
	got := app.inventoryMetaText()
	if !strings.Contains(got, "ctx prod") {
		t.Fatalf("inventoryMetaText() = %q, want context name", got)
	}
	if !strings.Contains(got, "engine engine.prod:443") {
		t.Fatalf("inventoryMetaText() = %q, want engine", got)
	}
}

func TestFormatRawObjectDoesNotPrefixJSON(t *testing.T) {
	t.Parallel()
	got := formatRawObject(map[string]string{"hello": "world"})
	if strings.Contains(got, "Raw object") {
		t.Fatalf("formatRawObject() = %q, want no Raw object prefix", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("formatRawObject() = %q, want JSON object", got)
	}
}
func TestUIAppDetailFormattersCoverSummaryAndJSONModes(t *testing.T) {
	t.Parallel()
	app := &uiApp{state: uiState{Detail: uiDetailModeSummary}}
	client := rstream.ClientProperties{ID: "client-1", Status: "online", Agent: rstream.StringPtr("agent"), Labels: map[string]string{"b": "2", "a": "1"}}
	clientSummary := app.formatClientDetail(client)
	if !strings.Contains(clientSummary, "ID                 client-1") || !strings.Contains(clientSummary, "  a=1\n  b=2") {
		t.Fatalf("formatClientDetail(summary) = %q", clientSummary)
	}
	tunnel := rstream.TunnelInventory{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("tun-1"), Name: rstream.StringPtr("web"), Publish: rstream.BoolPtr(true), Labels: map[string]string{"env": "prod"}}, Status: "online", ClientID: "client-1"}
	tunnelSummary := app.formatTunnelDetail(tunnel)
	if !strings.Contains(tunnelSummary, "Target             web") || !strings.Contains(tunnelSummary, "Published          true") {
		t.Fatalf("formatTunnelDetail(summary) = %q", tunnelSummary)
	}
	server := webtty.ServerInfo{Target: "shell", Status: "online", TunnelID: "tun-1", TunnelName: rstream.StringPtr("tty"), RstreamURL: "rstrm://tty", Hostname: rstream.StringPtr("host"), Labels: map[string]string{"role": "admin"}}
	serverSummary := app.formatWebTTYDetail(server)
	if !strings.Contains(serverSummary, "Target             shell") || !strings.Contains(serverSummary, "Tunnel name        tty") {
		t.Fatalf("formatWebTTYDetail(summary) = %q", serverSummary)
	}
	app.state.Detail = uiDetailModeJSON
	if got := app.formatClientDetail(client); !strings.Contains(got, `"id": "client-1"`) {
		t.Fatalf("formatClientDetail(json) = %q", got)
	}
}
func TestUIAppPureDisplayHelpers(t *testing.T) {
	t.Parallel()
	if got := defaultUIView(uiSnapshot{WebTTY: []webtty.ServerInfo{{Target: "shell"}}}); got != uiViewWebTTY {
		t.Fatalf("defaultUIView(webtty) = %q", got)
	}
	if got := defaultUIView(uiSnapshot{Tunnels: []rstream.TunnelInventory{{Status: "online"}}}); got != uiViewTunnels {
		t.Fatalf("defaultUIView(tunnels) = %q", got)
	}
	if got := defaultUIView(uiSnapshot{}); got != uiViewClients {
		t.Fatalf("defaultUIView(empty) = %q", got)
	}
	value := " value "
	protocol := rstream.ProtocolHTTP
	if optionalValue(nil) != "-" || optionalValue(&value) != "value" {
		t.Fatalf("optionalValue returned unexpected values")
	}
	if emptyDash("  ") != "-" || emptyDash(" value ") != "value" {
		t.Fatalf("emptyDash returned unexpected values")
	}
	if boolValue(nil) || !boolValue(rstream.BoolPtr(true)) {
		t.Fatalf("boolValue returned unexpected values")
	}
	if stringValue(&protocol) != "http" || stringValue((*rstream.Protocol)(nil)) != "" {
		t.Fatalf("stringValue returned unexpected values")
	}
	if uiStatusColor("offline") != uiColorDanger || uiStatusColor("connecting") != uiColorMuted || uiStatusColor("online") != uiColorText {
		t.Fatalf("uiStatusColor returned unexpected values")
	}
	if uiStatusMessage(" ") != " " || !strings.Contains(uiStatusMessage(" failed "), "failed") {
		t.Fatalf("uiStatusMessage returned unexpected values")
	}
	if got := uiSafe("[red]owned[-]"); got != tview.Escape("[red]owned[-]") {
		t.Fatalf("uiSafe() = %q, want escaped tview markup", got)
	}
	if got := uiStatusMessage("[red]owned[-]"); !strings.Contains(got, tview.Escape("[red]owned[-]")) {
		t.Fatalf("uiStatusMessage() = %q, want escaped message body", got)
	}
	if !contextCanceled(context.Canceled) || !contextCanceled(context.DeadlineExceeded) || contextCanceled(nil) {
		t.Fatalf("contextCanceled returned unexpected values")
	}
}
func TestUIAppTableHelpersAndSessionChrome(t *testing.T) {
	t.Parallel()
	table := tview.NewTable()
	addHeaderRow(table, []string{"Name", "Status"})
	addPlaceholderRow(table, "empty")
	setRow(table, 2, []string{"client-1", "offline"}, "client-1")
	setRow(table, 3, []string{"[red]client[-]", "online"}, "client-2")
	if table.GetCell(0, 0).Text != "Name" || table.GetCell(1, 0).Text != "empty" || table.GetCell(2, 0).GetReference() != "client-1" || table.GetCell(3, 0).Text != tview.Escape("[red]client[-]") {
		t.Fatalf("table helpers did not populate expected cells")
	}
	server := webtty.ServerInfo{Target: "shell", Hostname: rstream.StringPtr("host")}
	if !strings.Contains(sessionHeaderText(server), "shell") || !strings.Contains(sessionHeaderText(server), "host") {
		t.Fatalf("sessionHeaderText() = %q", sessionHeaderText(server))
	}
	if !strings.Contains(sessionActionsText(), "Ctrl+g q") {
		t.Fatalf("sessionActionsText() = %q", sessionActionsText())
	}
	if unicodeLower('É') != 'é' {
		t.Fatalf("unicodeLower did not lower-case rune")
	}
}
func TestUIWebTTYSessionConfigKeepsPlainRegisteredServerUnencrypted(t *testing.T) {
	workspaceID := "workspace-plain"
	projectID := "project-plain"
	serverID := "server-plain"
	projectEndpoint := "plain-endpoint"
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "control plane should not be called for plain registered runtime", http.StatusBadRequest)
	}))
	defer controlServer.Close()
	app := &uiApp{
		runtime: &resolvedRuntime{Resolved: config.Resolved{
			APIURL: controlServer.URL,
			Token:  "token",
			Context: &config.Context{
				ProjectEndpoint: projectEndpoint,
			},
		}},
	}
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{
		Target:           "plain-shell",
		RstreamURL:       "rstrm://plain-shell",
		ServerID:         &serverID,
		WorkspaceID:      &workspaceID,
		ProjectID:        &projectID,
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyDisabled),
	}, slog.Default(), "", false)
	if err != nil {
		t.Fatalf("webTTYSessionConfig() error = %v", err)
	}
	if selection != nil {
		t.Fatalf("plain server requested identity selection: %#v", selection)
	}
	cfg := plan.config
	if cfg.PayloadCrypto != nil || cfg.EndpointIdentity != nil || cfg.ExpectedServerIdentity != nil {
		t.Fatalf("plain registered server configured E2E unexpectedly: %#v", cfg)
	}
}
func TestUIWebTTYSessionConfigResolvesWorkspaceManagedE2E(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	projectID := "project-1"
	serverID := "server-1"
	projectEndpoint := "proj-endpoint"
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	var seen controlplane.ResolveWebTTYServerClientRequest
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution":
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if len(seen.DeviceProofs) != 1 || seen.DeviceProofs[0].DeviceFingerprint != device.Fingerprint {
				http.Error(w, "missing device proof", http.StatusBadRequest)
				return
			}
			signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
			payload := workspaceDeviceLookupPayload(workspaceID, device.Fingerprint, seen.DeviceProofs[0].Challenge, seen.DeviceProofs[0].SignedAt)
			if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.DeviceProofs[0].Signature) {
				http.Error(w, "invalid device proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
				ServerID:               serverID,
				WorkspaceID:            workspaceID,
				ProjectID:              projectID,
				EncryptionPolicy:       "workspace_managed",
				E2ERequired:            true,
				ServerPublicKey:        &serverPublicKey,
				ServerEndpointIdentity: &serverEndpointIdentity,
				ServerKeyAlgorithm:     rstream.StringPtr("x25519-hkdf-sha256-aes-256-gcm"),
				CurrentDevice:          testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle),
			})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer controlServer.Close()
	app := &uiApp{
		runtime: &resolvedRuntime{Resolved: config.Resolved{
			APIURL: controlServer.URL,
			Token:  "token",
			Context: &config.Context{
				ProjectEndpoint: projectEndpoint,
			},
		}},
	}
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{
		Target:           "prod-shell",
		RstreamURL:       "rstrm://prod-shell",
		ServerID:         &serverID,
		WorkspaceID:      &workspaceID,
		ProjectID:        &projectID,
		E2E:              rstream.StringPtr(webtty.WebTTYE2ERequired),
		ClientProof:      rstream.StringPtr(webtty.WebTTYClientProofRequired),
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyWorkspaceManaged),
	}, slog.Default(), "", false)
	if err != nil {
		t.Fatalf("webTTYSessionConfig() error = %v", err)
	}
	if selection != nil {
		t.Fatalf("workspace-managed server requested identity selection: %#v", selection)
	}
	cfg := plan.config
	if cfg.PayloadCrypto == nil || cfg.PayloadCrypto.SessionKeyGrant == nil || len(cfg.PayloadCrypto.SessionKeyGrant.KeyEnvelopes) != 2 {
		t.Fatalf("expected E2E payload crypto for workspace-managed UI session, got %#v", cfg.PayloadCrypto)
	}
	if len(cfg.PayloadCrypto.SessionKeyGrant.KeyContext) == 0 {
		t.Fatalf("expected typed workspace-managed key context")
	}
	if cfg.ExpectedServerIdentity == nil || cfg.EndpointIdentity == nil {
		t.Fatalf("expected authenticated server and workspace device endpoint identities")
	}
	if len(cfg.ClientCredential) == 0 {
		t.Fatalf("expected workspace-managed client credential")
	}
}
func TestUIWebTTYSessionConfigUsesKnownServerClientIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clientIdentity, err := writeTestWebTTYEndpointIdentity(t, "review-client")
	if err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	knownServersPath, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(knownServersPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	knownServers := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionKeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionPublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(serverPublic.SigningKeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(serverPublic.SigningPublicKey),
			ClientIdentity:   "review-client",
		}},
	}
	data, err := json.Marshal(knownServers)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(knownServersPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "control plane should not be called for explicit-key runtime", http.StatusBadRequest)
	}))
	defer controlServer.Close()
	projectEndpoint := "project-endpoint"
	app := &uiApp{
		runtime: &resolvedRuntime{Resolved: config.Resolved{
			APIURL: controlServer.URL,
			Token:  "token",
			Context: &config.Context{
				ProjectEndpoint: projectEndpoint,
			},
		}},
	}
	serverID := "server-1"
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{
		Target:           "prod-shell",
		RstreamURL:       "rstrm://prod-shell",
		ServerID:         &serverID,
		HostKeyID:        rstream.StringPtr(webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionKeyID)),
		E2E:              rstream.StringPtr(webtty.WebTTYE2ERequired),
		ClientProof:      rstream.StringPtr(webtty.WebTTYClientProofRequired),
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyExplicitKey),
	}, slog.Default(), "", false)
	if err != nil {
		t.Fatalf("webTTYSessionConfig() error = %v", err)
	}
	if selection != nil {
		t.Fatalf("known server with client identity requested identity selection: %#v", selection)
	}
	cfg := plan.config
	if cfg.PayloadCrypto == nil || cfg.ExpectedServerIdentity == nil || cfg.EndpointIdentity == nil {
		t.Fatalf("expected E2E UI session config, got %#v", cfg)
	}
	if !bytes.Equal(cfg.ExpectedServerIdentity.SigningKeyID, serverIdentity.Signing.KeyID) {
		t.Fatalf("expected server signing identity from known server entry")
	}
	if !bytes.Equal(cfg.EndpointIdentity.Signing.KeyID, clientIdentity.Signing.KeyID) {
		t.Fatalf("expected associated client identity")
	}
}
func TestUIWebTTYSessionConfigRequestsIdentitySelectionWhenKnownServerHasNoClientIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := writeTestWebTTYEndpointIdentity(t, "review-client"); err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	knownServersPath, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(knownServersPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	knownServers := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionKeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionPublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(serverPublic.SigningKeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(serverPublic.SigningPublicKey),
		}},
	}
	data, err := json.Marshal(knownServers)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(knownServersPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	app := &uiApp{}
	serverID := "server-1"
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{
		Target:           "prod-shell",
		RstreamURL:       "rstrm://prod-shell",
		ServerID:         &serverID,
		HostKeyID:        rstream.StringPtr(webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionKeyID)),
		E2E:              rstream.StringPtr(webtty.WebTTYE2ERequired),
		ClientProof:      rstream.StringPtr(webtty.WebTTYClientProofRequired),
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyExplicitKey),
	}, slog.Default(), "", false)
	if err != nil {
		t.Fatalf("webTTYSessionConfig() error = %v", err)
	}
	if plan != nil {
		t.Fatalf("expected identity selection, got plan %#v", plan)
	}
	if selection == nil {
		t.Fatalf("expected identity selection")
	}
	if selection.knownServerName != "prod-shell" {
		t.Fatalf("known server name = %q, want prod-shell", selection.knownServerName)
	}
	if len(selection.identities) != 1 || selection.identities[0]["name"] != "review-client" {
		t.Fatalf("unexpected identity choices: %#v", selection.identities)
	}
}
func TestUIWebTTYSessionConfigRequiresKnownServerForLightweightExplicitKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	app := &uiApp{}
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{
		Target:           "prod-shell",
		RstreamURL:       "rstrm://prod-shell",
		HostKeyID:        rstream.StringPtr(webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.KeyID)),
		E2E:              rstream.StringPtr(webtty.WebTTYE2ERequired),
		ClientProof:      rstream.StringPtr(webtty.WebTTYClientProofRequired),
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyExplicitKey),
	}, slog.Default(), "", false)
	if err == nil || !strings.Contains(err.Error(), "known-server add prod-shell") {
		t.Fatalf("webTTYSessionConfig() error = %v, want known-server add guidance", err)
	}
	if plan != nil || selection != nil {
		t.Fatalf("expected no plan or selection, got plan=%#v selection=%#v", plan, selection)
	}
}
func TestUIWebTTYSessionConfigSelectedIdentityCanBeRememberedAfterAck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	clientIdentity, err := writeTestWebTTYEndpointIdentity(t, "review-client")
	if err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	knownServersPath, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(knownServersPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	serverPublic := serverIdentity.Public()
	knownServers := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionKeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionPublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(serverPublic.SigningKeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(serverPublic.SigningPublicKey),
		}},
	}
	data, err := json.Marshal(knownServers)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(knownServersPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	app := &uiApp{}
	serverID := "server-1"
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{
		Target:           "prod-shell",
		RstreamURL:       "rstrm://prod-shell",
		ServerID:         &serverID,
		HostKeyID:        rstream.StringPtr(webtty.EncodeE2EKeyMaterial(serverPublic.EncryptionKeyID)),
		E2E:              rstream.StringPtr(webtty.WebTTYE2ERequired),
		ClientProof:      rstream.StringPtr(webtty.WebTTYClientProofRequired),
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyExplicitKey),
	}, slog.Default(), "review-client", true)
	if err != nil {
		t.Fatalf("webTTYSessionConfig() error = %v", err)
	}
	if selection != nil {
		t.Fatalf("selected identity should not request picker: %#v", selection)
	}
	if plan == nil || plan.config == nil {
		t.Fatalf("expected session plan")
	}
	if plan.rememberServerName != "prod-shell" || plan.rememberIdentity != "review-client" {
		t.Fatalf("remember plan = %#v", plan)
	}
	if !bytes.Equal(plan.config.EndpointIdentity.Signing.KeyID, clientIdentity.Signing.KeyID) {
		t.Fatalf("expected selected client identity")
	}
	before, err := webtty.ReadKnownServerKeysFile(knownServersPath)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if before.KnownServers[0].ClientIdentity != "" {
		t.Fatalf("client identity was persisted before ACK")
	}
	if err := rememberWebTTYKnownServerClientIdentity(plan.rememberServerName, plan.rememberIdentity); err != nil {
		t.Fatalf("rememberWebTTYKnownServerClientIdentity() error = %v", err)
	}
	after, err := webtty.ReadKnownServerKeysFile(knownServersPath)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() after error = %v", err)
	}
	if after.KnownServers[0].ClientIdentity != "review-client" {
		t.Fatalf("client identity after remember = %q", after.KnownServers[0].ClientIdentity)
	}
}
func TestUIWebTTYSessionOpensWorkspaceManagedE2ERuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interactive PTY allocation is not supported on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-runtime"
	projectID := "project-runtime"
	serverID := "server-runtime"
	projectEndpoint := "project-runtime-endpoint"
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-runtime"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	enrollment := &webTTYServerEnrollmentFile{
		Version:                         webTTYServerEnrollmentVersion,
		ServerID:                        serverID,
		WorkspaceID:                     workspaceID,
		ProjectID:                       projectID,
		ServerPublicKey:                 webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey),
		ServerSigningKeyID:              webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.KeyID),
		ServerSigningPublicKey:          webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.PublicKey),
		ServerFingerprint:               webTTYServerPublicKeyFingerprint(serverIdentity.Encryption.PublicKey),
		ServerKeyAlgorithm:              webTTYServerKeyAlgorithmX25519,
		WorkspaceTrustKeysetID:          "keyset-1",
		WorkspaceTrustKeysetFingerprint: keysetBundle.Fingerprint,
		WorkspaceTrustPublicSigningKey:  keysetBundle.PublicSigningKey,
		EncryptionPolicy:                webTTYServerEncryptionPolicyWorkspaceManaged,
		EnrollmentStatus:                webTTYServerEnrollmentStatusOK,
	}
	allowUnauthenticated := true
	requireSessionKeyGrant := true
	requireClientProof := true
	heartbeat := time.Duration(0)
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{
		AllowUnauthenticated:   &allowUnauthenticated,
		HeartbeatInterval:      &heartbeat,
		PayloadCryptoResolver:  webtty.NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &requireSessionKeyGrant,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &requireClientProof,
		ClientProofVerifier:    webTTYWorkspaceClientProofVerifier(enrollment),
		WorkspaceID:            workspaceID,
		ProjectID:              projectID,
		ServerID:               serverID,
	})
	terminalServer := httptest.NewServer(handler)
	defer terminalServer.Close()
	defer handler.Shutdown(t.Context())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	var seen controlplane.ResolveWebTTYServerClientRequest
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-runtime/webtty/servers/server-runtime/client-resolution":
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if len(seen.DeviceProofs) != 1 || seen.DeviceProofs[0].DeviceFingerprint != device.Fingerprint {
				http.Error(w, "missing device proof", http.StatusBadRequest)
				return
			}
			signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
			payload := workspaceDeviceLookupPayload(workspaceID, device.Fingerprint, seen.DeviceProofs[0].Challenge, seen.DeviceProofs[0].SignedAt)
			if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.DeviceProofs[0].Signature) {
				http.Error(w, "invalid device proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
				ServerID:               serverID,
				WorkspaceID:            workspaceID,
				ProjectID:              projectID,
				EncryptionPolicy:       "workspace_managed",
				E2ERequired:            true,
				ServerPublicKey:        &serverPublicKey,
				ServerEndpointIdentity: &serverEndpointIdentity,
				ServerKeyAlgorithm:     rstream.StringPtr("x25519-hkdf-sha256-aes-256-gcm"),
				CurrentDevice:          testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle),
			})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer controlServer.Close()
	app := &uiApp{
		runtime: &resolvedRuntime{Resolved: config.Resolved{
			APIURL: controlServer.URL,
			Token:  "token",
			Context: &config.Context{
				ProjectEndpoint: projectEndpoint,
			},
		}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	plan, selection, err := app.webTTYSessionConfig(ctx, webtty.ServerInfo{
		Target:           "prod-shell",
		RstreamURL:       "ws" + strings.TrimPrefix(terminalServer.URL, "http"),
		ServerID:         &serverID,
		WorkspaceID:      &workspaceID,
		ProjectID:        &projectID,
		E2E:              rstream.StringPtr(webtty.WebTTYE2ERequired),
		ClientProof:      rstream.StringPtr(webtty.WebTTYClientProofRequired),
		EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyWorkspaceManaged),
	}, slog.Default(), "", false)
	if err != nil {
		t.Fatalf("webTTYSessionConfig() error = %v", err)
	}
	if selection != nil {
		t.Fatalf("workspace-managed runtime requested identity selection: %#v", selection)
	}
	cfg := plan.config
	session, err := webtty.OpenClientSession(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	if err := session.SendText("echo ui-e2e\nexit\n"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectUITestSessionOutput(session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v, stderr=%q", exitCode, err, stderr)
	}
	if !strings.Contains(stdout, "ui-e2e") {
		t.Fatalf("stdout=%q, want ui-e2e", stdout)
	}
	if cfg.PayloadCrypto == nil || cfg.PayloadCrypto.SessionKeyGrant == nil {
		t.Fatalf("expected E2E payload crypto")
	}
}

func TestUIShutdownCancelsWatcherAndClosesRuntimeClient(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
	}()
	transport := &webTTYCloseTrackingTransport{}
	app := &uiApp{cancel: cancel, runtimeCancel: cancel, runtimeClient: ownRstreamClient(newWebTTYOwnedTestClient(t, transport)), runtimeDone: done}
	app.shutdown()
	if transport.closeCalls.Load() != 1 {
		t.Fatalf("runtime transport closes = %d, want 1", transport.closeCalls.Load())
	}
	select {
	case <-done:
	default:
		t.Fatal("runtime watcher did not stop")
	}
}

func TestUIShutdownDiscardsQueuedUpdateWithoutEventLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init() error = %v", err)
	}
	defer screen.Fini()
	applied := make(chan struct{}, 1)
	discarded := make(chan struct{}, 1)
	posted := make(chan bool, 1)
	app := &uiApp{ctx: ctx, cancel: cancel, screen: screen}
	if !app.async.Go(func() {
		posted <- app.postUpdate(ctx, uiUpdate{apply: func() { applied <- struct{}{} }, discard: func() { discarded <- struct{}{} }})
	}) {
		t.Fatal("async update was rejected before shutdown")
	}
	if ok := <-posted; !ok {
		t.Fatal("postUpdate() did not enqueue the update")
	}
	app.shutdown()
	select {
	case <-applied:
		t.Fatal("queued update ran without an event loop")
	default:
	}
	select {
	case <-discarded:
	default:
		t.Fatal("shutdown did not discard the queued update")
	}
}

func TestUIAsyncGroupRejectsWorkOnceShutdownStarts(t *testing.T) {
	var group uiAsyncGroup
	started := make(chan struct{}, 64)
	release := make(chan struct{})
	for index := 0; index < 64; index++ {
		if !group.Go(func() { started <- struct{}{}; <-release }) {
			t.Fatalf("Go() rejected worker %d", index)
		}
	}
	for index := 0; index < 64; index++ {
		<-started
	}
	stopped := make(chan struct{})
	go func() { group.StopAndWait(); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for {
		group.mu.Lock()
		stopping := group.stopped
		group.mu.Unlock()
		if stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("StopAndWait() did not enter stopping state")
		}
		time.Sleep(time.Millisecond)
	}
	if group.Go(func() {}) {
		t.Fatal("Go() accepted work after shutdown started")
	}
	select {
	case <-stopped:
		t.Fatal("StopAndWait() returned before active workers stopped")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("StopAndWait() did not wait for active workers")
	}
}

func collectUITestSessionOutput(session *webtty.ClientSession) (string, string, int, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			switch event.Stream {
			case webtty.ClientSessionStdout:
				stdout.Write(event.Data)
			case webtty.ClientSessionStderr:
				stderr.Write(event.Data)
			}
		}
	}()
	exitCode, err := session.Wait()
	<-done
	return stdout.String(), stderr.String(), exitCode, err
}
func newUITestSessionHandle() *uiSessionHandle {
	return &uiSessionHandle{
		showInfo: true,
		content:  tview.NewFlex(),
		view:     &uiTerminalView{Box: tview.NewBox(), term: vt.NewSafeEmulator(80, 24)},
		info:     tview.NewTextView(),
		meta:     tview.NewTextView(),
		header:   tview.NewTextView(),
		actions:  tview.NewTextView(),
		message:  tview.NewTextView(),
	}
}

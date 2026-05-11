// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rstreamlabs/rstream-go"
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

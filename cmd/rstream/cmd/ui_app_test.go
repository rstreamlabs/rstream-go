// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func TestUIRuntimeOptionsFromCommandUsesExplicitSelectionAndEnvironment(t *testing.T) {
	t.Setenv("RSTREAM_API_URL", "https://env.example/")
	t.Setenv("RSTREAM_CONTEXT", "env-context")
	t.Setenv("RSTREAM_ENGINE", "engine.example:443")
	command := &cobra.Command{Use: "test"}
	command.Flags().String("api-url", "", "")
	command.Flags().String("context", "", "")
	command.Flags().String("tunnel-transport", "", "")
	if err := command.Flags().Set("api-url", "https://flag.example/"); err != nil {
		t.Fatalf("set api-url: %v", err)
	}
	if err := command.Flags().Set("context", "flag-context"); err != nil {
		t.Fatalf("set context: %v", err)
	}
	if err := command.Flags().Set("tunnel-transport", "tls"); err != nil {
		t.Fatalf("set tunnel-transport: %v", err)
	}
	options := uiRuntimeOptionsFromCommand(command)
	if options.apiURLScope != "https://flag.example" || options.contextOverride != "flag-context" || options.tunnelTransport != "tls" {
		t.Fatalf("options = %#v", options)
	}
	if options.environment.Engine != "engine.example:443" {
		t.Fatalf("environment = %#v", options.environment)
	}
}

func TestUITargetPickerLifecycleAndKeyboardInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "device", Engine: "device.example:443", Auth: testUIInlineAuth("token")}
	cfg := config.Config{Contexts: []config.Context{contextValue}}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	app := &uiApp{
		ctx:        ctx,
		cancel:     cancel,
		app:        tview.NewApplication(),
		pages:      tview.NewPages(),
		table:      tview.NewTable(),
		runtime:    &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: config.Resolved{Context: &contextValue}},
		resolver:   newUIRuntimeResolver(path, uiRuntimeOptions{}),
		store:      newUIStore("sse"),
		activePage: uiPageInventory,
	}
	app.showTargetPicker()
	if app.activePage != uiPageTargetPicker || app.targetPicker == nil || len(app.targetPicker.targets) != 1 {
		t.Fatalf("target picker was not opened: page=%q picker=%#v", app.activePage, app.targetPicker)
	}
	if got := app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone)); got != nil || app.app.GetFocus() != app.targetPicker.filter {
		t.Fatalf("filter shortcut result=%#v focus=%T", got, app.app.GetFocus())
	}
	if got := app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone)); got == nil {
		t.Fatal("filter input intercepted printable text")
	}
	if got := app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)); got != nil || app.app.GetFocus() != app.targetPicker.table {
		t.Fatalf("filter Enter result=%#v focus=%T", got, app.app.GetFocus())
	}
	if got := app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone)); got != nil || app.targetPicker.mode != uiTargetProject {
		t.Fatalf("project view shortcut result=%#v mode=%q", got, app.targetPicker.mode)
	}
	if got := app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)); got != nil || app.targetPicker.mode != uiTargetContext {
		t.Fatalf("view toggle result=%#v mode=%q", got, app.targetPicker.mode)
	}
	app.targetPicker.filter.SetText("missing")
	app.renderTargetPicker(app.targetPicker)
	row, column := app.targetPicker.table.GetSelection()
	if row != 0 || column != 0 {
		t.Fatalf("empty picker selection = (%d, %d), want (0, 0)", row, column)
	}
	rowsSelectable, columnsSelectable := app.targetPicker.table.GetSelectable()
	if rowsSelectable || columnsSelectable {
		t.Fatalf("empty picker selectable = (%v, %v), want disabled", rowsSelectable, columnsSelectable)
	}
	assertUITableNavigationReturns(t, app.targetPicker.table)
	app.selectTargetPickerTarget(false)
	if !strings.Contains(app.targetPicker.status.GetText(false), "No context") {
		t.Fatalf("empty selection status = %q", app.targetPicker.status.GetText(false))
	}
	if got := app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)); got != nil || app.targetPicker != nil || app.activePage != uiPageInventory {
		t.Fatalf("Escape did not close picker: result=%#v page=%q picker=%#v", got, app.activePage, app.targetPicker)
	}
}

func TestUITargetPickerDoesNotOverlapRuntimeSwitch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pendingCtx, pendingCancel := context.WithCancel(t.Context())
	defer pendingCancel()
	app := &uiApp{ctx: ctx, app: tview.NewApplication(), switchCancel: pendingCancel, switchGen: 2}
	app.showTargetPicker()
	if app.targetPicker != nil || app.switchGen != 2 {
		t.Fatalf("overlapping picker state = picker %#v generation %d", app.targetPicker, app.switchGen)
	}
	if !strings.Contains(app.state.Message, "current context switch") {
		t.Fatalf("overlapping picker message = %q", app.state.Message)
	}
	select {
	case <-pendingCtx.Done():
		t.Fatal("opening picker canceled the active switch")
	default:
	}
}

func TestUISwitchTargetRejectsActiveSessionAndCancelsWithApp(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	app := &uiApp{ctx: ctx, cancel: cancel, app: tview.NewApplication(), store: newUIStore("sse"), session: &uiSessionHandle{}, activePage: uiPageSession}
	app.switchTarget(uiTarget{Kind: uiTargetContext, Context: config.Context{Name: "target"}}, false)
	if !strings.Contains(app.state.Message, "Close the WebTTY session") {
		t.Fatalf("session switch message = %q", app.state.Message)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "target", Engine: "target.example:443", Auth: testUIInlineAuth("token")}
	if err := config.WriteAtomic(path, config.Config{Contexts: []config.Context{contextValue}}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	app.session = nil
	app.activePage = uiPageInventory
	app.resolver = newUIRuntimeResolver(path, uiRuntimeOptions{})
	app.readyTimeout = time.Second
	cancel()
	app.switchTarget(uiTarget{Kind: uiTargetContext, Context: contextValue}, false)
	if app.switchGen != 0 || app.switchingTo != "" || app.state.Message != "The UI is shutting down" {
		t.Fatalf("canceled switch state = gen %d target %q message %q", app.switchGen, app.switchingTo, app.state.Message)
	}
}

func TestUISwitchTargetActivatesPreparedRuntimeInRunningApplication(t *testing.T) {
	server := newUIInventoryServer(t, 200)
	defer server.Close()
	path, _ := writeUIRuntimeTarget(t, server.URL)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	oldRuntimeCtx, oldRuntimeCancel := context.WithCancel(ctx)
	app := &uiApp{
		ctx:           ctx,
		cancel:        cancel,
		client:        &rstream.Client{},
		store:         newUIStore("sse"),
		runtime:       &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: config.Resolved{ContextName: "old"}},
		resolver:      newUIRuntimeResolver(path, uiRuntimeOptions{}),
		connection:    uiConnectionInfo{ContextName: "old"},
		app:           tview.NewApplication(),
		pages:         tview.NewPages(),
		state:         uiState{Detail: uiDetailModeSummary},
		activePage:    uiPageInventory,
		runtimeCancel: oldRuntimeCancel,
		runtimeGen:    1,
		readyTimeout:  2 * time.Second,
	}
	app.buildInventoryPage()
	app.buildHelpPage()
	app.pages.AddPage(uiPageInventory, app.inventoryPage(), true, true)
	app.pages.AddPage(uiPageHelp, uiCenteredPrimitive(app.help, 84, 14), true, false)
	screen := tcell.NewSimulationScreen("UTF-8")
	app.screen = screen
	app.app.SetScreen(screen).SetRoot(app.pages, true).SetFocus(app.table).SetInputCapture(app.captureInput)
	runDone := make(chan error, 1)
	go func() {
		runDone <- app.Run()
	}()
	resizeDone := make(chan error, 1)
	go func() {
		for index := 0; index < 24; index++ {
			width := 120 + index%7
			height := 36 + index%5
			screen.SetSize(width, height)
			if err := postUIEventWithBackpressure(screen, tcell.NewEventResize(width, height), 2*time.Second); err != nil {
				resizeDone <- err
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		screen.SetSize(172, 56)
		resizeDone <- postUIEventWithBackpressure(screen, tcell.NewEventResize(172, 56), 2*time.Second)
	}()
	pendingState := make(chan string, 1)
	app.app.QueueUpdateDraw(func() {
		app.showTargetPicker()
		app.captureTargetPickerInput(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))
		cell := app.table.GetCell(1, 0)
		if cell == nil {
			pendingState <- ""
			return
		}
		pendingState <- cell.Text
	})
	if pending := <-pendingState; pending != "Switching to target..." {
		app.app.Stop()
		t.Fatalf("pending inventory row = %q, want switching placeholder", pending)
	}
	deadline := time.Now().Add(3 * time.Second)
	switched := false
	for time.Now().Before(deadline) {
		result := make(chan bool, 1)
		app.app.QueueUpdateDraw(func() {
			result <- app.runtime != nil && app.runtime.Resolved.ContextName == "target" && app.switchingTo == ""
		})
		if <-result {
			switched = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !switched {
		app.app.Stop()
		t.Fatal("running application did not activate the prepared runtime")
	}
	if err := <-resizeDone; err != nil {
		app.app.Stop()
		t.Fatalf("post resize event: %v", err)
	}
	responsive := make(chan struct{})
	go func() {
		app.app.QueueUpdateDraw(func() {})
		close(responsive)
	}()
	select {
	case <-responsive:
	case <-time.After(2 * time.Second):
		app.app.Stop()
		t.Fatal("UI event loop stopped responding after switch and resize")
	}
	type screenState struct {
		width    int
		height   int
		hasTitle bool
	}
	screenStateCh := make(chan screenState, 1)
	app.app.QueueUpdateDraw(func() {
		cells, width, height := screen.GetContents()
		screenStateCh <- screenState{width: width, height: height, hasTitle: simulationScreenContains(cells, "rstream ui")}
	})
	snapshot := <-screenStateCh
	if snapshot.width != 172 || snapshot.height != 56 || !snapshot.hasTitle {
		app.app.Stop()
		t.Fatalf("resized screen = %dx%d, complete header=%v", snapshot.width, snapshot.height, snapshot.hasTitle)
	}
	persisted, err := config.Load(path)
	if err != nil {
		app.app.Stop()
		t.Fatalf("Load(persisted) error = %v", err)
	}
	if persisted.Defaults.Context == nil || persisted.Defaults.Context.Name != "target" {
		app.app.Stop()
		t.Fatalf("persistent picker default = %#v, want target", persisted.Defaults.Context)
	}
	select {
	case <-oldRuntimeCtx.Done():
	default:
		app.app.Stop()
		t.Fatal("running application did not cancel the old runtime")
	}
	app.app.Stop()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("ui Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ui Run() did not stop")
	}
}

func postUIEventWithBackpressure(screen tcell.Screen, event tcell.Event, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := screen.PostEvent(event)
		if err == nil || !errors.Is(err, tcell.ErrEventQFull) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

func simulationScreenContains(cells []tcell.SimCell, text string) bool {
	var content strings.Builder
	for _, cell := range cells {
		if len(cell.Runes) == 0 {
			content.WriteRune(' ')
			continue
		}
		content.WriteRune(cell.Runes[0])
	}
	return strings.Contains(content.String(), text)
}

func TestUIContextLookupAndProjectTargetHandleAmbiguityAndCurrentState(t *testing.T) {
	selected := config.Context{Name: "duplicate", APIURL: "https://api.example", Engine: "second.example:443", ProjectEndpoint: "project-2"}
	cfg := config.Config{Contexts: []config.Context{
		{Name: "duplicate", APIURL: "https://api.example", Engine: "first.example:443", ProjectEndpoint: "project-1"},
		selected,
	}}
	contextValue, err := findUITargetContext(&cfg, selected)
	if err != nil || contextValue.Engine != "second.example:443" {
		t.Fatalf("findUITargetContext() = %#v, %v", contextValue, err)
	}
	selected.Engine = "missing.example:443"
	if _, err := findUITargetContext(&cfg, selected); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("findUITargetContext() error = %v, want ambiguous", err)
	}
	if _, err := findUITargetContext(nil, selected); err == nil {
		t.Fatal("findUITargetContext() accepted nil config")
	}
	if _, err := findUITargetContext(&config.Config{}, selected); err == nil || !strings.Contains(err.Error(), "no longer configured") {
		t.Fatalf("missing context error = %v", err)
	}
	runtimeContext := cfg.Contexts[1]
	runtime := &resolvedRuntime{Resolved: config.Resolved{Context: &runtimeContext}}
	cfg.Defaults.Context = &config.DefaultContext{Name: "duplicate"}
	project := controlplane.Project{ID: "project-2", Endpoint: "project-2", Name: "Project"}
	target := newUIRuntimeResolver("unused", uiRuntimeOptions{}).projectTarget(cfg, runtime, "https://api.example", project, "Workspace")
	if !target.Current || !target.Default || target.Context.Engine != "second.example:443" || !strings.HasPrefix(target.stableID(), "project|") {
		t.Fatalf("project target = %#v", target)
	}
}

func TestUIActivateRuntimeRejectsIncompleteResult(t *testing.T) {
	app := &uiApp{app: tview.NewApplication()}
	app.activateRuntime(nil)
	if !strings.Contains(app.state.Message, "incomplete") {
		t.Fatalf("activation message = %q", app.state.Message)
	}
}

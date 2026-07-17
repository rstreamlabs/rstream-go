// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
)

const (
	uiPageInventory      = "inventory"
	uiPageSession        = "session"
	uiPageHelp           = "help"
	uiPageIdentityPicker = "identity-picker"
	uiPageTargetPicker   = "target-picker"
)

var (
	uiBorderOnce      sync.Once
	uiColorBackground = tcell.NewRGBColor(0x0b, 0x0f, 0x14)
	uiColorPanel      = tcell.NewRGBColor(0x12, 0x18, 0x20)
	uiColorBorder     = tcell.NewRGBColor(0x4d, 0x5a, 0x6d)
	uiColorText       = tcell.NewRGBColor(0xf3, 0xf4, 0xf6)
	uiColorMuted      = tcell.NewRGBColor(0xc4, 0xcd, 0xd8)
	uiColorSelection  = tcell.NewRGBColor(0x3a, 0x47, 0x58)
	uiColorDanger     = tcell.NewRGBColor(0xf2, 0x7a, 0x7a)
	uiColorChip       = tcell.NewRGBColor(0x24, 0x2d, 0x38)
	uiColorChipText   = tcell.NewRGBColor(0xdd, 0xe3, 0xea)
	uiColorChipActive = tcell.NewRGBColor(0xe5, 0xea, 0xf1)
	uiColorChipKey    = tcell.NewRGBColor(0xe0, 0xe6, 0xee)
)

type uiApp struct {
	ctx           context.Context
	cancel        context.CancelFunc
	client        *rstream.Client
	store         *uiStore
	runtime       *resolvedRuntime
	resolver      *uiRuntimeResolver
	connection    uiConnectionInfo
	app           *tview.Application
	screen        tcell.Screen
	pages         *tview.Pages
	headerMeta    *tview.TextView
	headerTabs    *tview.TextView
	table         *tview.Table
	detail        *tview.TextView
	footerActions *tview.TextView
	footerMessage *tview.TextView
	help          *tview.TextView
	state         uiState
	snapshot      uiSnapshot
	viewChosen    bool
	clientRows    []rstream.ClientProperties
	tunnelRows    []rstream.TunnelInventory
	webttyRows    []webtty.ServerInfo
	session       *uiSessionHandle
	helpVisible   bool
	activePage    string
	sessionLeader bool
	runtimeCancel context.CancelFunc
	runtimeGen    uint64
	switchCancel  context.CancelFunc
	switchGen     uint64
	switchingTo   string
	targetPicker  *uiTargetPicker
	readyTimeout  time.Duration
}

type uiConnectionInfo struct {
	ContextName string
	ProjectName string
	APIURL      string
	Engine      string
	SessionOnly bool
}

type uiSessionHandle struct {
	cancel   context.CancelFunc
	server   webtty.ServerInfo
	showInfo bool
	meta     *tview.TextView
	header   *tview.TextView
	content  *tview.Flex
	view     *uiTerminalView
	info     *tview.TextView
	actions  *tview.TextView
	message  *tview.TextView
	root     tview.Primitive
}

type uiWebTTYSessionPlan struct {
	config             *webtty.SessionConfig
	rememberServerName string
	rememberIdentity   string
}

type uiWebTTYIdentitySelection struct {
	knownServerName string
	identities      []map[string]any
}

func newUIApp(ctx context.Context, cancel context.CancelFunc, client *rstream.Client, store *uiStore, runtime *resolvedRuntime, resolver *uiRuntimeResolver, connection uiConnectionInfo) (*uiApp, error) {
	initUIBorders()
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	app := &uiApp{
		ctx:          ctx,
		cancel:       cancel,
		client:       client,
		store:        store,
		runtime:      runtime,
		resolver:     resolver,
		connection:   connection,
		app:          tview.NewApplication(),
		screen:       screen,
		pages:        tview.NewPages(),
		state:        uiState{Detail: uiDetailModeSummary},
		activePage:   uiPageInventory,
		readyTimeout: 20 * time.Second,
	}
	if app.resolver == nil {
		app.resolver = newUIRuntimeResolver(runtime.ConfigPath, uiRuntimeOptions{})
	}
	app.buildInventoryPage()
	app.buildHelpPage()
	app.pages.AddPage(uiPageInventory, app.inventoryPage(), true, true)
	app.pages.AddPage(uiPageHelp, uiCenteredPrimitive(app.help, 84, 14), true, false)
	app.app.SetTitle("rstream ui")
	app.app.SetScreen(screen)
	app.app.EnableMouse(true)
	app.app.EnablePaste(true)
	app.app.SetRoot(app.pages, true)
	app.app.SetFocus(app.table)
	app.app.SetInputCapture(app.captureInput)
	app.refreshSnapshot(store.snapshot())
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	app.runtimeCancel = runtimeCancel
	app.runtimeGen = 1
	go store.run(runtimeCtx, client, nil)
	go app.watchStore(runtimeCtx, app.runtimeGen, store)
	return app, nil
}

func initUIBorders() {
	uiBorderOnce.Do(func() {
		tview.Borders.HorizontalFocus = tview.Borders.Horizontal
		tview.Borders.VerticalFocus = tview.Borders.Vertical
		tview.Borders.TopLeftFocus = tview.Borders.TopLeft
		tview.Borders.TopRightFocus = tview.Borders.TopRight
		tview.Borders.BottomLeftFocus = tview.Borders.BottomLeft
		tview.Borders.BottomRightFocus = tview.Borders.BottomRight
	})
}

func (u *uiApp) Run() error {
	defer u.shutdown()
	return u.app.Run()
}

func (u *uiApp) shutdown() {
	u.closeSession("")
	if u.switchCancel != nil {
		u.switchCancel()
		u.switchCancel = nil
	}
	if u.runtimeCancel != nil {
		u.runtimeCancel()
		u.runtimeCancel = nil
	}
	u.closeTargetPicker()
}

func (u *uiApp) buildInventoryPage() {
	u.headerMeta = newUITextLine(uiColorText)
	u.headerTabs = newUITextLine(uiColorText)
	u.footerActions = newUITextLine(uiColorMuted)
	u.footerMessage = newUITextLine(uiColorMuted)
	u.table = tview.NewTable().SetBorders(false).SetFixed(1, 0).SetSelectable(true, false).SetSeparator(' ')
	u.table.SetBackgroundColor(uiColorPanel)
	u.table.SetBorder(true).SetBorderColor(uiColorBorder).SetTitle(" Resources ").SetTitleColor(uiColorText)
	u.table.SetSelectedStyle(tcell.StyleDefault.Foreground(uiColorText).Background(uiColorSelection))
	u.table.SetSelectionChangedFunc(func(row, column int) {
		u.handleSelectionChanged(row)
	})
	u.table.SetSelectedFunc(func(row, column int) {
		if u.state.View == uiViewWebTTY {
			u.openSelectedWebTTY()
		}
	})
	u.detail = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(false).SetWordWrap(false).SetTextColor(uiColorText)
	u.detail.SetBackgroundColor(uiColorPanel)
	u.detail.SetBorder(true).SetBorderColor(uiColorBorder).SetTitle(" Details ").SetTitleColor(uiColorText)
	u.help = tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetWordWrap(true).SetTextColor(uiColorText)
	u.help.SetBackgroundColor(uiColorPanel)
	u.help.SetBorder(true).SetBorderColor(uiColorBorder).SetTitle(" Help ").SetTitleColor(uiColorText)
	u.help.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter, tcell.KeyEscape, tcell.KeyF1:
			u.hideHelp()
			return nil
		}
		if event.Key() == tcell.KeyRune {
			switch unicodeLower(event.Rune()) {
			case '?', 'q':
				u.hideHelp()
				return nil
			}
		}
		return event
	})
}

func newUITextLine(color tcell.Color) *tview.TextView {
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetWordWrap(false).SetTextColor(color)
	view.SetBackgroundColor(uiColorBackground)
	return view
}

func (u *uiApp) buildHelpPage() {
	u.help.SetText(u.helpText())
}

func (u *uiApp) inventoryPage() tview.Primitive {
	content := tview.NewFlex().AddItem(u.table, 0, 2, true).AddItem(u.detail, 0, 1, false)
	content.SetBackgroundColor(uiColorBackground)
	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(u.headerMeta, 1, 0, false).
		AddItem(u.headerTabs, 1, 0, false).
		AddItem(content, 0, 1, true).
		AddItem(u.footerActions, 1, 0, false).
		AddItem(u.footerMessage, 1, 0, false)
	root.SetBackgroundColor(uiColorBackground)
	return root
}

func (u *uiApp) buildSessionPage(handle *uiSessionHandle) tview.Primitive {
	handle.content = tview.NewFlex()
	handle.content.SetBackgroundColor(uiColorBackground)
	u.refreshSessionLayout(handle)
	root := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(handle.meta, 1, 0, false).
		AddItem(handle.header, 1, 0, false).
		AddItem(handle.content, 0, 1, true).
		AddItem(handle.actions, 1, 0, false).
		AddItem(handle.message, 1, 0, false)
	root.SetBackgroundColor(uiColorBackground)
	return root
}

func (u *uiApp) watchStore(ctx context.Context, generation uint64, store *uiStore) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-store.Changes():
			snapshot := store.snapshot()
			u.app.QueueUpdateDraw(func() {
				if u.runtimeGen != generation || u.store != store {
					return
				}
				u.refreshSnapshot(snapshot)
			})
		}
	}
}

func (u *uiApp) refreshSnapshot(snapshot uiSnapshot) {
	u.snapshot = snapshot
	if u.state.View == "" {
		u.state.View = defaultUIView(snapshot)
	} else if !u.viewChosen {
		u.state.View = defaultUIView(snapshot)
	}
	if u.activePage == uiPageInventory {
		u.renderInventory()
	}
	u.refreshChrome()
	if u.helpVisible {
		u.help.SetText(u.helpText())
	}
}

func (u *uiApp) refreshChrome() {
	meta := u.inventoryMetaText()
	if u.headerMeta != nil {
		u.headerMeta.SetText(meta)
	}
	if u.headerTabs != nil {
		u.headerTabs.SetText(u.inventoryTabsText())
	}
	if u.footerActions != nil {
		u.footerActions.SetText(u.inventoryActionsText())
	}
	if u.footerMessage != nil {
		u.footerMessage.SetText(u.inventoryMessageText())
	}
	if u.session != nil {
		if u.session.meta != nil {
			u.session.meta.SetText(meta)
		}
		if u.session.header != nil {
			u.session.header.SetText(sessionHeaderText(u.session.server))
		}
		if u.session.actions != nil {
			u.session.actions.SetText(sessionActionsText())
		}
		if u.session.message != nil {
			u.session.message.SetText(u.sessionMessageText())
		}
	}
}

func (u *uiApp) inventoryMetaText() string {
	if target := strings.TrimSpace(u.switchingTo); target != "" {
		return strings.Join([]string{
			"[white::b]rstream ui[-:-:-]",
			"[#b0bac5]https://rstream.io/[-]",
			"[#b0bac5]switching to " + uiSafe(target) + "[-]",
		}, " [#b0bac5]-[-] ")
	}
	status := "[#9ca3af]connecting[-]"
	if u.snapshot.Connected {
		status = "[white]connected[-]"
	}
	if strings.TrimSpace(u.snapshot.LastError) != "" {
		status = "[#f27a7a]reconnecting[-]"
	}
	parts := []string{
		"[white::b]rstream ui[-:-:-]",
		"[#b0bac5]https://rstream.io/[-]",
		status,
	}
	if contextName := strings.TrimSpace(u.connection.ContextName); contextName != "" {
		label := fmt.Sprintf("ctx %s", uiSafe(contextName))
		if u.connection.SessionOnly {
			label += " (session)"
		}
		parts = append(parts, "[#b0bac5]"+label+"[-]")
	}
	if projectName := strings.TrimSpace(u.connection.ProjectName); projectName != "" {
		parts = append(parts, fmt.Sprintf("[#b0bac5]project %s[-]", uiSafe(projectName)))
	}
	if engine := strings.TrimSpace(u.connection.Engine); engine != "" {
		parts = append(parts, fmt.Sprintf("[#b0bac5]engine %s[-]", uiSafe(engine)))
	} else if apiURL := strings.TrimSpace(u.connection.APIURL); apiURL != "" {
		parts = append(parts, fmt.Sprintf("[#b0bac5]api %s[-]", uiSafe(apiURL)))
	}
	return strings.Join(parts, " [#b0bac5]-[-] ")
}

func (u *uiApp) sessionMessageText() string {
	if message := strings.TrimSpace(u.state.Message); message != "" {
		return uiStatusMessage(message)
	}
	if u.sessionLeader {
		return "[#b0bac5]Ctrl+g command: ? help, d details, q back, Ctrl+g send literal[-]"
	}
	return " "
}

func (u *uiApp) inventoryTabsText() string {
	return strings.Join([]string{
		u.tabLabel(uiViewWebTTY, "1", "WebTTY"),
		u.tabLabel(uiViewTunnels, "2", "Tunnels"),
		u.tabLabel(uiViewClients, "3", "Clients"),
	}, "   ")
}

func (u *uiApp) inventoryActionsText() string {
	parts := []string{
		uiKeyLabel("1", "WebTTY"),
		uiKeyLabel("2", "Tunnels"),
		uiKeyLabel("3", "Clients"),
		uiKeyLabel("Tab", "Next"),
		uiKeyLabel("c", "Context/Project"),
		uiKeyLabel("v", "Summary/JSON"),
		uiKeyLabel("?", "Help"),
		uiKeyLabel("q", "Quit"),
	}
	if u.state.View == uiViewWebTTY {
		parts = append(parts, uiKeyLabel("Enter", "Connect"))
	}
	return strings.Join(parts, "   ")
}

func (u *uiApp) inventoryMessageText() string {
	return uiStatusMessage(u.state.Message)
}

func (u *uiApp) tabLabel(view uiView, key, label string) string {
	if u.state.View == view {
		return fmt.Sprintf("[black:%s:b] %s %s [-:-:-]", uiColorTag(uiColorChipActive), key, label)
	}
	return fmt.Sprintf("[%s:%s:b] %s %s [-:-:-]", uiColorTag(uiColorChipText), uiColorTag(uiColorChip), key, label)
}

func (u *uiApp) renderInventory() {
	if strings.TrimSpace(u.switchingTo) != "" {
		u.renderSwitchingInventory()
		return
	}
	switch u.state.View {
	case uiViewClients:
		u.renderClients()
	case uiViewTunnels:
		u.renderTunnels()
	default:
		u.renderWebTTY()
	}
	u.syncDetails()
}

func (u *uiApp) renderSwitchingInventory() {
	u.clientRows = nil
	u.tunnelRows = nil
	u.webttyRows = nil
	u.prepareInventoryTable(u.state.View)
	addPlaceholderRow(u.table, "Switching to "+u.switchingTo+"...")
	u.detail.SetText(" ")
}

func (u *uiApp) prepareInventoryTable(view uiView) {
	title := " WebTTY Servers "
	headers := []string{"TARGET", "STATUS", "SECURITY", "HOSTNAME", "SYSTEM", "DOMAIN/HOST"}
	switch view {
	case uiViewClients:
		title = " Clients "
		headers = []string{"ID", "STATUS", "AGENT", "VERSION", "SYSTEM"}
	case uiViewTunnels:
		title = " Tunnels "
		headers = []string{"TARGET", "STATUS", "TYPE", "PROTOCOL", "DOMAIN/HOST", "CLIENT"}
	}
	u.table.SetTitle(title)
	u.table.Clear()
	u.table.SetSelectable(false, false).Select(0, 0).SetOffset(0, 0)
	addHeaderRow(u.table, headers)
}

func (u *uiApp) renderClients() {
	u.clientRows = append([]rstream.ClientProperties(nil), u.snapshot.Clients...)
	u.prepareInventoryTable(uiViewClients)
	if len(u.clientRows) == 0 {
		addPlaceholderRow(u.table, "No clients in this project")
		return
	}
	selectedRow := 1
	for index, client := range u.clientRows {
		row := index + 1
		agent := optionalValue(client.Agent)
		version := optionalValue(client.Version)
		system := strings.TrimSpace(strings.Join([]string{optionalValue(client.OS), optionalValue(client.Arch)}, " "))
		system = strings.TrimSpace(system)
		if system == "" || system == "-" {
			system = optionalValue(client.OS)
		}
		setRow(u.table, row, []string{client.ID, client.Status, agent, version, emptyDash(system)}, client.ID)
		if client.ID == strings.TrimSpace(u.state.ClientID) {
			selectedRow = row
		}
	}
	u.table.SetSelectable(true, false).Select(selectedRow, 0)
	if strings.TrimSpace(u.state.ClientID) == "" && len(u.clientRows) > 0 {
		u.state.ClientID = u.clientRows[0].ID
	}
}

func (u *uiApp) renderTunnels() {
	u.tunnelRows = append([]rstream.TunnelInventory(nil), u.snapshot.Tunnels...)
	u.prepareInventoryTable(uiViewTunnels)
	if len(u.tunnelRows) == 0 {
		addPlaceholderRow(u.table, "No tunnels in this project")
		return
	}
	selectedRow := 1
	for index, tunnel := range u.tunnelRows {
		row := index + 1
		target := trimOptionalString(tunnel.ID)
		if value := trimOptionalString(tunnel.Name); value != "" {
			target = value
		}
		setRow(
			u.table,
			row,
			[]string{
				target,
				tunnel.Status,
				emptyDash(stringValue(tunnel.Type)),
				emptyDash(stringValue(tunnel.Protocol)),
				emptyDash(tunnelDisplayHost(tunnel.TunnelProperties)),
				emptyDash(strings.TrimSpace(tunnel.ClientID)),
			},
			trimOptionalString(tunnel.ID),
		)
		if trimOptionalString(tunnel.ID) == strings.TrimSpace(u.state.TunnelID) {
			selectedRow = row
		}
	}
	u.table.SetSelectable(true, false).Select(selectedRow, 0)
	if strings.TrimSpace(u.state.TunnelID) == "" && len(u.tunnelRows) > 0 {
		u.state.TunnelID = trimOptionalString(u.tunnelRows[0].ID)
	}
}

func (u *uiApp) renderWebTTY() {
	u.webttyRows = append([]webtty.ServerInfo(nil), u.snapshot.WebTTY...)
	u.prepareInventoryTable(uiViewWebTTY)
	if len(u.webttyRows) == 0 {
		addPlaceholderRow(u.table, "No WebTTY servers are currently available")
		return
	}
	selectedRow := 1
	for index, server := range u.webttyRows {
		row := index + 1
		setRow(
			u.table,
			row,
			[]string{
				server.Target,
				server.Status,
				webTTYSecuritySummary(server),
				webTTYValue(server.Hostname),
				webTTYSystem(server),
				webTTYValue(server.Host),
			},
			server.TunnelID,
		)
		if server.TunnelID == strings.TrimSpace(u.state.TunnelID) {
			selectedRow = row
		}
	}
	u.table.SetSelectable(true, false).Select(selectedRow, 0)
	if strings.TrimSpace(u.state.TunnelID) == "" && len(u.webttyRows) > 0 {
		u.state.TunnelID = u.webttyRows[0].TunnelID
	}
}

func (u *uiApp) handleSelectionChanged(row int) {
	if row <= 0 {
		return
	}
	switch u.state.View {
	case uiViewClients:
		if row-1 >= 0 && row-1 < len(u.clientRows) {
			u.state.ClientID = u.clientRows[row-1].ID
		}
	case uiViewTunnels:
		if row-1 >= 0 && row-1 < len(u.tunnelRows) {
			u.state.TunnelID = trimOptionalString(u.tunnelRows[row-1].ID)
		}
	case uiViewWebTTY:
		if row-1 >= 0 && row-1 < len(u.webttyRows) {
			u.state.TunnelID = u.webttyRows[row-1].TunnelID
		}
	}
	u.syncDetails()
}

func (u *uiApp) syncDetails() {
	switch u.state.View {
	case uiViewClients:
		if client, ok := u.selectedClient(); ok {
			u.detail.SetText(u.formatClientDetail(client))
			return
		}
	case uiViewTunnels:
		if tunnel, ok := u.selectedTunnel(); ok {
			u.detail.SetText(u.formatTunnelDetail(tunnel))
			return
		}
	default:
		if item, ok := u.selectedWebTTY(); ok {
			u.detail.SetText(u.formatWebTTYDetail(item))
			return
		}
	}
	u.detail.SetText("No resource selected.")
}

func (u *uiApp) selectedClient() (rstream.ClientProperties, bool) {
	target := strings.TrimSpace(u.state.ClientID)
	for _, client := range u.clientRows {
		if client.ID == target {
			return client, true
		}
	}
	return rstream.ClientProperties{}, false
}

func (u *uiApp) selectedTunnel() (rstream.TunnelInventory, bool) {
	target := strings.TrimSpace(u.state.TunnelID)
	for _, tunnel := range u.tunnelRows {
		if trimOptionalString(tunnel.ID) == target {
			return tunnel, true
		}
	}
	return rstream.TunnelInventory{}, false
}

func (u *uiApp) selectedWebTTY() (webtty.ServerInfo, bool) {
	target := strings.TrimSpace(u.state.TunnelID)
	for _, server := range u.webttyRows {
		if server.TunnelID == target {
			return server, true
		}
	}
	return webtty.ServerInfo{}, false
}

func (u *uiApp) openSelectedWebTTY() {
	server, ok := u.selectedWebTTY()
	if !ok {
		u.setMessage("No WebTTY server selected")
		return
	}
	u.openWebTTY(server, "", false)
}

func (u *uiApp) openWebTTY(server webtty.ServerInfo, selectedIdentity string, rememberIdentity bool) {
	if strings.ToLower(strings.TrimSpace(server.Status)) != "online" {
		u.setMessage(fmt.Sprintf("WebTTY server %s is not online", server.Target))
		return
	}
	u.closeSession("")
	sessionCtx, sessionCancel := context.WithCancel(u.ctx)
	logger := slog.With("cmd", "ui", "component", "webtty")
	sessionPlan, selection, err := u.webTTYSessionConfig(sessionCtx, server, logger, selectedIdentity, rememberIdentity)
	if err != nil {
		sessionCancel()
		u.setMessage(fmt.Sprintf("Failed to configure %s: %v", server.Target, err))
		return
	}
	if selection != nil {
		sessionCancel()
		u.showWebTTYIdentityPicker(server, selection)
		return
	}
	session, err := webtty.OpenClientSession(sessionCtx, sessionPlan.config)
	if err != nil {
		sessionCancel()
		u.setMessage(fmt.Sprintf("Failed to connect to %s: %v", server.Target, err))
		return
	}
	rememberMessage := ""
	if strings.TrimSpace(sessionPlan.rememberServerName) != "" && strings.TrimSpace(sessionPlan.rememberIdentity) != "" {
		if err := rememberWebTTYKnownServerClientIdentity(sessionPlan.rememberServerName, sessionPlan.rememberIdentity); err != nil {
			rememberMessage = fmt.Sprintf("Connected. Could not remember identity: %v", err)
		} else {
			rememberMessage = fmt.Sprintf("Connected. Remembered identity %s for %s.", sessionPlan.rememberIdentity, sessionPlan.rememberServerName)
		}
	}
	handle := &uiSessionHandle{
		cancel:   sessionCancel,
		server:   server,
		showInfo: true,
		meta:     newUITextLine(uiColorText),
		header:   newUITextLine(uiColorText),
		actions:  newUITextLine(uiColorMuted),
		message:  newUITextLine(uiColorMuted),
	}
	handle.info = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(false).SetWordWrap(false).SetTextColor(uiColorText)
	handle.info.SetBackgroundColor(uiColorPanel)
	handle.info.SetBorder(true).SetBorderColor(uiColorBorder).SetTitle(" Details ").SetTitleColor(uiColorText)
	handle.info.SetText(formatSessionInfo(server))
	handle.view = newUITerminalView(u.app, session, u.copyTerminalSelection)
	handle.root = u.buildSessionPage(handle)
	u.session = handle
	u.state.Message = rememberMessage
	u.sessionLeader = false
	u.pages.AddAndSwitchToPage(uiPageSession, handle.root, true)
	u.activePage = uiPageSession
	u.app.SetFocus(handle.view)
	u.refreshChrome()
	sessionID := server.TunnelID
	go func() {
		exitCode, err := session.Wait()
		u.app.QueueUpdateDraw(func() {
			if u.session == nil || u.session.server.TunnelID != sessionID {
				return
			}
			if err != nil {
				if contextCanceled(err) {
					u.closeSession("")
					return
				}
				u.closeSession(fmt.Sprintf("Session failed for %s: %v", server.Target, err))
				return
			}
			if exitCode > 0 {
				u.closeSession(fmt.Sprintf("Session closed for %s with exit code %d", server.Target, exitCode))
				return
			}
			u.closeSession(fmt.Sprintf("Session closed for %s", server.Target))
		})
	}()
}

func (u *uiApp) webTTYSessionConfig(ctx context.Context, server webtty.ServerInfo, logger *slog.Logger, selectedIdentity string, rememberIdentity bool) (*uiWebTTYSessionPlan, *uiWebTTYIdentitySelection, error) {
	runtimeE2E, err := webTTYClientRuntimeE2EContextFromServerInfo(ctx, u.runtime, server)
	if err != nil {
		return nil, nil, err
	}
	scope := webTTYClientSecurityScopeFromServerInfo(server.Target, &server)
	cryptoConfig, err := webTTYClientCryptoForRuntimeAndScope(ctx, runtimeE2E, scope)
	if err != nil {
		return nil, nil, err
	}
	endpointIdentity := cryptoConfig.EndpointIdentity
	if cryptoConfig.ExpectedServerIdentity != nil && endpointIdentity == nil {
		switch {
		case strings.TrimSpace(cryptoConfig.ClientIdentityName) != "":
			endpointIdentity, err = webTTYClientEndpointIdentityByName(cryptoConfig.ClientIdentityName)
			if err != nil {
				return nil, nil, err
			}
		case strings.TrimSpace(selectedIdentity) != "":
			endpointIdentity, err = webTTYClientEndpointIdentityByName(selectedIdentity)
			if err != nil {
				return nil, nil, err
			}
		default:
			identities, err := listLocalWebTTYIdentities()
			if err != nil {
				return nil, nil, err
			}
			knownServerName, err := webTTYKnownServerNameForScope(scope, cryptoConfig.ExpectedServerIdentity)
			if err != nil {
				return nil, nil, err
			}
			return nil, &uiWebTTYIdentitySelection{knownServerName: knownServerName, identities: identities}, nil
		}
	}
	plan := &uiWebTTYSessionPlan{
		config: &webtty.SessionConfig{
			URL:                    server.RstreamURL,
			DialContext:            newWebTTYClientDialContext(u.client),
			Interactive:            true,
			AllocateTTY:            true,
			SendHeartbeat:          true,
			PayloadCrypto:          cryptoConfig.PayloadCrypto,
			EndpointIdentity:       endpointIdentity,
			ClientCredential:       cryptoConfig.ClientCredential,
			ExpectedServerIdentity: cryptoConfig.ExpectedServerIdentity,
			Logger:                 logger,
		},
	}
	if cryptoConfig.ExpectedServerIdentity != nil && strings.TrimSpace(selectedIdentity) != "" && rememberIdentity {
		knownServerName, err := webTTYKnownServerNameForScope(scope, cryptoConfig.ExpectedServerIdentity)
		if err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(knownServerName) != "" {
			plan.rememberServerName = knownServerName
			plan.rememberIdentity = strings.TrimSpace(selectedIdentity)
		}
	}
	return plan, nil, nil
}

func (u *uiApp) showWebTTYIdentityPicker(server webtty.ServerInfo, selection *uiWebTTYIdentitySelection) {
	if selection == nil {
		u.setMessage("No WebTTY identity selection is available")
		return
	}
	list := tview.NewList().ShowSecondaryText(true)
	actions := make([]func(), 0)
	addAction := func(main string, secondary string, shortcut rune, action func()) {
		actions = append(actions, action)
		list.AddItem(main, secondary, shortcut, action)
	}
	list.SetBackgroundColor(uiColorPanel)
	list.SetMainTextColor(uiColorText)
	list.SetSecondaryTextColor(uiColorMuted)
	list.SetSelectedTextColor(uiColorText)
	list.SetSelectedBackgroundColor(uiColorSelection)
	list.SetBorder(true).SetBorderColor(uiColorBorder).SetTitle(" WebTTY Identity ").SetTitleColor(uiColorText)
	if len(selection.identities) == 0 {
		name := webTTYIdentitySuggestion(server)
		addAction(
			"No local WebTTY identity",
			fmt.Sprintf("Create one with: rstream webtty identity create --name %s", name),
			0,
			func() {
				u.closeIdentityPicker()
				u.setMessage(fmt.Sprintf("Create an identity first: rstream webtty identity create --name %s", name))
			},
		)
	} else {
		shortcut := '1'
		for _, item := range selection.identities {
			name := strings.TrimSpace(fmt.Sprint(item["name"]))
			signing := strings.TrimSpace(fmt.Sprint(item["signing_key_id"]))
			if name == "" {
				continue
			}
			useShortcut := rune(0)
			if shortcut <= '9' {
				useShortcut = shortcut
				shortcut++
			}
			useDetail := "Connect without changing local known-server state"
			if signing != "" && signing != "<nil>" {
				useDetail += " - signing " + signing
			}
			addAction("Use "+name+" once", useDetail, useShortcut, func(identity string) func() {
				return func() {
					u.closeIdentityPicker()
					u.openWebTTY(server, identity, false)
				}
			}(name))
			rememberShortcut := rune(0)
			if shortcut <= '9' {
				rememberShortcut = shortcut
				shortcut++
			}
			rememberDetail := "Persist only after successful authentication"
			if strings.TrimSpace(selection.knownServerName) != "" {
				rememberDetail = "Persist for " + selection.knownServerName + " after successful authentication"
			}
			addAction("Use "+name+" and remember", rememberDetail, rememberShortcut, func(identity string) func() {
				return func() {
					u.closeIdentityPicker()
					u.openWebTTY(server, identity, true)
				}
			}(name))
		}
	}
	addAction("Cancel", "Return to the WebTTY server list", 'q', func() {
		u.closeIdentityPicker()
		u.setMessage("WebTTY connection cancelled")
	})
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			runWebTTYIdentityPickerAction(list, actions)
			return nil
		case tcell.KeyEscape:
			u.closeIdentityPicker()
			u.setMessage("WebTTY connection cancelled")
			return nil
		}
		if event.Key() == tcell.KeyRune && unicodeLower(event.Rune()) == 'q' {
			u.closeIdentityPicker()
			u.setMessage("WebTTY connection cancelled")
			return nil
		}
		return event
	})
	list.SetSelectedFunc(func(int, string, string, rune) {
		runWebTTYIdentityPickerAction(list, actions)
	})
	list.SetDoneFunc(func() {
		u.closeIdentityPicker()
	})
	u.pages.AddAndSwitchToPage(uiPageIdentityPicker, uiCenteredPrimitive(list, 92, 18), true)
	u.activePage = uiPageIdentityPicker
	u.app.SetFocus(list)
}

func runWebTTYIdentityPickerAction(list *tview.List, actions []func()) {
	current := list.GetCurrentItem()
	if current >= 0 && current < len(actions) && actions[current] != nil {
		actions[current]()
	}
}

func (u *uiApp) closeIdentityPicker() {
	u.pages.RemovePage(uiPageIdentityPicker)
	u.activePage = uiPageInventory
	u.pages.SwitchToPage(uiPageInventory)
	u.app.SetFocus(u.table)
}

func webTTYIdentitySuggestion(server webtty.ServerInfo) string {
	target := strings.TrimSpace(server.Target)
	if target == "" && server.ServerID != nil {
		target = strings.TrimSpace(*server.ServerID)
	}
	if target == "" {
		target = "operator"
	}
	target = strings.ToLower(target)
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-", ":", "-", "/", "-")
	target = replacer.Replace(target)
	target = strings.Trim(target, "-")
	if target == "" {
		return "operator"
	}
	return target + "-client"
}

func rememberWebTTYKnownServerClientIdentity(name string, identity string) error {
	name = strings.TrimSpace(name)
	identity = strings.TrimSpace(identity)
	if name == "" {
		return fmt.Errorf("known WebTTY server name is empty")
	}
	if identity == "" {
		return fmt.Errorf("WebTTY client identity is empty")
	}
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("known WebTTY server name contains unsupported characters")
	}
	if err := validateWebTTYServerID(identity); err != nil {
		return fmt.Errorf("WebTTY client identity contains unsupported characters")
	}
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		return err
	}
	_, err = webtty.UpdateKnownServerKeysFile(path, func(doc *webtty.KnownServerKeysFile) error {
		for i := range doc.KnownServers {
			if doc.KnownServers[i].Name != name {
				continue
			}
			doc.KnownServers[i].ClientIdentity = identity
			return nil
		}
		return fmt.Errorf("known WebTTY server %q was not found", name)
	})
	return err
}

func (u *uiApp) closeSession(message string) {
	if u.session == nil {
		if strings.TrimSpace(message) != "" {
			u.setMessage(message)
		}
		return
	}
	handle := u.session
	u.session = nil
	u.sessionLeader = false
	handle.cancel()
	if handle.view != nil {
		handle.view.Close()
	}
	u.pages.RemovePage(uiPageSession)
	u.activePage = uiPageInventory
	u.pages.SwitchToPage(uiPageInventory)
	u.app.SetFocus(u.table)
	if strings.TrimSpace(message) != "" {
		u.setMessage(message)
	}
	u.renderInventory()
	u.refreshChrome()
}

func (u *uiApp) setMessage(message string) {
	u.state.Message = strings.TrimSpace(message)
	u.refreshChrome()
}

func (u *uiApp) copyTerminalSelection(text string) bool {
	if uiCopyToClipboard(text) {
		return true
	}
	if u.screen != nil && len(text) > 0 {
		u.screen.SetClipboard([]byte(text))
		return true
	}
	return false
}

func (u *uiApp) cycleView(step int) {
	order := []uiView{uiViewWebTTY, uiViewTunnels, uiViewClients}
	current := 0
	for index, view := range order {
		if view == u.state.View {
			current = index
			break
		}
	}
	current = (current + step + len(order)) % len(order)
	u.switchView(order[current])
}

func (u *uiApp) switchView(view uiView) {
	u.viewChosen = true
	if u.state.View == view {
		u.refreshChrome()
		return
	}
	u.state.View = view
	u.renderInventory()
	u.refreshChrome()
}

func (u *uiApp) captureInput(event *tcell.EventKey) *tcell.EventKey {
	if u.helpVisible {
		switch event.Key() {
		case tcell.KeyF1:
			u.hideHelp()
			return nil
		}
		if event.Key() == tcell.KeyRune && unicodeLower(event.Rune()) == '?' {
			u.hideHelp()
			return nil
		}
		return event
	}
	if u.activePage == uiPageSession {
		if u.sessionLeader {
			return u.handleSessionLeader(event)
		}
		if isUILocalClipboardShortcut(event) {
			return nil
		}
		switch event.Key() {
		case tcell.KeyCtrlG:
			u.sessionLeader = true
			u.refreshChrome()
			return nil
		case tcell.KeyCtrlC:
			if u.session != nil && u.session.view != nil {
				u.session.view.SendKey(event)
			}
			return nil
		}
		return event
	}
	if u.activePage == uiPageIdentityPicker {
		switch event.Key() {
		case tcell.KeyEscape:
			u.closeIdentityPicker()
			u.setMessage("WebTTY connection cancelled")
			return nil
		case tcell.KeyCtrlC:
			u.cancel()
			u.app.Stop()
			return nil
		}
		if event.Key() == tcell.KeyRune && unicodeLower(event.Rune()) == 'q' {
			u.closeIdentityPicker()
			u.setMessage("WebTTY connection cancelled")
			return nil
		}
		return event
	}
	if u.activePage == uiPageTargetPicker {
		return u.captureTargetPickerInput(event)
	}
	switch event.Key() {
	case tcell.KeyCtrlC:
		u.cancel()
		u.app.Stop()
		return nil
	case tcell.KeyTab:
		u.cycleView(1)
		return nil
	case tcell.KeyBacktab:
		u.cycleView(-1)
		return nil
	case tcell.KeyF1:
		u.showHelp()
		return nil
	case tcell.KeyEnter:
		if u.state.View == uiViewWebTTY {
			u.openSelectedWebTTY()
			return nil
		}
	}
	if event.Key() == tcell.KeyRune {
		switch unicodeLower(event.Rune()) {
		case 'q':
			u.cancel()
			u.app.Stop()
			return nil
		case '1':
			u.switchView(uiViewWebTTY)
			return nil
		case '2':
			u.switchView(uiViewTunnels)
			return nil
		case '3':
			u.switchView(uiViewClients)
			return nil
		case 'v':
			u.toggleDetailMode()
			return nil
		case 'c':
			u.showTargetPicker()
			return nil
		case '?':
			u.showHelp()
			return nil
		}
	}
	return event
}

func (u *uiApp) handleSessionLeader(event *tcell.EventKey) *tcell.EventKey {
	u.sessionLeader = false
	u.refreshChrome()
	if event == nil {
		return nil
	}
	switch event.Key() {
	case tcell.KeyEscape:
		return nil
	case tcell.KeyCtrlG:
		if u.session != nil && u.session.view != nil {
			u.session.view.SendKey(event)
		}
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch unicodeLower(event.Rune()) {
		case '?':
			u.showHelp()
			return nil
		case 'd':
			u.toggleSessionInfo()
			return nil
		case 'q':
			u.closeSession("")
			return nil
		}
	}
	return event
}

func (u *uiApp) showHelp() {
	u.sessionLeader = false
	u.help.SetText(u.helpText())
	u.helpVisible = true
	u.pages.ShowPage(uiPageHelp)
	u.pages.SendToFront(uiPageHelp)
	u.app.SetFocus(u.help)
}

func (u *uiApp) hideHelp() {
	u.helpVisible = false
	u.pages.HidePage(uiPageHelp)
	if u.activePage == uiPageSession && u.session != nil && u.session.view != nil {
		u.app.SetFocus(u.session.view)
		return
	}
	u.app.SetFocus(u.table)
}

func (u *uiApp) helpText() string {
	if u.activePage == uiPageSession {
		return strings.TrimSpace(`
[white::b]Session[-:-:-]
  Ctrl+g ?   Open help
  Ctrl+g d   Toggle the details pane
  Ctrl+g q   Close the session and return
  Ctrl+g g   Send Ctrl+g to the remote terminal
  double click   Select a word
  drag       Select terminal text and copy it locally
  Shift/Alt+drag   Select terminal text in full-screen apps
  Cmd+V / Ctrl+Shift+V   Paste from the local clipboard
  Esc        Close this help
`)
	}
	return strings.TrimSpace(`
[white::b]Inventory[-:-:-]
  1          Show WebTTY servers
  2          Show tunnels
  3          Show clients
  Tab        Move to the next view
  arrows     Move in the list
  Enter      Connect to the selected WebTTY server
  c          Select a context or project
  v          Toggle between summary and JSON details
  F1 / ?     Open or close help
  q          Quit rstream ui
`)
}

func defaultUIView(snapshot uiSnapshot) uiView {
	if len(snapshot.WebTTY) > 0 {
		return uiViewWebTTY
	}
	if len(snapshot.Tunnels) > 0 {
		return uiViewTunnels
	}
	return uiViewClients
}

func addHeaderRow(table *tview.Table, values []string) {
	for column, value := range values {
		table.SetCell(
			0,
			column,
			tview.NewTableCell(value).
				SetSelectable(false).
				SetExpansion(1).
				SetTextColor(uiColorChipText).
				SetAttributes(tcell.AttrBold),
		)
	}
}

func addPlaceholderRow(table *tview.Table, text string) {
	table.SetCell(
		1,
		0,
		tview.NewTableCell(text).
			SetSelectable(false).
			SetExpansion(1).
			SetTextColor(uiColorMuted),
	)
}

func setRow(table *tview.Table, row int, values []string, ref string) {
	for column, value := range values {
		cell := tview.NewTableCell(uiSafe(value)).SetExpansion(1).SetTextColor(uiColorText)
		if column == 0 {
			cell.SetReference(ref)
		}
		if column == 1 {
			cell.SetTextColor(uiStatusColor(value))
		}
		table.SetCell(row, column, cell)
	}
}

func uiStatusColor(status string) tcell.Color {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "offline":
		return uiColorDanger
	case "connecting":
		return uiColorMuted
	default:
		return uiColorText
	}
}

func (u *uiApp) formatClientDetail(client rstream.ClientProperties) string {
	if u.currentDetailMode() == uiDetailModeJSON {
		return formatRawObject(client)
	}
	summary := []string{
		fmt.Sprintf("ID                 %s", uiSafe(client.ID)),
		fmt.Sprintf("Status             %s", uiSafe(client.Status)),
		fmt.Sprintf("Agent              %s", uiSafe(optionalValue(client.Agent))),
		fmt.Sprintf("Channel            %s", uiSafe(optionalValue(client.Channel))),
		fmt.Sprintf("Version            %s", uiSafe(optionalValue(client.Version))),
		fmt.Sprintf("User               %s", uiSafe(optionalValue(client.UserID))),
		fmt.Sprintf("OS                 %s", uiSafe(optionalValue(client.OS))),
		fmt.Sprintf("Arch               %s", uiSafe(optionalValue(client.Arch))),
		fmt.Sprintf("Protocol version   %s", uiSafe(optionalValue(client.ProtocolVersion))),
	}
	return strings.TrimSpace(strings.Join([]string{strings.Join(summary, "\n"), formatLabelBlock(client.Labels)}, "\n\n"))
}

func (u *uiApp) formatTunnelDetail(tunnel rstream.TunnelInventory) string {
	if u.currentDetailMode() == uiDetailModeJSON {
		return formatRawObject(tunnel)
	}
	target := trimOptionalString(tunnel.ID)
	if value := trimOptionalString(tunnel.Name); value != "" {
		target = value
	}
	summary := []string{
		fmt.Sprintf("Target             %s", uiSafe(target)),
		fmt.Sprintf("ID                 %s", uiSafe(trimOptionalString(tunnel.ID))),
		fmt.Sprintf("Status             %s", uiSafe(tunnel.Status)),
		fmt.Sprintf("Client             %s", uiSafe(emptyDash(strings.TrimSpace(tunnel.ClientID)))),
		fmt.Sprintf("Type               %s", uiSafe(emptyDash(stringValue(tunnel.Type)))),
		fmt.Sprintf("Protocol           %s", uiSafe(emptyDash(stringValue(tunnel.Protocol)))),
		fmt.Sprintf("Published          %t", boolValue(tunnel.Publish)),
		fmt.Sprintf("Domain / host      %s", uiSafe(emptyDash(tunnelDisplayHost(tunnel.TunnelProperties)))),
	}
	return strings.TrimSpace(strings.Join([]string{strings.Join(summary, "\n"), formatLabelBlock(tunnel.Labels)}, "\n\n"))
}

func tunnelDisplayHost(props rstream.TunnelProperties) string {
	if v, err := rstream.FormatForwardingAddr(props); err == nil {
		return v
	}
	return trimOptionalString(props.Hostname)
}

func (u *uiApp) formatWebTTYDetail(server webtty.ServerInfo) string {
	if u.currentDetailMode() == uiDetailModeJSON {
		return formatRawObject(server)
	}
	summary := []string{
		fmt.Sprintf("Target             %s", uiSafe(server.Target)),
		fmt.Sprintf("Status             %s", uiSafe(server.Status)),
		fmt.Sprintf("Tunnel ID          %s", uiSafe(server.TunnelID)),
		fmt.Sprintf("Tunnel name        %s", uiSafe(optionalValue(server.TunnelName))),
		fmt.Sprintf("rstream URL        %s", uiSafe(server.RstreamURL)),
		fmt.Sprintf("Published          %t", server.Publish),
		fmt.Sprintf("Security           %s", uiSafe(webTTYSecuritySummary(server))),
		fmt.Sprintf("Encryption policy  %s", uiSafe(emptyDash(trimOptionalString(server.EncryptionPolicy)))),
		fmt.Sprintf("Client proof       %s", uiSafe(webTTYClientProofSummary(server))),
		fmt.Sprintf("Host key           %s", uiSafe(emptyDash(trimOptionalString(server.HostKeyID)))),
		fmt.Sprintf("Domain / host      %s", uiSafe(optionalValue(server.Host))),
		fmt.Sprintf("Hostname           %s", uiSafe(optionalValue(server.Hostname))),
		fmt.Sprintf("System             %s", uiSafe(webTTYSystem(server))),
		fmt.Sprintf("Arch               %s", uiSafe(optionalValue(server.Arch))),
	}
	return strings.TrimSpace(strings.Join([]string{strings.Join(summary, "\n"), formatLabelBlock(server.Labels)}, "\n\n"))
}

func formatSessionInfo(server webtty.ServerInfo) string {
	summary := []string{
		fmt.Sprintf("Target             %s", uiSafe(server.Target)),
		fmt.Sprintf("Status             %s", uiSafe(server.Status)),
		fmt.Sprintf("rstream URL        %s", uiSafe(server.RstreamURL)),
		fmt.Sprintf("Security           %s", uiSafe(webTTYSecuritySummary(server))),
		fmt.Sprintf("Encryption policy  %s", uiSafe(emptyDash(trimOptionalString(server.EncryptionPolicy)))),
		fmt.Sprintf("Client proof       %s", uiSafe(webTTYClientProofSummary(server))),
		fmt.Sprintf("Domain / host      %s", uiSafe(optionalValue(server.Host))),
		fmt.Sprintf("Hostname           %s", uiSafe(optionalValue(server.Hostname))),
		fmt.Sprintf("System             %s", uiSafe(webTTYSystem(server))),
		fmt.Sprintf("Arch               %s", uiSafe(optionalValue(server.Arch))),
	}
	return strings.TrimSpace(strings.Join([]string{strings.Join(summary, "\n"), formatLabelBlock(server.Labels)}, "\n\n"))
}

func formatLabelBlock(labels map[string]string) string {
	if len(labels) == 0 {
		return "Labels\n  -"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys)+1)
	lines = append(lines, "Labels")
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("  %s=%s", uiSafe(key), uiSafe(labels[key])))
	}
	return strings.Join(lines, "\n")
}

func webTTYSecuritySummary(server webtty.ServerInfo) string {
	if server.E2E != nil && strings.EqualFold(strings.TrimSpace(*server.E2E), webtty.WebTTYE2ERequired) {
		if server.ClientProof != nil && strings.EqualFold(strings.TrimSpace(*server.ClientProof), webtty.WebTTYClientProofRequired) {
			return "E2E, client proof"
		}
		return "E2E"
	}
	if strings.TrimSpace(trimOptionalString(server.HostKeyID)) != "" {
		return "E2E, client proof"
	}
	return "plain"
}

func webTTYClientProofSummary(server webtty.ServerInfo) string {
	if server.ClientProof != nil && strings.EqualFold(strings.TrimSpace(*server.ClientProof), webtty.WebTTYClientProofRequired) {
		return "required"
	}
	if strings.TrimSpace(trimOptionalString(server.HostKeyID)) != "" {
		return "required"
	}
	return "not required"
}

func formatRawObject(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "unavailable"
	}
	return tview.Escape(string(raw))
}

func sessionHeaderText(server webtty.ServerInfo) string {
	return fmt.Sprintf(
		"[white::b]target[-:-:-] %s   [white::b]hostname[-:-:-] %s",
		uiSafe(server.Target),
		uiSafe(emptyDash(optionalValue(server.Hostname))),
	)
}

func sessionActionsText() string {
	return strings.Join([]string{
		uiKeyLabel("Ctrl+g ?", "Help"),
		uiKeyLabel("Ctrl+g d", "Details"),
		uiKeyLabel("Ctrl+g q", "Back"),
	}, "   ")
}

func uiColorTag(color tcell.Color) string {
	r, g, b := color.RGB()
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func (u *uiApp) refreshSessionLayout(handle *uiSessionHandle) {
	handle.content.Clear()
	if handle.showInfo {
		handle.content.AddItem(handle.view, 0, 2, true)
		handle.content.AddItem(handle.info, 0, 1, false)
		return
	}
	handle.content.AddItem(handle.view, 0, 1, true)
}

func (u *uiApp) currentDetailMode() uiDetailMode {
	if u.state.Detail == uiDetailModeJSON {
		return uiDetailModeJSON
	}
	return uiDetailModeSummary
}

func (u *uiApp) toggleDetailMode() {
	if u.currentDetailMode() == uiDetailModeSummary {
		u.state.Detail = uiDetailModeJSON
	} else {
		u.state.Detail = uiDetailModeSummary
	}
	u.syncDetails()
	u.refreshChrome()
}

func (u *uiApp) toggleSessionInfo() {
	if u.session == nil {
		return
	}
	u.session.showInfo = !u.session.showInfo
	u.refreshSessionLayout(u.session)
	u.app.SetFocus(u.session.view)
}

func optionalValue(value *string) string {
	if value == nil {
		return "-"
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return "-"
	}
	return trimmed
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func stringValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(string(*value))
}

func uiCenteredPrimitive(p tview.Primitive, width, height int) tview.Primitive {
	column := tview.NewFlex().AddItem(tview.NewBox(), 0, 1, false).AddItem(p, width, 0, true).AddItem(tview.NewBox(), 0, 1, false)
	return tview.NewFlex().SetDirection(tview.FlexRow).AddItem(tview.NewBox(), 0, 1, false).AddItem(column, height, 0, true).AddItem(tview.NewBox(), 0, 1, false)
}

func uiStatusMessage(message string) string {
	if strings.TrimSpace(message) == "" {
		return " "
	}
	return "[#f27a7a]" + uiSafe(strings.TrimSpace(message)) + "[-]"
}

func uiSafe(value string) string {
	return tview.Escape(terminalSafeDefault(value))
}

func uiKeyLabel(key, label string) string {
	return fmt.Sprintf("[black:%s:b] %s [-:-:-] [%s]%s[-]", uiColorTag(uiColorChipKey), key, uiColorTag(uiColorText), label)
}

func unicodeLower(value rune) rune {
	return []rune(strings.ToLower(string(value)))[0]
}

func isUILocalClipboardShortcut(event *tcell.EventKey) bool {
	if event == nil || event.Key() != tcell.KeyRune {
		return false
	}
	r := unicodeLower(event.Rune())
	modifiers := event.Modifiers()
	if modifiers&tcell.ModMeta != 0 && (r == 'c' || r == 'v') {
		return true
	}
	if modifiers&tcell.ModCtrl != 0 && modifiers&tcell.ModShift != 0 && (r == 'c' || r == 'v') {
		return true
	}
	return false
}

func contextCanceled(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

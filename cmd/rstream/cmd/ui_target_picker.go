// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type uiTargetPicker struct {
	filter          *tview.InputField
	tabs            *tview.TextView
	table           *tview.Table
	details         *tview.TextView
	status          *tview.TextView
	actions         *tview.TextView
	mode            uiTargetKind
	targets         []uiTarget
	filtered        []uiTarget
	selectedIDs     map[uiTargetKind]string
	discoveryCancel context.CancelFunc
	loading         bool
	projectError    error
	workspaceWarn   error
}

func (u *uiApp) showTargetPicker() {
	if u.session != nil || u.activePage == uiPageSession {
		u.setMessage("Close the WebTTY session before switching context")
		return
	}
	if u.switchCancel != nil {
		u.setMessage("Wait for the current context switch to finish")
		return
	}
	u.closeTargetPicker()
	picker := &uiTargetPicker{mode: uiTargetContext, selectedIDs: make(map[uiTargetKind]string)}
	picker.filter = tview.NewInputField().SetLabel("rstream ui  -  context / project  -  Search: ").SetFieldWidth(0)
	picker.filter.SetBackgroundColor(uiColorBackground)
	picker.filter.SetFieldBackgroundColor(uiColorBackground).SetLabelColor(uiColorText).SetFieldTextColor(uiColorText)
	picker.tabs = newUITextLine(uiColorText)
	picker.table = tview.NewTable().SetBorders(false).SetFixed(1, 0).SetSelectable(true, false).SetSeparator(' ')
	picker.table.SetBackgroundColor(uiColorPanel)
	picker.table.SetBorder(true).SetBorderColor(uiColorBorder).SetTitleColor(uiColorText)
	picker.table.SetSelectedStyle(tcell.StyleDefault.Foreground(uiColorText).Background(uiColorSelection))
	picker.details = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(false).SetWordWrap(false).SetTextColor(uiColorText)
	picker.details.SetBackgroundColor(uiColorPanel)
	picker.details.SetBorder(true).SetBorderColor(uiColorBorder).SetTitle(" Details ").SetTitleColor(uiColorText)
	picker.status = newUITextLine(uiColorMuted)
	picker.actions = newUITextLine(uiColorMuted)
	choices := tview.NewFlex().AddItem(picker.table, 0, 2, true).AddItem(picker.details, 0, 1, false)
	choices.SetBackgroundColor(uiColorBackground)
	content := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(picker.filter, 1, 0, false).
		AddItem(picker.tabs, 1, 0, false).
		AddItem(choices, 0, 1, true).
		AddItem(picker.actions, 1, 0, false).
		AddItem(picker.status, 1, 0, false)
	content.SetBackgroundColor(uiColorBackground)
	picker.filter.SetChangedFunc(func(string) {
		u.renderTargetPicker(picker)
	})
	picker.filter.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter || key == tcell.KeyEscape {
			u.app.SetFocus(picker.table)
		}
	})
	picker.table.SetSelectionChangedFunc(func(row, _ int) {
		if row > 0 && row <= len(picker.filtered) {
			picker.selectedIDs[picker.mode] = picker.filtered[row-1].stableID()
		}
		u.renderTargetPickerDetails(picker)
	})
	picker.table.SetSelectedFunc(func(_, _ int) {
		u.selectTargetPickerTarget(false)
	})
	picker.targets = u.resolver.initialTargets(u.runtime.Config, u.runtime)
	picker.loading = true
	u.targetPicker = picker
	u.renderTargetPicker(picker)
	u.pages.AddAndSwitchToPage(uiPageTargetPicker, content, true)
	u.activePage = uiPageTargetPicker
	u.app.SetFocus(picker.table)
	u.reloadTargetPicker()
}

func (u *uiApp) reloadTargetPicker() {
	picker := u.targetPicker
	if picker == nil {
		return
	}
	if picker.discoveryCancel != nil {
		picker.discoveryCancel()
	}
	discoveryCtx, discoveryCancel := context.WithCancel(u.ctx)
	picker.discoveryCancel = discoveryCancel
	picker.loading = true
	picker.projectError = nil
	picker.workspaceWarn = nil
	u.renderTargetPicker(picker)
	started := u.async.Go(func() {
		discovery := u.resolver.discoverTargets(discoveryCtx, u.runtime)
		u.postUpdate(discoveryCtx, uiUpdate{apply: func() {
			if u.targetPicker != picker || discoveryCtx.Err() != nil {
				return
			}
			picker.targets = discovery.Targets
			picker.loading = false
			picker.projectError = discovery.ProjectError
			picker.workspaceWarn = discovery.WorkspaceWarning
			u.renderTargetPicker(picker)
		}})
	})
	if !started {
		discoveryCancel()
	}
}

func (u *uiApp) renderTargetPicker(picker *uiTargetPicker) {
	if picker == nil || picker.table == nil || picker.filter == nil {
		return
	}
	if picker.selectedIDs == nil {
		picker.selectedIDs = make(map[uiTargetKind]string)
	}
	selectedID := picker.selectedIDs[picker.mode]
	filter := strings.ToLower(strings.TrimSpace(picker.filter.GetText()))
	picker.filtered = picker.filtered[:0]
	for _, target := range picker.targets {
		if target.Kind == picker.mode && (filter == "" || strings.Contains(target.searchText(), filter)) {
			picker.filtered = append(picker.filtered, target)
		}
	}
	picker.table.Clear()
	picker.table.SetSelectable(false, false).Select(0, 0).SetOffset(0, 0)
	headers := []string{"CURRENT", "DEFAULT", "CONTEXT", "PROJECT", "ENGINE", "SCOPE"}
	title := " Contexts "
	if picker.mode == uiTargetProject {
		headers = []string{"CURRENT", "DEFAULT", "PROJECT", "WORKSPACE", "STATUS", "ENDPOINT"}
		title = " Projects "
	}
	picker.table.SetTitle(title)
	addHeaderRow(picker.table, headers)
	selectedRow := 1
	for index, target := range picker.filtered {
		setTargetPickerRow(picker.table, index+1, target)
		if target.stableID() == selectedID {
			selectedRow = index + 1
		}
	}
	if len(picker.filtered) == 0 {
		addPlaceholderRow(picker.table, targetPickerEmptyText(picker, filter))
		picker.selectedIDs[picker.mode] = ""
	} else {
		picker.table.SetSelectable(true, false).Select(selectedRow, 0)
		picker.selectedIDs[picker.mode] = picker.filtered[selectedRow-1].stableID()
	}
	picker.tabs.SetText(formatUITargetPickerTabs(picker))
	picker.status.SetText(formatUITargetPickerStatus(picker))
	picker.actions.SetText(formatUITargetPickerActions())
	u.renderTargetPickerDetails(picker)
}

func (u *uiApp) renderTargetPickerDetails(picker *uiTargetPicker) {
	if picker == nil || picker.details == nil {
		return
	}
	target, ok := selectedTargetPickerTarget(picker)
	if !ok {
		picker.details.SetText(" ")
		return
	}
	picker.details.SetText(formatUITargetDetail(target))
}

func (u *uiApp) captureTargetPickerInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil || u.targetPicker == nil {
		return event
	}
	picker := u.targetPicker
	if u.app.GetFocus() == picker.filter {
		switch event.Key() {
		case tcell.KeyCtrlC:
			u.cancel()
			u.app.Stop()
			return nil
		case tcell.KeyEscape, tcell.KeyEnter:
			u.app.SetFocus(picker.table)
			return nil
		}
		return event
	}
	switch event.Key() {
	case tcell.KeyCtrlC:
		u.cancel()
		u.app.Stop()
		return nil
	case tcell.KeyEscape:
		u.closeTargetPicker()
		return nil
	case tcell.KeyEnter:
		u.selectTargetPickerTarget(false)
		return nil
	case tcell.KeyTab, tcell.KeyBacktab:
		u.toggleTargetPickerMode(picker)
		return nil
	}
	if event.Key() == tcell.KeyRune {
		switch unicodeLower(event.Rune()) {
		case 'q':
			u.closeTargetPicker()
			return nil
		case '/':
			u.app.SetFocus(picker.filter)
			return nil
		case '1':
			u.setTargetPickerMode(picker, uiTargetContext)
			return nil
		case '2':
			u.setTargetPickerMode(picker, uiTargetProject)
			return nil
		case 'd':
			u.selectTargetPickerTarget(true)
			return nil
		case 'r':
			u.reloadTargetPicker()
			return nil
		}
	}
	return event
}

func (u *uiApp) setTargetPickerMode(picker *uiTargetPicker, mode uiTargetKind) {
	if picker == nil || picker.mode == mode {
		return
	}
	picker.mode = mode
	u.renderTargetPicker(picker)
	if u.app != nil {
		u.app.SetFocus(picker.table)
	}
}

func (u *uiApp) toggleTargetPickerMode(picker *uiTargetPicker) {
	if picker != nil && picker.mode == uiTargetContext {
		u.setTargetPickerMode(picker, uiTargetProject)
		return
	}
	u.setTargetPickerMode(picker, uiTargetContext)
}

func (u *uiApp) selectTargetPickerTarget(persist bool) {
	picker := u.targetPicker
	if picker == nil {
		return
	}
	target, ok := selectedTargetPickerTarget(picker)
	if !ok {
		picker.status.SetText("[#f27a7a]No context or project is selected[-]")
		return
	}
	u.switchTarget(target, persist)
}

func selectedTargetPickerTarget(picker *uiTargetPicker) (uiTarget, bool) {
	if picker == nil || picker.table == nil {
		return uiTarget{}, false
	}
	row, _ := picker.table.GetSelection()
	index := row - 1
	if index < 0 || index >= len(picker.filtered) {
		return uiTarget{}, false
	}
	return picker.filtered[index], true
}

func (u *uiApp) closeTargetPicker() {
	if u.targetPicker != nil && u.targetPicker.discoveryCancel != nil {
		u.targetPicker.discoveryCancel()
	}
	u.targetPicker = nil
	if u.pages == nil {
		return
	}
	u.pages.RemovePage(uiPageTargetPicker)
	if u.activePage == uiPageTargetPicker {
		u.activePage = uiPageInventory
		u.pages.SwitchToPage(uiPageInventory)
		if u.table != nil {
			u.app.SetFocus(u.table)
		}
	}
}

func setTargetPickerRow(table *tview.Table, row int, target uiTarget) {
	values := []string{targetPickerFlag(target.Current), targetPickerFlag(target.Default)}
	if target.Kind == uiTargetProject {
		values = append(values,
			target.displayName(),
			uiFirstNonEmpty(target.WorkspaceName, target.Project.WorkspaceID, "-"),
			emptyDash(target.Project.Status),
			emptyDash(target.Project.Endpoint),
		)
	} else {
		scope := "local"
		if strings.TrimSpace(target.Context.APIURL) != "" {
			scope = "linked"
		}
		values = append(values,
			emptyDash(target.Context.Name),
			emptyDash(target.Context.ProjectEndpoint),
			emptyDash(target.Context.Engine),
			scope,
		)
	}
	for column, value := range values {
		text := uiSafe(value)
		if column < 2 {
			text = tview.Escape(value)
		}
		cell := tview.NewTableCell(text).SetExpansion(1).SetTextColor(uiColorText)
		table.SetCell(row, column, cell)
	}
}

func targetPickerFlag(value bool) string {
	if value {
		return "*"
	}
	return ""
}

func targetPickerEmptyText(picker *uiTargetPicker, filter string) string {
	if filter != "" {
		return "No matching " + targetPickerKindPlural(picker.mode)
	}
	if picker.mode == uiTargetProject && picker.loading {
		return "Loading projects..."
	}
	if picker.mode == uiTargetProject && picker.projectError != nil {
		return "Projects unavailable"
	}
	return "No " + targetPickerKindPlural(picker.mode) + " available"
}

func targetPickerKindPlural(kind uiTargetKind) string {
	if kind == uiTargetProject {
		return "projects"
	}
	return "contexts"
}

func formatUITargetPickerTabs(picker *uiTargetPicker) string {
	return strings.Join([]string{
		targetPickerTabLabel("1", "Contexts", picker.mode == uiTargetContext),
		targetPickerTabLabel("2", "Projects", picker.mode == uiTargetProject),
	}, "   ")
}

func targetPickerTabLabel(key, label string, active bool) string {
	if active {
		return fmt.Sprintf("[black:%s:b] %s %s [-:-:-]", uiColorTag(uiColorChipActive), key, label)
	}
	return fmt.Sprintf("[%s:%s:b] %s %s [-:-:-]", uiColorTag(uiColorChipText), uiColorTag(uiColorChip), key, label)
}

func formatUITargetPickerStatus(picker *uiTargetPicker) string {
	if picker.mode != uiTargetProject {
		return " "
	}
	if picker.loading {
		return "[#b0bac5]Loading projects...[-]"
	}
	if picker.projectError != nil {
		return "[#f27a7a]" + uiSafe(picker.projectError.Error()) + "[-]"
	}
	if picker.workspaceWarn != nil {
		return "[#b0bac5]" + uiSafe(picker.workspaceWarn.Error()) + "[-]"
	}
	return " "
}

func formatUITargetPickerActions() string {
	return strings.Join([]string{
		uiKeyLabel("1", "Contexts"),
		uiKeyLabel("2", "Projects"),
		uiKeyLabel("Tab", "Next"),
		uiKeyLabel("Enter", "Use"),
		uiKeyLabel("d", "Make default"),
		uiKeyLabel("/", "Search"),
		uiKeyLabel("r", "Refresh"),
		uiKeyLabel("Esc", "Back"),
	}, "   ")
}

func formatUITargetDetail(target uiTarget) string {
	lines := []string{
		"Name       " + uiSafe(target.displayName()),
	}
	if target.Kind == uiTargetProject {
		lines = append(lines,
			"Workspace  "+uiSafe(uiFirstNonEmpty(target.WorkspaceName, target.Project.WorkspaceID, "-")),
			"Status     "+uiSafe(emptyDash(target.Project.Status)),
			"Endpoint   "+uiSafe(emptyDash(target.Project.Endpoint)),
			"Engine     "+uiSafe(emptyDash(target.Project.EngineAddress())),
			"Context    "+uiSafe(emptyDash(target.Context.Name)),
		)
	} else {
		lines = append(lines,
			"Project    "+uiSafe(emptyDash(target.Context.ProjectEndpoint)),
			"Engine     "+uiSafe(emptyDash(target.Context.Engine)),
			"API        "+uiSafe(emptyDash(target.Context.APIURL)),
		)
	}
	return strings.Join(lines, "\n")
}

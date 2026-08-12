// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"image/color"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/rstreamlabs/rstream-go/webtty"
)

type uiTerminalView struct {
	*tview.Box

	session     *webtty.ClientSession
	term        *vt.SafeEmulator
	copyText    func(string) bool
	requestDraw func(func()) bool

	mu              sync.Mutex
	closeOnce       sync.Once
	wg              sync.WaitGroup
	redrawTimer     *time.Timer
	width           int
	height          int
	closed          bool
	scroll          int
	redraw          bool
	selecting       bool
	selectionMoved  bool
	selectionStart  uiTerminalPoint
	selectionEnd    uiTerminalPoint
	selectionActive bool
}

type uiTerminalPoint struct {
	Line   int
	Column int
}

func newUITerminalView(session *webtty.ClientSession, copyText func(string) bool, requestDraw func(func()) bool) *uiTerminalView {
	view := &uiTerminalView{
		Box:         tview.NewBox(),
		session:     session,
		term:        vt.NewSafeEmulator(80, 24),
		copyText:    copyText,
		requestDraw: requestDraw,
	}
	view.term.SetScrollbackSize(5000)
	view.term.SetDefaultForegroundColor(color.NRGBA{R: 0xf3, G: 0xf4, B: 0xf6, A: 0xff})
	view.term.SetDefaultBackgroundColor(color.NRGBA{R: 0x0c, G: 0x10, B: 0x14, A: 0xff})
	applyUITerminalPalette(view.term)
	view.SetBackgroundColor(uiColorPanel)
	view.SetBorder(true).
		SetBorderColor(uiColorBorder).
		SetTitle(" rstream WebTTY ").
		SetTitleColor(uiColorText)
	view.start()
	return view
}

func (v *uiTerminalView) start() {
	v.wg.Add(2)
	go func() {
		defer v.wg.Done()
		v.pumpSessionOutput()
	}()
	go func() {
		defer v.wg.Done()
		v.pumpTerminalInput()
	}()
}

func (v *uiTerminalView) Close() {
	if v == nil {
		return
	}
	v.closeOnce.Do(func() {
		v.mu.Lock()
		v.closed = true
		timer := v.redrawTimer
		v.redrawTimer = nil
		v.redraw = false
		v.mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		if v.term != nil {
			_ = v.term.Close()
		}
		if v.session != nil {
			_ = v.session.Close()
		}
		v.wg.Wait()
	})
}

func (v *uiTerminalView) Draw(screen tcell.Screen) {
	v.Box.DrawForSubclass(screen, v)
	x, y, width, height := v.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	v.ensureSize(height, width)
	baseStyle := tcell.StyleDefault.Foreground(uiColorText).Background(uiColorPanel)
	scroll := v.currentScroll()
	scrollbackLen := v.term.ScrollbackLen()
	startLine := scrollbackLen - scroll
	if startLine < 0 {
		startLine = 0
	}
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			screen.SetContent(x+column, y+row, ' ', nil, baseStyle)
		}
	}
	cursor := v.term.CursorPosition()
	screen.HideCursor()
	defaultForeground := tcellColor(v.term.ForegroundColor(), uiColorText)
	defaultBackground := tcellColor(v.term.BackgroundColor(), uiColorPanel)
	for row := 0; row < height; row++ {
		for column := 0; column < width; {
			cell := v.cellAt(column, startLine+row, scrollbackLen)
			if cell == nil {
				screen.SetContent(
					x+column,
					y+row,
					' ',
					nil,
					tcell.StyleDefault.Foreground(defaultForeground).Background(defaultBackground),
				)
				column++
				continue
			}
			step := cell.Width
			if step <= 0 {
				step = 1
			}
			style := styleFromUVCell(cell, defaultForeground, defaultBackground)
			if v.isCellSelected(uiTerminalPoint{Line: startLine + row, Column: column}, step) {
				style = tcell.StyleDefault.Foreground(uiColorText).Background(uiColorSelection)
			}
			if scroll == 0 && v.HasFocus() && cursor.X == column && cursor.Y == row {
				style = style.Reverse(true)
				screen.ShowCursor(x+column, y+row)
			}
			main, combining := graphemeToTCell(cell.Content)
			screen.SetContent(x+column, y+row, main, combining, style)
			column += step
		}
	}
}

func (v *uiTerminalView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return v.Box.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		if v.handleLocalKey(event) {
			return
		}
		v.SendKey(event)
	})
}

func (v *uiTerminalView) PasteHandler() func(text string, setFocus func(p tview.Primitive)) {
	return v.Box.WrapPasteHandler(func(text string, setFocus func(p tview.Primitive)) {
		if text == "" {
			return
		}
		v.resetScroll()
		v.clearSelection()
		v.term.Paste(text)
	})
}

func (v *uiTerminalView) SendKey(event *tcell.EventKey) bool {
	v.resetScroll()
	v.clearSelection()
	key, ok := keyFromTCell(event)
	if !ok {
		return false
	}
	v.term.SendKey(key)
	return true
}

func (v *uiTerminalView) Focus(delegate func(p tview.Primitive)) {
	v.Box.Focus(delegate)
}

func (v *uiTerminalView) Blur() {
	v.Box.Blur()
}

func (v *uiTerminalView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return v.Box.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		if action == tview.MouseLeftDown && v.InRect(event.Position()) {
			setFocus(v)
		}
		if !v.InInnerRect(event.Position()) {
			if action == tview.MouseLeftUp && v.selecting {
				v.finishSelection()
				return true, nil
			}
			return false, nil
		}
		localSelection := v.localSelectionEnabled(event)
		switch action {
		case tview.MouseLeftDown:
			if !localSelection {
				v.clearSelection()
				return v.sendRemoteMouseClick(event), nil
			}
			v.beginSelectionFromMouse(event)
			return true, nil
		case tview.MouseLeftDoubleClick:
			if !localSelection {
				v.clearSelection()
				return v.sendRemoteMouseClick(event), nil
			}
			v.selectWordFromMouse(event)
			return true, nil
		case tview.MouseMove:
			if v.selecting {
				v.updateSelectionFromMouse(event)
				return true, nil
			}
			if !localSelection {
				return v.sendRemoteMouseMotion(event), nil
			}
			return false, nil
		case tview.MouseLeftUp:
			if v.selecting {
				v.updateSelectionFromMouse(event)
				v.finishSelection()
				return true, nil
			}
			if !localSelection {
				return v.sendRemoteMouseRelease(event), nil
			}
			return false, nil
		case tview.MouseScrollUp:
			return v.handleMouseWheel(event, true), nil
		case tview.MouseScrollDown:
			return v.handleMouseWheel(event, false), nil
		default:
			return false, nil
		}
	})
}

func (v *uiTerminalView) pumpSessionOutput() {
	pending := make([]byte, 0, 64*1024)
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		before := v.term.ScrollbackLen()
		if _, err := v.term.Write(pending); err != nil {
			return false
		}
		after := v.term.ScrollbackLen()
		pending = pending[:0]
		v.preserveScroll(after - before)
		v.scheduleDraw()
		return true
	}
	events := v.session.Events()
	for event := range events {
		if len(event.Data) == 0 {
			continue
		}
		pending = append(pending, event.Data...)
		for len(pending) < 128*1024 {
			select {
			case event, ok := <-events:
				if !ok {
					flush()
					return
				}
				if len(event.Data) == 0 {
					continue
				}
				pending = append(pending, event.Data...)
			default:
				if !flush() {
					return
				}
				goto nextEvent
			}
		}
		if !flush() {
			return
		}
	nextEvent:
	}
}

func (v *uiTerminalView) pumpTerminalInput() {
	buffer := make([]byte, 32*1024)
	for {
		n, err := v.term.Read(buffer)
		if n > 0 {
			if sendErr := v.session.SendInput(buffer[:n]); sendErr != nil {
				return
			}
		}
		if err != nil {
			if err == io.EOF || err == context.Canceled {
				return
			}
			return
		}
	}
}

func (v *uiTerminalView) ensureSize(rows, cols int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.width == cols && v.height == rows {
		return
	}
	v.width = cols
	v.height = rows
	v.term.Resize(cols, rows)
	_ = v.session.Resize(rows, cols)
}

func (v *uiTerminalView) cellAt(column, line, scrollbackLen int) *uv.Cell {
	if line < 0 {
		return nil
	}
	if line < scrollbackLen {
		return v.term.ScrollbackCellAt(column, line)
	}
	return v.term.CellAt(column, line-scrollbackLen)
}

func (v *uiTerminalView) currentScroll() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.localScrollEnabled() {
		v.scroll = 0
		return 0
	}
	maxScroll := v.term.ScrollbackLen()
	if v.scroll > maxScroll {
		v.scroll = maxScroll
	}
	if v.scroll < 0 {
		v.scroll = 0
	}
	return v.scroll
}

func (v *uiTerminalView) resetScroll() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.scroll = 0
}

func (v *uiTerminalView) preserveScroll(delta int) {
	if delta <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.localScrollEnabled() {
		v.scroll = 0
		return
	}
	if v.scroll <= 0 {
		return
	}
	v.scroll += delta
	maxScroll := v.term.ScrollbackLen()
	if v.scroll > maxScroll {
		v.scroll = maxScroll
	}
}

func (v *uiTerminalView) scrollBy(delta int) {
	if delta == 0 {
		return
	}
	if !v.localScrollEnabled() {
		return
	}
	v.clearSelection()
	v.mu.Lock()
	maxScroll := v.term.ScrollbackLen()
	next := v.scroll + delta
	if next < 0 {
		next = 0
	}
	if next > maxScroll {
		next = maxScroll
	}
	if next == v.scroll {
		v.mu.Unlock()
		return
	}
	v.scroll = next
	v.mu.Unlock()
}

func (v *uiTerminalView) localScrollEnabled() bool {
	return !v.term.IsAltScreen()
}

func (v *uiTerminalView) localSelectionEnabled(event *tcell.EventMouse) bool {
	if event == nil {
		return false
	}
	if !v.term.IsAltScreen() {
		return true
	}
	modifiers := event.Modifiers()
	return modifiers&tcell.ModShift != 0 || modifiers&tcell.ModAlt != 0
}

func (v *uiTerminalView) lineScrollStep() int {
	return 1
}

func (v *uiTerminalView) wheelScrollStep() int {
	return 3
}

func (v *uiTerminalView) pageScrollStep() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.height <= 4 {
		return 1
	}
	return v.height - 2
}

func (v *uiTerminalView) handleLocalKey(event *tcell.EventKey) bool {
	if event == nil {
		return false
	}
	if !v.localScrollEnabled() {
		return false
	}
	if event.Modifiers()&tcell.ModShift == 0 {
		return false
	}
	switch event.Key() {
	case tcell.KeyUp:
		v.scrollBy(v.lineScrollStep())
		return true
	case tcell.KeyDown:
		v.scrollBy(-v.lineScrollStep())
		return true
	case tcell.KeyPgUp:
		v.scrollBy(v.pageScrollStep())
		return true
	case tcell.KeyPgDn:
		v.scrollBy(-v.pageScrollStep())
		return true
	case tcell.KeyHome:
		v.scrollBy(v.term.ScrollbackLen())
		return true
	case tcell.KeyEnd:
		v.scrollBy(-v.term.ScrollbackLen())
		return true
	default:
		return false
	}
}

func (v *uiTerminalView) handleMouseWheel(event *tcell.EventMouse, up bool) bool {
	if event == nil {
		return false
	}
	if !v.localScrollEnabled() {
		v.clearSelection()
		return v.sendRemoteMouseWheel(event, up)
	}
	v.clearSelection()
	step := v.mouseScrollStep(event.Modifiers())
	if !up {
		step = -step
	}
	v.scrollBy(step)
	return true
}

func (v *uiTerminalView) mouseScrollStep(modifiers tcell.ModMask) int {
	if modifiers&tcell.ModAlt != 0 {
		return v.lineScrollStep()
	}
	if modifiers&tcell.ModShift != 0 {
		return v.pageScrollStep()
	}
	return v.wheelScrollStep()
}

func (v *uiTerminalView) beginSelectionFromMouse(event *tcell.EventMouse) {
	point, ok := v.selectionPointFromMouse(event)
	if !ok {
		return
	}
	v.selecting = true
	v.selectionMoved = false
	v.selectionStart = point
	v.selectionEnd = point
	v.selectionActive = true
	v.scheduleDraw()
}

func (v *uiTerminalView) selectWordFromMouse(event *tcell.EventMouse) {
	point, ok := v.selectionPointFromMouse(event)
	if !ok {
		v.clearSelection()
		return
	}
	start, end, ok := v.wordBoundsAtPoint(point)
	if !ok {
		v.clearSelection()
		return
	}
	v.selecting = false
	v.selectionMoved = true
	v.selectionStart = start
	v.selectionEnd = end
	v.selectionActive = true
	v.copySelection()
	v.scheduleDraw()
}

func (v *uiTerminalView) updateSelectionFromMouse(event *tcell.EventMouse) {
	point, ok := v.selectionPointFromMouse(event)
	if !ok {
		return
	}
	if point != v.selectionEnd {
		v.selectionMoved = true
	}
	v.selectionEnd = point
	v.scheduleDraw()
}

func (v *uiTerminalView) finishSelection() {
	if !v.selecting {
		return
	}
	v.selecting = false
	if !v.selectionActive || !v.selectionMoved {
		v.clearSelection()
		return
	}
	v.copySelection()
	v.scheduleDraw()
}

func (v *uiTerminalView) clearSelection() {
	if !v.selectionActive && !v.selecting {
		return
	}
	v.selecting = false
	v.selectionMoved = false
	v.selectionActive = false
	v.selectionStart = uiTerminalPoint{}
	v.selectionEnd = uiTerminalPoint{}
	v.scheduleDraw()
}

func (v *uiTerminalView) selectionPointFromMouse(event *tcell.EventMouse) (uiTerminalPoint, bool) {
	if event == nil {
		return uiTerminalPoint{}, false
	}
	x, y := event.Position()
	innerX, innerY, width, height := v.GetInnerRect()
	if width <= 0 || height <= 0 {
		return uiTerminalPoint{}, false
	}
	if x < innerX || x >= innerX+width || y < innerY || y >= innerY+height {
		return uiTerminalPoint{}, false
	}
	return uiTerminalPoint{
		Line:   v.term.ScrollbackLen() - v.currentScroll() + (y - innerY),
		Column: x - innerX,
	}, true
}

func (v *uiTerminalView) wordBoundsAtPoint(point uiTerminalPoint) (uiTerminalPoint, uiTerminalPoint, bool) {
	scrollbackLen := v.term.ScrollbackLen()
	cell := v.cellAt(point.Column, point.Line, scrollbackLen)
	if !isUIWordSelectionCell(cell) {
		return uiTerminalPoint{}, uiTerminalPoint{}, false
	}
	start := point.Column
	for column := point.Column - 1; column >= 0; column-- {
		if !isUIWordSelectionCell(v.cellAt(column, point.Line, scrollbackLen)) {
			break
		}
		start = column
	}
	end := point.Column
	for column := point.Column + 1; column < v.width; column++ {
		if !isUIWordSelectionCell(v.cellAt(column, point.Line, scrollbackLen)) {
			break
		}
		end = column
	}
	return uiTerminalPoint{Line: point.Line, Column: start}, uiTerminalPoint{Line: point.Line, Column: end}, true
}

func isUIWordSelectionCell(cell *uv.Cell) bool {
	if cell == nil {
		return false
	}
	return strings.TrimSpace(cell.Content) != ""
}

func (v *uiTerminalView) copySelection() {
	if !v.selectionActive || v.copyText == nil {
		return
	}
	text := strings.TrimRight(v.selectedText(), "\n")
	if text == "" {
		return
	}
	_ = v.copyText(text)
}

func (v *uiTerminalView) isCellSelected(point uiTerminalPoint, width int) bool {
	if !v.selectionActive {
		return false
	}
	start, end := v.normalizedSelection()
	if point.Line < start.Line || point.Line > end.Line {
		return false
	}
	cellStart := point.Column
	cellEnd := point.Column + width - 1
	if cellEnd < cellStart {
		cellEnd = cellStart
	}
	lineStart := 0
	lineEnd := maxInt(v.width-1, 0)
	if point.Line == start.Line {
		lineStart = start.Column
	}
	if point.Line == end.Line {
		lineEnd = end.Column
	}
	return cellEnd >= lineStart && cellStart <= lineEnd
}

func (v *uiTerminalView) normalizedSelection() (uiTerminalPoint, uiTerminalPoint) {
	start := v.selectionStart
	end := v.selectionEnd
	if end.Line < start.Line || (end.Line == start.Line && end.Column < start.Column) {
		start, end = end, start
	}
	return start, end
}

func (v *uiTerminalView) selectedText() string {
	if !v.selectionActive {
		return ""
	}
	start, end := v.normalizedSelection()
	scrollbackLen := v.term.ScrollbackLen()
	lines := make([]string, 0, end.Line-start.Line+1)
	for line := start.Line; line <= end.Line; line++ {
		lineStart := 0
		lineEnd := maxInt(v.width-1, 0)
		if line == start.Line {
			lineStart = start.Column
		}
		if line == end.Line {
			lineEnd = end.Column
		}
		lines = append(lines, v.selectedLineText(line, lineStart, lineEnd, scrollbackLen))
	}
	return strings.Join(lines, "\n")
}

func (v *uiTerminalView) selectedLineText(line, startColumn, endColumn, scrollbackLen int) string {
	if endColumn < startColumn {
		return ""
	}
	var builder strings.Builder
	for column := 0; column < v.width; {
		cell := v.cellAt(column, line, scrollbackLen)
		step := 1
		content := " "
		if cell != nil {
			if cell.Width > 0 {
				step = cell.Width
			}
			if cell.Content != "" {
				content = cell.Content
			}
		}
		cellStart := column
		cellEnd := column + step - 1
		if cellEnd >= startColumn && cellStart <= endColumn {
			builder.WriteString(content)
		}
		column += step
	}
	return strings.TrimRight(builder.String(), " ")
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (v *uiTerminalView) sendRemoteMouseWheel(event *tcell.EventMouse, up bool) bool {
	button := vt.MouseWheelDown
	if up {
		button = vt.MouseWheelUp
	}
	x, y, ok := v.remoteMousePosition(event)
	if !ok {
		return false
	}
	v.term.SendMouse(vt.MouseWheel{
		X:      x,
		Y:      y,
		Button: button,
		Mod:    vtKeyModFromTCell(event.Modifiers()),
	})
	return true
}

func (v *uiTerminalView) sendRemoteMouseClick(event *tcell.EventMouse) bool {
	x, y, ok := v.remoteMousePosition(event)
	if !ok {
		return false
	}
	v.term.SendMouse(vt.MouseClick{
		X:      x,
		Y:      y,
		Button: vt.MouseLeft,
		Mod:    vtKeyModFromTCell(event.Modifiers()),
	})
	return true
}

func (v *uiTerminalView) sendRemoteMouseRelease(event *tcell.EventMouse) bool {
	x, y, ok := v.remoteMousePosition(event)
	if !ok {
		return false
	}
	v.term.SendMouse(vt.MouseRelease{
		X:      x,
		Y:      y,
		Button: vt.MouseLeft,
		Mod:    vtKeyModFromTCell(event.Modifiers()),
	})
	return true
}

func (v *uiTerminalView) sendRemoteMouseMotion(event *tcell.EventMouse) bool {
	x, y, ok := v.remoteMousePosition(event)
	if !ok {
		return false
	}
	v.term.SendMouse(vt.MouseMotion{
		X:      x,
		Y:      y,
		Button: vt.MouseLeft,
		Mod:    vtKeyModFromTCell(event.Modifiers()),
	})
	return true
}

func (v *uiTerminalView) remoteMousePosition(event *tcell.EventMouse) (int, int, bool) {
	if event == nil {
		return 0, 0, false
	}
	x, y := event.Position()
	innerX, innerY, _, _ := v.GetInnerRect()
	return x - innerX, y - innerY, true
}

func (v *uiTerminalView) scheduleDraw() {
	v.mu.Lock()
	if v.closed || v.redraw || v.requestDraw == nil {
		v.mu.Unlock()
		return
	}
	v.redraw = true
	v.redrawTimer = time.AfterFunc(33*time.Millisecond, func() {
		v.mu.Lock()
		if v.closed {
			v.redraw = false
			v.redrawTimer = nil
			v.mu.Unlock()
			return
		}
		v.redrawTimer = nil
		requestDraw := v.requestDraw
		v.mu.Unlock()
		requestDraw(func() {
			v.mu.Lock()
			v.redraw = false
			v.mu.Unlock()
		})
	})
	v.mu.Unlock()
}

func keyFromTCell(event *tcell.EventKey) (vt.KeyPressEvent, bool) {
	mod := vtKeyModFromTCell(event.Modifiers())
	switch event.Key() {
	case tcell.KeyRune:
		return vt.KeyPressEvent{Code: event.Rune(), Mod: mod}, true
	case tcell.KeyEnter:
		return vt.KeyPressEvent{Code: vt.KeyEnter, Mod: mod}, true
	case tcell.KeyTab:
		return vt.KeyPressEvent{Code: vt.KeyTab, Mod: mod}, true
	case tcell.KeyBacktab:
		return vt.KeyPressEvent{Code: vt.KeyTab, Mod: mod | vt.ModShift}, true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return vt.KeyPressEvent{Code: vt.KeyBackspace, Mod: mod}, true
	case tcell.KeyEscape:
		return vt.KeyPressEvent{Code: vt.KeyEscape, Mod: mod}, true
	case tcell.KeyUp:
		return vt.KeyPressEvent{Code: vt.KeyUp, Mod: mod}, true
	case tcell.KeyDown:
		return vt.KeyPressEvent{Code: vt.KeyDown, Mod: mod}, true
	case tcell.KeyLeft:
		return vt.KeyPressEvent{Code: vt.KeyLeft, Mod: mod}, true
	case tcell.KeyRight:
		return vt.KeyPressEvent{Code: vt.KeyRight, Mod: mod}, true
	case tcell.KeyHome:
		return vt.KeyPressEvent{Code: vt.KeyHome, Mod: mod}, true
	case tcell.KeyEnd:
		return vt.KeyPressEvent{Code: vt.KeyEnd, Mod: mod}, true
	case tcell.KeyPgUp:
		return vt.KeyPressEvent{Code: vt.KeyPgUp, Mod: mod}, true
	case tcell.KeyPgDn:
		return vt.KeyPressEvent{Code: vt.KeyPgDown, Mod: mod}, true
	case tcell.KeyDelete:
		return vt.KeyPressEvent{Code: vt.KeyDelete, Mod: mod}, true
	case tcell.KeyInsert:
		return vt.KeyPressEvent{Code: vt.KeyInsert, Mod: mod}, true
	case tcell.KeyF1:
		return vt.KeyPressEvent{Code: vt.KeyF1, Mod: mod}, true
	case tcell.KeyF2:
		return vt.KeyPressEvent{Code: vt.KeyF2, Mod: mod}, true
	case tcell.KeyF3:
		return vt.KeyPressEvent{Code: vt.KeyF3, Mod: mod}, true
	case tcell.KeyF4:
		return vt.KeyPressEvent{Code: vt.KeyF4, Mod: mod}, true
	case tcell.KeyF5:
		return vt.KeyPressEvent{Code: vt.KeyF5, Mod: mod}, true
	case tcell.KeyF6:
		return vt.KeyPressEvent{Code: vt.KeyF6, Mod: mod}, true
	case tcell.KeyF7:
		return vt.KeyPressEvent{Code: vt.KeyF7, Mod: mod}, true
	case tcell.KeyF8:
		return vt.KeyPressEvent{Code: vt.KeyF8, Mod: mod}, true
	case tcell.KeyF9:
		return vt.KeyPressEvent{Code: vt.KeyF9, Mod: mod}, true
	case tcell.KeyF10:
		return vt.KeyPressEvent{Code: vt.KeyF10, Mod: mod}, true
	case tcell.KeyF11:
		return vt.KeyPressEvent{Code: vt.KeyF11, Mod: mod}, true
	case tcell.KeyF12:
		return vt.KeyPressEvent{Code: vt.KeyF12, Mod: mod}, true
	case tcell.KeyCtrlSpace:
		return vt.KeyPressEvent{Code: vt.KeySpace, Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlA:
		return vt.KeyPressEvent{Code: 'a', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlB:
		return vt.KeyPressEvent{Code: 'b', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlC:
		return vt.KeyPressEvent{Code: 'c', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlD:
		return vt.KeyPressEvent{Code: 'd', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlE:
		return vt.KeyPressEvent{Code: 'e', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlF:
		return vt.KeyPressEvent{Code: 'f', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlG:
		return vt.KeyPressEvent{Code: 'g', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlJ:
		return vt.KeyPressEvent{Code: 'j', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlK:
		return vt.KeyPressEvent{Code: 'k', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlL:
		return vt.KeyPressEvent{Code: 'l', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlN:
		return vt.KeyPressEvent{Code: 'n', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlO:
		return vt.KeyPressEvent{Code: 'o', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlP:
		return vt.KeyPressEvent{Code: 'p', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlQ:
		return vt.KeyPressEvent{Code: 'q', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlR:
		return vt.KeyPressEvent{Code: 'r', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlS:
		return vt.KeyPressEvent{Code: 's', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlT:
		return vt.KeyPressEvent{Code: 't', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlU:
		return vt.KeyPressEvent{Code: 'u', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlV:
		return vt.KeyPressEvent{Code: 'v', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlW:
		return vt.KeyPressEvent{Code: 'w', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlX:
		return vt.KeyPressEvent{Code: 'x', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlY:
		return vt.KeyPressEvent{Code: 'y', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlZ:
		return vt.KeyPressEvent{Code: 'z', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlRightSq:
		return vt.KeyPressEvent{Code: ']', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlBackslash:
		return vt.KeyPressEvent{Code: '\\', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlCarat:
		return vt.KeyPressEvent{Code: '^', Mod: vt.ModCtrl}, true
	case tcell.KeyCtrlUnderscore:
		return vt.KeyPressEvent{Code: '_', Mod: vt.ModCtrl}, true
	default:
		return vt.KeyPressEvent{}, false
	}
}

func vtKeyModFromTCell(modifiers tcell.ModMask) vt.KeyMod {
	mod := vt.KeyMod(0)
	if modifiers&tcell.ModCtrl != 0 {
		mod |= vt.ModCtrl
	}
	if modifiers&tcell.ModAlt != 0 {
		mod |= vt.ModAlt
	}
	if modifiers&tcell.ModShift != 0 {
		mod |= vt.ModShift
	}
	return mod
}

func styleFromUVCell(cell *uv.Cell, defaultForeground, defaultBackground tcell.Color) tcell.Style {
	if cell == nil {
		return tcell.StyleDefault.Foreground(defaultForeground).Background(defaultBackground)
	}
	foreground := defaultForeground
	background := defaultBackground
	if cell.Style.Fg != nil {
		foreground = tcellColor(cell.Style.Fg, defaultForeground)
	}
	if cell.Style.Bg != nil {
		background = tcellColor(cell.Style.Bg, defaultBackground)
	}
	if cell.Style.Attrs&uv.AttrReverse != 0 {
		foreground, background = background, foreground
	}
	foreground = uiReadableTerminalColor(foreground, background)
	style := tcell.StyleDefault.Foreground(foreground).Background(background)
	if cell.Style.Attrs&uv.AttrBold != 0 {
		style = style.Bold(true)
	}
	if cell.Style.Attrs&uv.AttrItalic != 0 {
		style = style.Italic(true)
	}
	if cell.Style.Attrs&uv.AttrFaint != 0 {
		style = style.Dim(true)
	}
	if cell.Style.Attrs&(uv.AttrBlink|uv.AttrRapidBlink) != 0 {
		style = style.Blink(true)
	}
	if cell.Style.Attrs&uv.AttrStrikethrough != 0 {
		style = style.StrikeThrough(true)
	}
	if cell.Style.Underline != uv.UnderlineNone {
		style = style.Underline(true)
	}
	if cell.Style.Attrs&uv.AttrConceal != 0 {
		style = style.Foreground(background)
	}
	return style
}

func graphemeToTCell(value string) (rune, []rune) {
	if strings.TrimSpace(value) == "" {
		return ' ', nil
	}
	runes := []rune(value)
	if len(runes) == 0 {
		return ' ', nil
	}
	return runes[0], runes[1:]
}

func tcellColor(value color.Color, fallback tcell.Color) tcell.Color {
	if value == nil {
		return fallback
	}
	red, green, blue, _ := value.RGBA()
	return tcell.NewRGBColor(
		int32(uint8(red>>8)),
		int32(uint8(green>>8)),
		int32(uint8(blue>>8)),
	)
}

func uiReadableTerminalColor(foreground, background tcell.Color) tcell.Color {
	fgR, fgG, fgB := foreground.RGB()
	bgR, bgG, bgB := background.RGB()
	if uiRelativeLuminance(bgR, bgG, bgB) > 0.16 {
		return foreground
	}
	if uiRelativeLuminance(fgR, fgG, fgB) >= 0.22 {
		return foreground
	}
	return uiBoostTerminalColor(fgR, fgG, fgB, 0xf0)
}

func uiBoostTerminalColor(red, green, blue int32, targetMax int32) tcell.Color {
	maxComponent := red
	if green > maxComponent {
		maxComponent = green
	}
	if blue > maxComponent {
		maxComponent = blue
	}
	if maxComponent <= 0 {
		return uiColorText
	}
	if maxComponent >= targetMax {
		return tcell.NewRGBColor(red, green, blue)
	}
	scale := float64(targetMax) / float64(maxComponent)
	boost := func(value int32) int32 {
		scaled := int32(float64(value) * scale)
		if scaled > 0xff {
			return 0xff
		}
		return scaled
	}
	return tcell.NewRGBColor(boost(red), boost(green), boost(blue))
}

func uiRelativeLuminance(red, green, blue int32) float64 {
	linear := func(value int32) float64 {
		v := float64(value) / 255.0
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r := linear(red)
	g := linear(green)
	b := linear(blue)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func applyUITerminalPalette(term *vt.SafeEmulator) {
	if term == nil {
		return
	}
	palette := []color.NRGBA{
		{R: 0x1b, G: 0x1f, B: 0x24, A: 0xff},
		{R: 0xff, G: 0x5f, B: 0x72, A: 0xff},
		{R: 0x00, G: 0xff, B: 0x5f, A: 0xff},
		{R: 0xff, G: 0xe0, B: 0x5f, A: 0xff},
		{R: 0x5f, G: 0xaf, B: 0xff, A: 0xff},
		{R: 0xe0, G: 0x7a, B: 0xff, A: 0xff},
		{R: 0x3f, G: 0xe5, B: 0xff, A: 0xff},
		{R: 0xe6, G: 0xed, B: 0xf3, A: 0xff},
		{R: 0x6e, G: 0x76, B: 0x81, A: 0xff},
		{R: 0xff, G: 0x8a, B: 0x9b, A: 0xff},
		{R: 0x5c, G: 0xff, B: 0x8d, A: 0xff},
		{R: 0xff, G: 0xed, B: 0x78, A: 0xff},
		{R: 0x86, G: 0xc2, B: 0xff, A: 0xff},
		{R: 0xed, G: 0xb2, B: 0xff, A: 0xff},
		{R: 0x7a, G: 0xee, B: 0xff, A: 0xff},
		{R: 0xf8, G: 0xfa, B: 0xfc, A: 0xff},
	}
	for index, value := range palette {
		term.SetIndexedColor(index, value)
	}
}

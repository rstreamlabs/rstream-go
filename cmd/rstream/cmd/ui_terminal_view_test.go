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

func TestUIReadableTerminalColorBoostsDarkForegroundOnDarkBackground(t *testing.T) {
	t.Parallel()
	foreground := tcell.NewRGBColor(0x00, 0x00, 0xaa)
	background := tcell.NewRGBColor(0x0c, 0x10, 0x14)
	boosted := uiReadableTerminalColor(foreground, background)
	if boosted == foreground {
		t.Fatalf("uiReadableTerminalColor() did not adjust dark foreground")
	}
	fgR, fgG, fgB := foreground.RGB()
	boostedR, boostedG, boostedB := boosted.RGB()
	if uiRelativeLuminance(boostedR, boostedG, boostedB) <= uiRelativeLuminance(fgR, fgG, fgB) {
		t.Fatalf("uiReadableTerminalColor() did not increase luminance")
	}
	if boostedR != 0 || boostedG != 0 {
		t.Fatalf("uiReadableTerminalColor() changed the blue hue into a washed-out color")
	}
}

func TestUIReadableTerminalColorKeepsBrightForeground(t *testing.T) {
	t.Parallel()
	foreground := tcell.NewRGBColor(0xf3, 0xf4, 0xf6)
	background := tcell.NewRGBColor(0x0c, 0x10, 0x14)
	boosted := uiReadableTerminalColor(foreground, background)
	if boosted != foreground {
		t.Fatalf("uiReadableTerminalColor() changed bright foreground")
	}
}

func TestUIReadableTerminalColorKeepsReadablePaletteGreen(t *testing.T) {
	t.Parallel()
	foreground := tcell.NewRGBColor(0x00, 0xff, 0x5f)
	background := tcell.NewRGBColor(0x0c, 0x10, 0x14)
	boosted := uiReadableTerminalColor(foreground, background)
	if boosted != foreground {
		t.Fatalf("uiReadableTerminalColor() changed an already readable palette color")
	}
}

func TestUITerminalViewHandleLocalKeyScrollsScrollback(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{Box: tview.NewBox(), app: tview.NewApplication(), term: vt.NewSafeEmulator(6, 3), height: 3}
	view.term.SetScrollbackSize(32)
	view.term.Resize(6, 3)
	if _, err := view.term.Write([]byte("1\n2\n3\n4\n5\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := view.term.ScrollbackLen(); got == 0 {
		t.Fatalf("ScrollbackLen() = %d, want > 0", got)
	}
	if !view.handleLocalKey(tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModShift)) {
		t.Fatalf("handleLocalKey() = false, want true")
	}
	if got := view.currentScroll(); got == 0 {
		t.Fatalf("currentScroll() = %d, want > 0", got)
	}
}

func TestUITerminalViewHandleLocalKeyUsesFineAndPageSteps(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{Box: tview.NewBox(), app: tview.NewApplication(), term: vt.NewSafeEmulator(6, 8), height: 8}
	view.term.SetScrollbackSize(64)
	view.term.Resize(6, 8)
	data := ""
	for i := 1; i <= 20; i++ {
		data += "1\n"
	}
	if _, err := view.term.Write([]byte(data)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !view.handleLocalKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModShift)) {
		t.Fatalf("handleLocalKey(Shift+Up) = false, want true")
	}
	if got := view.currentScroll(); got != 1 {
		t.Fatalf("currentScroll() after Shift+Up = %d, want 1", got)
	}
	if !view.handleLocalKey(tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModShift)) {
		t.Fatalf("handleLocalKey(Shift+PgUp) = false, want true")
	}
	if got := view.currentScroll(); got != 7 {
		t.Fatalf("currentScroll() after Shift+PgUp = %d, want 7", got)
	}
}

func TestUITerminalViewDisablesLocalScrollInAltScreen(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{Box: tview.NewBox(), app: tview.NewApplication(), term: vt.NewSafeEmulator(6, 3), height: 3}
	view.term.SetScrollbackSize(32)
	view.term.Resize(6, 3)
	if _, err := view.term.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !view.term.IsAltScreen() {
		t.Fatalf("IsAltScreen() = false, want true")
	}
	view.scroll = 5
	if got := view.currentScroll(); got != 0 {
		t.Fatalf("currentScroll() in alt screen = %d, want 0", got)
	}
	if view.handleLocalKey(tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModShift)) {
		t.Fatalf("handleLocalKey() = true in alt screen, want false")
	}
}

func TestUITerminalViewMouseScrollStepSupportsFineAndPageModes(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{Box: tview.NewBox(), app: tview.NewApplication(), term: vt.NewSafeEmulator(6, 8), height: 8}
	if got := view.mouseScrollStep(tcell.ModNone); got != 3 {
		t.Fatalf("mouseScrollStep() = %d, want 3", got)
	}
	if got := view.mouseScrollStep(tcell.ModAlt); got != 1 {
		t.Fatalf("mouseScrollStep(Alt) = %d, want 1", got)
	}
	if got := view.mouseScrollStep(tcell.ModShift); got != 6 {
		t.Fatalf("mouseScrollStep(Shift) = %d, want 6", got)
	}
}

func TestUITerminalViewSendKeyResetsScrollbackPosition(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{Box: tview.NewBox(), app: tview.NewApplication(), term: vt.NewSafeEmulator(80, 24)}
	view.scroll = 5
	readDone := make(chan struct{}, 1)
	go func() {
		buffer := make([]byte, 8)
		_, _ = view.term.Read(buffer)
		readDone <- struct{}{}
	}()
	if ok := view.SendKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone)); !ok {
		t.Fatalf("SendKey() = false, want true")
	}
	if got := view.currentScroll(); got != 0 {
		t.Fatalf("currentScroll() = %d, want 0", got)
	}
	select {
	case <-readDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Read() timed out")
	}
}

func TestUITerminalViewFinishSelectionCopiesSelection(t *testing.T) {
	t.Parallel()
	var copied string
	view := &uiTerminalView{
		Box:      tview.NewBox(),
		app:      tview.NewApplication(),
		term:     vt.NewSafeEmulator(8, 2),
		copyText: func(value string) bool { copied = value; return true },
		width:    8,
		height:   2,
	}
	view.term.Resize(8, 2)
	if _, err := view.term.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	view.selecting = true
	view.selectionMoved = true
	view.selectionActive = true
	view.selectionStart = uiTerminalPoint{Line: 0, Column: 0}
	view.selectionEnd = uiTerminalPoint{Line: 0, Column: 4}
	view.finishSelection()
	if copied != "hello" {
		t.Fatalf("copied = %q, want %q", copied, "hello")
	}
	if !view.selectionActive {
		t.Fatalf("finishSelection() cleared selection, want active selection")
	}
}

func TestUITerminalViewPasteHandlerPastesText(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{
		Box:    tview.NewBox(),
		app:    tview.NewApplication(),
		term:   vt.NewSafeEmulator(20, 4),
		width:  20,
		height: 4,
		scroll: 3,
	}
	view.selectionActive = true
	view.selectionStart = uiTerminalPoint{Line: 0, Column: 0}
	view.selectionEnd = uiTerminalPoint{Line: 0, Column: 4}
	readCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		n, err := view.term.Read(buffer)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- append([]byte(nil), buffer[:n]...)
	}()
	handler := view.PasteHandler()
	if handler == nil {
		t.Fatalf("PasteHandler() = nil")
	}
	handler("hello\nworld", nil)
	select {
	case err := <-errCh:
		t.Fatalf("Read() error = %v", err)
	case data := <-readCh:
		if string(data) != "hello\nworld" {
			t.Fatalf("pasted = %q, want %q", string(data), "hello\nworld")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Read() timed out")
	}
	if view.selectionActive {
		t.Fatalf("PasteHandler() kept selection active")
	}
	if got := view.currentScroll(); got != 0 {
		t.Fatalf("currentScroll() = %d, want 0", got)
	}
}

func TestUITerminalViewMouseDragSelectsTerminalText(t *testing.T) {
	t.Parallel()
	var copied string
	view := &uiTerminalView{
		Box:      tview.NewBox(),
		app:      tview.NewApplication(),
		term:     vt.NewSafeEmulator(5, 2),
		copyText: func(value string) bool { copied = value; return true },
		width:    5,
		height:   2,
	}
	view.SetRect(0, 0, 5, 2)
	view.term.Resize(5, 2)
	if _, err := view.term.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	handler := view.MouseHandler()
	handler(tview.MouseLeftDown, tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModNone), func(p tview.Primitive) {})
	handler(tview.MouseMove, tcell.NewEventMouse(4, 0, tcell.Button1, tcell.ModNone), func(p tview.Primitive) {})
	handler(tview.MouseLeftUp, tcell.NewEventMouse(4, 0, tcell.ButtonNone, tcell.ModNone), func(p tview.Primitive) {})
	if !view.selectionActive {
		t.Fatalf("selectionActive = false, want true")
	}
	if copied != "hello" {
		t.Fatalf("copied = %q, want %q", copied, "hello")
	}
	if got := view.selectedText(); got != "hello" {
		t.Fatalf("selectedText() = %q, want %q", got, "hello")
	}
}

func TestUITerminalViewMouseDoubleClickSelectsWord(t *testing.T) {
	t.Parallel()
	var copied string
	view := &uiTerminalView{
		Box:      tview.NewBox(),
		app:      tview.NewApplication(),
		term:     vt.NewSafeEmulator(11, 2),
		copyText: func(value string) bool { copied = value; return true },
		width:    11,
		height:   2,
	}
	view.SetRect(0, 0, 11, 2)
	view.term.Resize(11, 2)
	if _, err := view.term.Write([]byte("hello world")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	handler := view.MouseHandler()
	handler(tview.MouseLeftDoubleClick, tcell.NewEventMouse(7, 0, tcell.Button1, tcell.ModNone), func(p tview.Primitive) {})
	if !view.selectionActive {
		t.Fatalf("selectionActive = false, want true")
	}
	if got := view.selectedText(); got != "world" {
		t.Fatalf("selectedText() = %q, want %q", got, "world")
	}
	if copied != "world" {
		t.Fatalf("copied = %q, want %q", copied, "world")
	}
}

func TestUITerminalViewAltScreenUsesModifierForLocalSelection(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{
		Box:    tview.NewBox(),
		app:    tview.NewApplication(),
		term:   vt.NewSafeEmulator(10, 4),
		width:  10,
		height: 4,
	}
	view.SetRect(0, 0, 10, 4)
	view.term.Resize(10, 4)
	if _, err := view.term.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	handler := view.MouseHandler()
	handler(tview.MouseLeftDown, tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModNone), func(p tview.Primitive) {})
	handler(tview.MouseMove, tcell.NewEventMouse(4, 0, tcell.Button1, tcell.ModNone), func(p tview.Primitive) {})
	handler(tview.MouseLeftUp, tcell.NewEventMouse(4, 0, tcell.ButtonNone, tcell.ModNone), func(p tview.Primitive) {})
	if view.selectionActive {
		t.Fatalf("selectionActive = true without modifier in alt screen")
	}
	handler(tview.MouseLeftDown, tcell.NewEventMouse(0, 0, tcell.Button1, tcell.ModShift), func(p tview.Primitive) {})
	handler(tview.MouseMove, tcell.NewEventMouse(4, 0, tcell.Button1, tcell.ModShift), func(p tview.Primitive) {})
	handler(tview.MouseLeftUp, tcell.NewEventMouse(4, 0, tcell.ButtonNone, tcell.ModShift), func(p tview.Primitive) {})
	if !view.selectionActive {
		t.Fatalf("selectionActive = false with Shift modifier in alt screen")
	}
}

func TestUITerminalViewAltScreenPlainClickGoesToRemoteTerminal(t *testing.T) {
	t.Parallel()
	view := &uiTerminalView{
		Box:    tview.NewBox(),
		app:    tview.NewApplication(),
		term:   vt.NewSafeEmulator(10, 4),
		width:  10,
		height: 4,
	}
	view.SetRect(0, 0, 10, 4)
	view.term.Resize(10, 4)
	if _, err := view.term.Write([]byte("\x1b[?1049h\x1b[?1006h\x1b[?1002h")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	readCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		n, err := view.term.Read(buffer)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- append([]byte(nil), buffer[:n]...)
	}()
	handler := view.MouseHandler()
	handler(tview.MouseLeftDown, tcell.NewEventMouse(2, 1, tcell.Button1, tcell.ModNone), func(p tview.Primitive) {})
	select {
	case err := <-errCh:
		t.Fatalf("Read() error = %v", err)
	case data := <-readCh:
		if !strings.Contains(string(data), "\x1b[<0;3;2M") {
			t.Fatalf("remote mouse payload = %q, want SGR left click", string(data))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Read() timed out")
	}
}

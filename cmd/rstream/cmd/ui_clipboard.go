// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

type uiClipboardCommand struct {
	Name string
	Args []string
}

type uiClipboard struct {
	ctx        context.Context
	command    uiClipboardCommand
	enabled    bool
	queue      chan string
	runCommand func(context.Context, uiClipboardCommand, string)
}

var (
	uiClipboardOnce    sync.Once
	uiClipboardProgram uiClipboardCommand
	uiClipboardEnabled bool
)

func newUIClipboard(ctx context.Context) *uiClipboard {
	command, ok := uiClipboardProgramForRuntime()
	return &uiClipboard{ctx: ctx, command: command, enabled: ok, queue: make(chan string, 1), runCommand: uiRunClipboardCommand}
}

func (c *uiClipboard) Copy(text string) bool {
	if c == nil || !c.enabled || strings.TrimSpace(text) == "" {
		return false
	}
	select {
	case c.queue <- text:
		return true
	default:
		return false
	}
}

func (c *uiClipboard) run() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case text := <-c.queue:
			c.runCommand(c.ctx, c.command, text)
		}
	}
}

func uiClipboardProgramForRuntime() (uiClipboardCommand, bool) {
	uiClipboardOnce.Do(func() {
		for _, candidate := range uiClipboardCandidates() {
			if _, err := exec.LookPath(candidate.Name); err == nil {
				uiClipboardProgram = candidate
				uiClipboardEnabled = true
				return
			}
		}
	})
	return uiClipboardProgram, uiClipboardEnabled
}

func uiClipboardCandidates() []uiClipboardCommand {
	switch runtime.GOOS {
	case "darwin":
		return []uiClipboardCommand{{Name: "pbcopy"}}
	case "windows":
		return []uiClipboardCommand{{Name: "clip"}}
	default:
		return []uiClipboardCommand{
			{Name: "wl-copy"},
			{Name: "xclip", Args: []string{"-selection", "clipboard"}},
			{Name: "xsel", Args: []string{"--clipboard", "--input"}},
		}
	}
}

func uiRunClipboardCommand(ctx context.Context, command uiClipboardCommand, text string) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

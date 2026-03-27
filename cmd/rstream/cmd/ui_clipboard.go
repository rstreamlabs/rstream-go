// See LICENSE file in the project root for license information.

package cmd

import (
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

type uiClipboardCommand struct {
	Name string
	Args []string
}

var (
	uiClipboardOnce    sync.Once
	uiClipboardProgram uiClipboardCommand
	uiClipboardEnabled bool
)

func uiCopyToClipboard(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	command, ok := uiClipboardProgramForRuntime()
	if !ok {
		return false
	}
	go uiRunClipboardCommand(command, text)
	return true
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

func uiRunClipboardCommand(command uiClipboardCommand, text string) {
	cmd := exec.Command(command.Name, command.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return
	}
	_, _ = io.WriteString(stdin, text)
	_ = stdin.Close()
	_ = cmd.Wait()
}

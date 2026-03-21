// See LICENSE file in the project root for license information.

//go:build !windows

package cmd

import (
	"os"
	"strings"
	"unicode"
)

func netcatShellCommand(command string) (string, []string) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-c", command}
}

func splitNetcatCommand(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	args := make([]string, 0, 4)
	var current strings.Builder
	inSingle := false
	inDouble := false
	escape := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}
	for _, ch := range command {
		switch {
		case escape:
			current.WriteRune(ch)
			escape = false
		case ch == '\\' && !inSingle:
			escape = true
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case unicode.IsSpace(ch) && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(ch)
		}
	}
	flush()
	return args
}

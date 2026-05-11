// See LICENSE file in the project root for license information.

package cmd

import "testing"

func TestTerminalSafe(t *testing.T) {
	input := "prod\x1b[31m-red\x1b[0m\nrow\t\x1b]52;c;secret\x07tail\x00"
	got := terminalSafe(input)
	if got != "prod-red row tail" {
		t.Fatalf("terminalSafe() = %q", got)
	}
	if got := terminalSafeDefault("\x1b[31m\n"); got != "-" {
		t.Fatalf("terminalSafeDefault() = %q, want -", got)
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"os"

	"golang.org/x/term"
)

type forwardUI interface {
	Start(ctx context.Context) <-chan struct{}
	Stop() error
	SetStatus(s forwardStatus)
	AddConn(ci forwardConnInfo) int
	CloseConn(idx int)
}

func stdoutIsTTY() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

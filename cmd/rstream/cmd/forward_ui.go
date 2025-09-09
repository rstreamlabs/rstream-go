// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
)

type forwardUI interface {
	Start(ctx context.Context) <-chan struct{}
	Stop() error
	SetStatus(s forwardStatus)
	AddConn(ci forwardConnInfo) int
	CloseConn(idx int)
}

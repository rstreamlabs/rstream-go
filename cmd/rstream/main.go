// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd.ExecuteContext(ctx)
}

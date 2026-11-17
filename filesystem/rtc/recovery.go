// See LICENSE file in the project root for license information.

package rtc

import (
	"context"
	"fmt"
	"io"
)

// Only an active empty-queue read gets a recovery budget. A paused consumer keeps
// its bounded receive window and authorization renewal without an idle timeout.
func (b *responseBody) nextPacket() ([]byte, error) {
	select {
	case packet := <-b.packets:
		return packet, nil
	default:
	}
	for {
		ctx := b.ctx
		stop := func() {}
		if b.needsRecovery() {
			ctx, stop = context.WithTimeout(ctx, restartRecoveryBudget)
		}
		select {
		case packet := <-b.packets:
			stop()
			return packet, nil
		case <-b.recovery:
			stop()
		case <-ctx.Done():
			stop()
			if b.ctx.Err() != nil {
				return nil, context.Cause(b.ctx)
			}
			if b.needsRecovery() {
				err := fmt.Errorf("filesystem data did not resume after ICE restart")
				b.cancel(err)
				return nil, err
			}
		case <-b.ended:
			stop()
			select {
			case packet := <-b.packets:
				return packet, nil
			default:
				_ = b.channel.SendText("done")
				return nil, io.EOF
			}
		}
	}
}

func (b *responseBody) needsRecovery() bool {
	return b.recovering.Load() && !b.complete.Load() && b.progress.Load() <= b.recoveryAt.Load()
}

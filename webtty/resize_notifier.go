// See LICENSE file in the project root for license information.

package webtty

import "time"

type terminalResizeNotifier interface {
	C() <-chan struct{}
	Close()
}

func newTerminalResizeNotifier(interval time.Duration) terminalResizeNotifier {
	return newTerminalResizeNotifierImpl(interval)
}

// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import "time"

type tickerResizeNotifier struct {
	ch     chan struct{}
	done   chan struct{}
	ticker *time.Ticker
}

func newTerminalResizeNotifierImpl(interval time.Duration) terminalResizeNotifier {
	if interval <= 0 {
		interval = 300 * time.Millisecond
	}
	n := &tickerResizeNotifier{
		ch:     make(chan struct{}, 1),
		done:   make(chan struct{}),
		ticker: time.NewTicker(interval),
	}
	go n.loop()
	n.notify()
	return n
}

func (n *tickerResizeNotifier) C() <-chan struct{} {
	return n.ch
}

func (n *tickerResizeNotifier) Close() {
	select {
	case <-n.done:
		return
	default:
		close(n.done)
		n.ticker.Stop()
	}
}

func (n *tickerResizeNotifier) loop() {
	for {
		select {
		case <-n.done:
			return
		case <-n.ticker.C:
			n.notify()
		}
	}
}

func (n *tickerResizeNotifier) notify() {
	select {
	case n.ch <- struct{}{}:
	default:
	}
}

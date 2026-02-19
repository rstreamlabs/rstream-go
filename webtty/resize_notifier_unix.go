// See LICENSE file in the project root for license information.

//go:build !windows

package webtty

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

type signalResizeNotifier struct {
	ch    chan struct{}
	sigCh chan os.Signal
	done  chan struct{}
}

func newTerminalResizeNotifierImpl(_ time.Duration) terminalResizeNotifier {
	n := &signalResizeNotifier{
		ch:    make(chan struct{}, 1),
		sigCh: make(chan os.Signal, 1),
		done:  make(chan struct{}),
	}
	signal.Notify(n.sigCh, syscall.SIGWINCH)
	go n.loop()
	n.notify()
	return n
}

func (n *signalResizeNotifier) C() <-chan struct{} {
	return n.ch
}

func (n *signalResizeNotifier) Close() {
	select {
	case <-n.done:
		return
	default:
		close(n.done)
		signal.Stop(n.sigCh)
	}
}

func (n *signalResizeNotifier) loop() {
	for {
		select {
		case <-n.done:
			return
		case <-n.sigCh:
			n.notify()
		}
	}
}

func (n *signalResizeNotifier) notify() {
	select {
	case n.ch <- struct{}{}:
	default:
	}
}

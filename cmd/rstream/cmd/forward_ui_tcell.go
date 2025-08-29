// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
)

type forwardUITCell struct {
	mu     sync.Mutex
	screen tcell.Screen
	done   chan struct{}
	status forwardStatus
	conns  []forwardConnInfo
}

func newForwardUITCell() (forwardUI, error) {
	s, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err = s.Init(); err != nil {
		return nil, err
	}
	s.SetStyle(tcell.StyleDefault.Background(tcell.ColorBlack).Foreground(tcell.ColorWhite))
	return &forwardUITCell{
		screen: s,
		done:   make(chan struct{}),
		status: forwardStatus{},
		conns:  make([]forwardConnInfo, 0),
	}, nil
}

func (u *forwardUITCell) Start(ctx context.Context) <-chan struct{} {
	go func() {
		evch := make(chan tcell.Event, 8)
		go func() {
			for {
				evch <- u.screen.PollEvent()
			}
		}()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			u.draw()
			u.screen.Show()
			select {
			case <-ctx.Done():
				u.screen.Fini()
				u.done <- struct{}{}
				return
			case ev := <-evch:
				switch e := ev.(type) {
				case *tcell.EventKey:
					switch e.Key() {
					case tcell.KeyCtrlC:
						u.screen.Fini()
						u.done <- struct{}{}
						return
					case tcell.KeyRune:
						if e.Rune() == 'q' || e.Rune() == 'Q' {
							u.screen.Fini()
							u.done <- struct{}{}
							return
						}
					}
				case *tcell.EventResize:
					u.screen.Sync()
				}
			case <-tick.C:
			}
		}
	}()
	return u.done
}

func (u *forwardUITCell) Stop() error { u.screen.Fini(); return nil }

func (u *forwardUITCell) SetStatus(s forwardStatus) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.status = s
}

func (u *forwardUITCell) AddConn(ci forwardConnInfo) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.conns = append(u.conns, ci)
	idx := len(u.conns) - 1
	return idx
}

func (u *forwardUITCell) CloseConn(idx int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if idx >= 0 && idx < len(u.conns) {
		u.conns[idx].Active = false
	}
}

func (u *forwardUITCell) draw() {
	s := u.screen
	s.Clear()
	sw, sh := s.Size()
	if sw <= 0 || sh <= 0 {
		return
	}
	top, bottom, left, right := 1, 1, 1, 1
	foot := sh - bottom - 1
	if foot < top {
		foot = top
	}
	maxRow := foot - 1
	row := top
	row = printWrappedLine(s, row, left, right, "rstream - (https://rstream.io/) - scalable tunneling from localhost to the global network", maxRow)
	row++
	row = printWrappedLine(s, row, left, right, "this program is part of rstream (https://rstream.io/download/cli) and was created using rstream Go SDK (https://rstream.io/sdk)", maxRow)
	row++
	val := func(p *string) string {
		if p == nil || *p == "" {
			return "-"
		}
		return *p
	}
	rows := [][2]string{
		{"version", "-"},
		{"update", "-"},
		{"status", val(u.status.Status)},
		{"plan", "-"},
		{"region", "-"},
		{"tunnel ID", val(u.status.TunnelID)},
		{"forwarding", val(u.status.Forwarding)},
		{"forwarded", val(u.status.Forwarded)},
	}
	lines := make([]string, 0, len(rows)+3)
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("%-12s: %s", r[0], r[1]))
	}
	lines = append(lines, "", "incoming connections:", "")
	for _, t := range lines {
		if row > maxRow {
			break
		}
		printLineTruncated(s, row, left, right, t)
		row++
	}
	if len(u.conns) == 0 {
		if row <= maxRow {
			printLineTruncated(s, row, left, right, "no connection")
			row++
		}
	} else {
		showConns := func(conns []forwardConnInfo) {
			for i := len(conns) - 1; i >= 0; i-- {
				if row > maxRow {
					break
				}
				c := conns[i]
				streamID := "-"
				if c.StreamID != nil {
					streamID = *c.StreamID
				}
				sourceIP := "-"
				if c.SourceIP != nil {
					sourceIP = c.SourceIP.String()
				}
				printLineTruncated(s, row, left, right, fmt.Sprintf("[date: %s, stream_id: %s, source_ip: %s, active:%t]", c.Date.UTC().Format("2006-01-02 15:04:05.000 UTC"), streamID, sourceIP, c.Active))
				row++
			}
		}
		var actives, inactives []forwardConnInfo
		for _, c := range u.conns {
			if c.Active {
				actives = append(actives, c)
			} else {
				inactives = append(inactives, c)
			}
		}
		showConns(actives)
		showConns(inactives)
	}
	if foot >= 0 && foot < sh {
		clearLine(s, foot-1, left, sw-right)
		printLineTruncated(s, foot, left, right, "press 'q' or 'Ctrl-C' to exit")
	}
}

func printWrappedLine(s tcell.Screen, start, left, right int, text string, maxRow int) int {
	sw, _ := s.Size()
	if sw <= left+right {
		return start
	}
	row, col := start, left
	end := sw - right
	for _, r := range []rune(text) {
		if col >= end {
			row++
			col = left
		}
		if row > maxRow {
			return row
		}
		s.SetContent(col, row, r, nil, tcell.StyleDefault)
		col++
	}
	return row + 1
}

func printLineTruncated(s tcell.Screen, row, left, right int, text string) {
	sw, _ := s.Size()
	start := left
	end := sw - right
	if end < start {
		return
	}
	w := end - start
	rs := []rune(text)
	if len(rs) > w {
		rs = rs[:w]
	}
	col := start
	for _, r := range rs {
		s.SetContent(col, row, r, nil, tcell.StyleDefault)
		col++
	}
}

func clearLine(s tcell.Screen, row, start, end int) {
	if end < start {
		return
	}
	for c := start; c < end; c++ {
		s.SetContent(c, row, ' ', nil, tcell.StyleDefault)
	}
}

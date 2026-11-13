//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

type packetResults struct {
	received   int
	duplicates int
	reordered  int
	samples    []int64
	err        error
}

func receivePackets(read func([]byte) (int, error), sentAt []atomic.Int64, start time.Time) (stats packetResults) {
	buf := make([]byte, 2048)
	seen := make([]bool, len(sentAt))
	stats.samples = make([]int64, 0, len(sentAt))
	maxSequence := -1
	var zero [992]byte
	for {
		n, err := read(buf)
		if err != nil {
			var timeout net.Error
			if !errors.As(err, &timeout) || !timeout.Timeout() {
				stats.err = err
			}
			return stats
		}
		if n != 1000 || !bytes.Equal(buf[8:n], zero[:]) {
			stats.err = fmt.Errorf("invalid datagram payload of %d bytes", n)
			return stats
		}
		sequence := binary.BigEndian.Uint64(buf[:8])
		if sequence >= uint64(len(sentAt)) {
			stats.err = errors.New("unexpected datagram sequence")
			return stats
		}
		if seen[sequence] {
			stats.duplicates++
			continue
		}
		seen[sequence] = true
		if int(sequence) < maxSequence {
			stats.reordered++
		}
		maxSequence = max(maxSequence, int(sequence))
		stats.received++
		stats.samples = append(stats.samples, time.Since(start).Nanoseconds()-sentAt[sequence].Load())
	}
}

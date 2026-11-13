//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"encoding/binary"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestPacketMetricsSeparateLossDuplicatesAndReordering(t *testing.T) {
	sequence := []uint64{2, 0, 1, 1}
	stats := receivePackets(func(buf []byte) (int, error) {
		if len(sequence) == 0 {
			return 0, io.EOF
		}
		binary.BigEndian.PutUint64(buf, sequence[0])
		sequence = sequence[1:]
		return 1000, nil
	}, make([]atomic.Int64, 4), time.Now())
	if stats.received != 3 || stats.duplicates != 1 || stats.reordered != 2 || len(stats.samples) != 3 || !errors.Is(stats.err, io.EOF) {
		t.Fatalf("unexpected packet accounting: %+v", stats)
	}
}

func TestPacketMetricsRejectMalformedEchoes(t *testing.T) {
	for _, kind := range []string{"short", "unknown sequence", "corruption"} {
		t.Run(kind, func(t *testing.T) {
			stats := receivePackets(func(buf []byte) (int, error) {
				switch kind {
				case "short":
					return 7, nil
				case "unknown sequence":
					binary.BigEndian.PutUint64(buf, 2)
				case "corruption":
					buf[99] = 1
				}
				return 1000, nil
			}, make([]atomic.Int64, 1), time.Now())
			if stats.err == nil || stats.received != 0 {
				t.Fatalf("accepted malformed packet: %+v", stats)
			}
		})
	}
}

func TestLatencyPercentilesUseNearestRank(t *testing.T) {
	p50, p95, p99 := percentiles([]int64{4000, 1000, 2000, 3000})
	if p50 != 2 || p95 != 4 || p99 != 4 {
		t.Fatalf("percentiles = %v/%v/%v", p50, p95, p99)
	}
}

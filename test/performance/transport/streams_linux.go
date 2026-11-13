//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func serveStreams(ctx context.Context, listener net.Listener, count int) error {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	var workers sync.WaitGroup
	defer workers.Wait()
	for range count {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		workers.Go(func() {
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			_, _ = io.Copy(conn, conn)
		})
	}
	return nil
}

func exchangePing(conn net.Conn) (time.Duration, error) {
	payload := bytes.Repeat([]byte{0x35}, 64)
	reply := make([]byte, len(payload))
	start := time.Now()
	if n, err := conn.Write(payload); err != nil || n != len(payload) {
		return 0, errors.Join(err, io.ErrShortWrite)
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		return 0, err
	}
	if !bytes.Equal(payload, reply) {
		return 0, errors.New("interactive payload mismatch")
	}
	return time.Since(start), nil
}

func transferStream(ctx context.Context, conn net.Conn, size int64, delay time.Duration, out *result, active *atomic.Bool) error {
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	block := bytes.Repeat([]byte{0x5a}, 64<<10)
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, len(block))
		for remaining := size; remaining > 0; {
			n := min(int64(len(buf)), remaining)
			if _, err := io.ReadFull(conn, buf[:n]); err != nil {
				readDone <- err
				return
			}
			if !bytes.Equal(buf[:n], block[:n]) {
				readDone <- errors.New("bulk payload mismatch")
				return
			}
			remaining -= n
			if delay > 0 {
				select {
				case <-ctx.Done():
					readDone <- ctx.Err()
					return
				case <-time.After(delay):
				}
			}
		}
		readDone <- nil
	}()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu, err := cpuSeconds()
	if err != nil {
		_ = conn.Close()
		return errors.Join(err, <-readDone)
	}
	start := time.Now()
	active.Store(true)
	defer active.Store(false)
	for remaining := size; remaining > 0; {
		n, err := conn.Write(block[:min(int64(len(block)), remaining)])
		if err != nil || n == 0 {
			_ = conn.Close()
			return errors.Join(err, io.ErrNoProgress, <-readDone)
		}
		remaining -= int64(n)
	}
	err = <-readDone
	afterCPU, cpuErr := cpuSeconds()
	out.Seconds, out.CPUSeconds, out.Bytes = time.Since(start).Seconds(), afterCPU-cpu, size
	runtime.ReadMemStats(&after)
	out.AllocBytes, out.Allocs = after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
	return errors.Join(err, cpuErr)
}

func applicationProbe(ctx context.Context, client *rstream.Client, target rstream.Addr, size int64, out *result) error {
	conn, err := client.Dial(ctx, target)
	if err != nil {
		return err
	}
	defer conn.Close()
	if out.Workload == "cancel" {
		if err := cancellationProbe(ctx, conn, out); err != nil {
			return err
		}
		conn, err := client.Dial(ctx, target)
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
		_, err = exchangePing(conn)
		return err
	}
	var active atomic.Bool
	if out.Workload == "slow" {
		return transferStream(ctx, conn, size, 2*time.Millisecond, out, &active)
	}
	interactive, err := client.Dial(ctx, target)
	if err != nil {
		return err
	}
	defer interactive.Close()
	deadline, _ := ctx.Deadline()
	if err := interactive.SetDeadline(deadline); err != nil {
		return err
	}
	bulkDone := make(chan error, 1)
	go func() { bulkDone <- transferStream(ctx, conn, size, 0, out, &active) }()
	samples := make([]int64, 0, out.Pings)
	for range out.Pings {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Millisecond):
		}
		if err != nil {
			break
		}
		if !active.Load() {
			continue
		}
		elapsed, pingErr := exchangePing(interactive)
		if pingErr != nil {
			err = pingErr
			break
		}
		if active.Load() {
			samples = append(samples, elapsed.Nanoseconds())
		}
	}
	if err != nil {
		_ = conn.Close()
	}
	err = errors.Join(err, <-bulkDone)
	out.ConcurrentPings = len(samples)
	out.P50US, out.P95US, out.P99US = percentiles(samples)
	if len(samples) == 0 {
		err = errors.Join(err, errors.New("no interactive sample overlapped bulk transfer"))
	}
	return err
}

func cancellationProbe(ctx context.Context, conn net.Conn, out *result) error {
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	written := make(chan error, 1)
	go func() {
		block := make([]byte, 32<<10)
		for {
			if n, err := conn.Write(block); err != nil || n == 0 {
				written <- errors.Join(err, io.ErrNoProgress)
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
	case <-time.After(50 * time.Millisecond):
	}
	start := time.Now()
	closeErr := conn.Close()
	select {
	case <-written:
		out.CancelUS = float64(time.Since(start).Nanoseconds()) / 1000
		return closeErr
	case <-time.After(2 * time.Second):
		return errors.Join(errors.New("writer did not stop after stream close"), ctx.Err())
	}
}

func sampleResources(out *result) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	out.GoroutinesBefore = runtime.NumGoroutine()
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			out.PeakHeapBytes = max(out.PeakHeapBytes, memory.HeapAlloc)
			out.PeakGoroutines = max(out.PeakGoroutines, runtime.NumGoroutine())
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		out.GoroutinesAfter = runtime.NumGoroutine()
	}
}

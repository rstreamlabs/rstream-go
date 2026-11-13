//go:build linux

// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"slices"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

type result struct {
	ConcurrentPings  int                           `json:"concurrent_pings"`
	CancelUS         float64                       `json:"cancel_us"`
	PeakHeapBytes    uint64                        `json:"peak_heap_bytes"`
	PeakGoroutines   int                           `json:"peak_goroutines"`
	GoroutinesBefore int                           `json:"goroutines_before"`
	GoroutinesAfter  int                           `json:"goroutines_after"`
	PeerQueue        *rstream.DatagramReceiveStats `json:"peer_queue,omitempty"`
	EchoQueue        *rstream.DatagramReceiveStats `json:"echo_queue,omitempty"`
	SendSeconds      float64                       `json:"send_seconds"`
	PacingMS         int                           `json:"pacing_ms"`
	DrainSeconds     int                           `json:"drain_seconds"`
	Workload         string                        `json:"workload"`
	Transport        string                        `json:"transport"`
	CrossRegion      bool                          `json:"cross_region"`
	Pings            int                           `json:"pings"`
	Seconds          float64                       `json:"seconds"`
	CPUSeconds       float64                       `json:"cpu_seconds"`
	AllocBytes       uint64                        `json:"alloc_bytes"`
	Allocs           uint64                        `json:"allocs"`
	Bytes            int64                         `json:"bytes"`
	Sent             int                           `json:"sent"`
	Received         int                           `json:"received"`
	Duplicates       int                           `json:"duplicates"`
	Reordered        int                           `json:"reordered"`
	P50US            float64                       `json:"p50_us"`
	P95US            float64                       `json:"p95_us"`
	P99US            float64                       `json:"p99_us"`
}

func cpuSeconds() (float64, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, fmt.Errorf("read process CPU usage: %w", err)
	}
	return float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6, nil
}

func percentiles(samples []int64) (float64, float64, float64) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	slices.Sort(samples)
	at := func(percent int) float64 { return float64(samples[(len(samples)*percent+99)/100-1]) / 1000 }
	return at(50), at(95), at(99)
}

func benchmarkClient(engine, transport string, roots *x509.CertPool) (*rstream.Client, error) {
	var dialer rstream.Dialer
	switch transport {
	case "tls":
		dialer = &rstream.Transport{}
	case "quic":
		dialer = &rstream.QUICTransport{}
	default:
		return nil, fmt.Errorf("unsupported transport %q", transport)
	}
	return rstream.NewClient(rstream.ClientOptions{Engine: engine, Token: os.Getenv("RSTREAM_AUTHENTICATION_TOKEN"), Transport: dialer, OwnTransport: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ZeroRTT: rstream.BoolPtr(false)})
}

func serveEcho(ctx context.Context, tunnel rstream.Tunnel, workload string, stats **rstream.DatagramReceiveStats) error {
	if workload == "stream" {
		conn, err := tunnel.(net.Listener).Accept()
		if err != nil {
			return err
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		_, err = io.Copy(conn, conn)
		return err
	}
	conn, _, err := tunnel.(rstream.DatagramTunnel).Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() {
		if got, ok := rstream.PacketConnDatagramReceiveStats(conn); ok {
			*stats = &got
		}
	}()
	buf := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		if _, err := conn.WriteTo(buf[:n], addr); err != nil {
			return err
		}
	}
}

func streamProbe(ctx context.Context, client *rstream.Client, target rstream.Addr, size int64, out *result) error {
	conn, err := client.Dial(ctx, target)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	ping := make([]byte, 64)
	reply := make([]byte, len(ping))
	samples := make([]int64, 0, 100)
	for range out.Pings {
		start := time.Now()
		if _, err := conn.Write(ping); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, reply); err != nil {
			return err
		}
		if !bytes.Equal(ping, reply) {
			return errors.New("stream ping payload mismatch")
		}
		samples = append(samples, time.Since(start).Nanoseconds())
	}
	out.P50US, out.P95US, out.P99US = percentiles(samples)
	block := bytes.Repeat([]byte{0x5a}, 64<<10)
	readDone := make(chan error, 1)
	readerReady := make(chan struct{})
	go func() {
		buf := make([]byte, len(block))
		close(readerReady)
		remaining := size
		for remaining > 0 {
			n := min(int64(len(buf)), remaining)
			if _, err := io.ReadFull(conn, buf[:n]); err != nil {
				readDone <- err
				return
			}
			if !bytes.Equal(buf[:n], block[:n]) {
				readDone <- errors.New("stream bulk payload mismatch")
				return
			}
			remaining -= n
		}
		readDone <- nil
	}()
	<-readerReady
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu, err := cpuSeconds()
	if err != nil {
		_ = conn.Close()
		<-readDone
		return err
	}
	start := time.Now()
	for remaining := size; remaining > 0; {
		n, err := conn.Write(block[:min(int64(len(block)), remaining)])
		if err != nil {
			_ = conn.Close()
			<-readDone
			return err
		}
		if n == 0 {
			_ = conn.Close()
			<-readDone
			return io.ErrNoProgress
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

func datagramProbe(ctx context.Context, client *rstream.Client, target rstream.Addr, packets int, out *result) error {
	conn, err := client.PacketDial(ctx, target)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	sentAt := make([]atomic.Int64, packets)
	start := time.Now()
	statsDone := make(chan packetResults, 1)
	go func() {
		statsDone <- receivePackets(func(buf []byte) (int, error) { n, _, err := conn.ReadFrom(buf); return n, err }, sentAt, start)
	}()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu, err := cpuSeconds()
	if err != nil {
		_ = conn.Close()
		<-statsDone
		return err
	}
	packet := make([]byte, 1000)
	for i := range packets {
		binary.BigEndian.PutUint64(packet, uint64(i))
		sentAt[i].Store(time.Since(start).Nanoseconds())
		if _, err := conn.WriteTo(packet, &target); err != nil {
			_ = conn.Close()
			<-statsDone
			return err
		}
		out.Sent++
		time.Sleep(time.Duration(out.PacingMS) * time.Millisecond)
	}
	out.SendSeconds = time.Since(start).Seconds()
	if err := conn.SetReadDeadline(time.Now().Add(time.Duration(out.DrainSeconds) * time.Second)); err != nil {
		_ = conn.Close()
		<-statsDone
		return err
	}
	stats := <-statsDone
	if got, ok := rstream.PacketConnDatagramReceiveStats(conn); ok {
		out.PeerQueue = &got
	}
	err = stats.err
	out.Received, out.Duplicates, out.Reordered = stats.received, stats.duplicates, stats.reordered
	out.P50US, out.P95US, out.P99US = percentiles(stats.samples)
	afterCPU, cpuErr := cpuSeconds()
	out.Seconds, out.CPUSeconds, out.Bytes = time.Since(start).Seconds(), afterCPU-cpu, int64(out.Received*1000)
	runtime.ReadMemStats(&after)
	out.AllocBytes, out.Allocs = after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
	if out.Received == 0 {
		return errors.New("no echoed datagrams")
	}
	return errors.Join(err, cpuErr)
}

func run() (out result, err error) {
	ownerEngine := flag.String("owner", "", "Owner engine host:port")
	peerEngine := flag.String("peer", "", "Consumer engine host:port")
	ca := flag.String("ca", "", "Trusted fixture CA PEM")
	flag.StringVar(&out.Workload, "workload", "stream", "stream, mixed, slow, cancel or datagram")
	flag.StringVar(&out.Transport, "transport", "quic", "tls or quic control transport")
	flag.BoolVar(&out.CrossRegion, "cross-region", false, "Allow routing between engines")
	flag.IntVar(&out.Pings, "pings", 100, "Stream latency samples before bulk transfer")
	size := flag.Int64("bytes", 16<<20, "Bulk stream payload bytes")
	packets := flag.Int("packets", 1000, "Datagrams to send at 1 ms intervals")
	flag.IntVar(&out.PacingMS, "pacing-ms", 1, "Datagram send pacing in milliseconds")
	flag.IntVar(&out.DrainSeconds, "drain-seconds", 3, "Post-send datagram drain in seconds")
	profilePrefix := flag.String("profile-prefix", "", "Exclusive output prefix for CPU and allocation profiles")
	flag.Parse()
	if out.PacingMS < 1 || out.PacingMS > 1000 || out.DrainSeconds < 1 || out.DrainSeconds > 30 || *ownerEngine == "" || *peerEngine == "" || *ca == "" || os.Getenv("RSTREAM_AUTHENTICATION_TOKEN") == "" || *size <= 0 || *size > 1<<31 || *packets <= 0 || *packets > 100000 || out.Pings <= 0 || out.Pings > 10000 || (out.Workload != "stream" && out.Workload != "datagram" && out.Workload != "mixed" && out.Workload != "slow" && out.Workload != "cancel") {
		return out, errors.New("invalid benchmark configuration or missing fixture authentication")
	}
	stopResources := sampleResources(&out)
	defer stopResources()
	pem, err := os.ReadFile(*ca)
	if err != nil {
		return out, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return out, errors.New("invalid fixture CA")
	}
	owner, err := benchmarkClient(*ownerEngine, out.Transport, roots)
	if err != nil {
		return out, err
	}
	defer owner.Close()
	peer, err := benchmarkClient(*peerEngine, out.Transport, roots)
	if err != nil {
		return out, err
	}
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	control, err := owner.Connect(ctx, nil)
	if err != nil {
		return out, err
	}
	defer control.Close()
	tunnelType := rstream.TunnelTypeBytestream
	if out.Workload == "datagram" {
		tunnelType = rstream.TunnelTypeDatagram
	}
	name := fmt.Sprintf("transport-benchmark-%d", time.Now().UnixNano())
	tunnel, err := control.CreateTunnel(ctx, rstream.TunnelProperties{Name: &name, Type: &tunnelType, Publish: rstream.BoolPtr(false), AllowCrossRegionRouting: &out.CrossRegion})
	if err != nil {
		return out, err
	}
	defer tunnel.Close()
	serverCtx, stopServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() {
		if out.Workload == "mixed" || out.Workload == "slow" || out.Workload == "cancel" {
			count := 2
			if out.Workload == "slow" {
				count = 1
			}
			serverDone <- serveStreams(serverCtx, tunnel.(net.Listener), count)
			return
		}
		serverDone <- serveEcho(serverCtx, tunnel, out.Workload, &out.EchoQueue)
	}()
	defer func() {
		stopServer()
		_ = tunnel.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			err = errors.Join(err, errors.New("echo server did not stop"))
		}
	}()
	stopProfiles, err := startProfiles(*profilePrefix)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, stopProfiles()) }()
	target := rstream.Addr{IdOrName: name}
	if out.Workload == "datagram" {
		err = datagramProbe(ctx, peer, target, *packets, &out)
	} else if out.Workload == "stream" {
		err = streamProbe(ctx, peer, target, *size, &out)
	} else {
		err = applicationProbe(ctx, peer, target, *size, &out)
	}
	return out, err
}

func main() {
	out, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

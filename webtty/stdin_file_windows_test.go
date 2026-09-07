// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsStdinReaderCancellationAndReuse(t *testing.T) {
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	defer input.Close()
	read, closeReader, err := resolveClientStdinRead(&ClientConfig{Stdin: stdin})
	if err != nil {
		t.Fatal(err)
	}
	defer closeReader()
	buffer := make([]byte, 32)
	for range 32 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		n, err := read(ctx, buffer)
		cancel()
		if n != 0 || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled Read = %d, %v", n, err)
		}
	}
	payload := []byte("after-cancel\x00é")
	if _, err := input.Write(payload); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	n, err := read(ctx, buffer)
	if err != nil || !bytes.Equal(buffer[:n], payload) {
		t.Fatalf("Read after cancellation = %q, %v", buffer[:n], err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := read(ctx, buffer); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("Read after EOF = %d, %v", n, err)
	}
	if err := closeReader(); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Stat(); err != nil {
		t.Fatalf("reader closed borrowed stdin: %v", err)
	}
}

func TestWindowsStdinReaderConcurrentClose(t *testing.T) {
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	started := make(chan struct{})
	reader, err := newWindowsStdinReader(func(buffer []byte) (int, error) {
		close(started)
		return stdin.Read(buffer)
	}, stdin.Close)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.close()
	results := make(chan error, 2)
	go func() {
		_, err := reader.readContext(t.Context(), make([]byte, 8))
		results <- err
	}()
	<-started
	queued, cancelQueued := context.WithCancel(t.Context())
	go func() {
		_, err := reader.readContext(queued, make([]byte, 8))
		results <- err
	}()
	cancelQueued()
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued read cancellation = %v", err)
	}
	closed := make(chan error, 4)
	for range 4 {
		go func() { closed <- reader.close() }()
	}
	select {
	case err := <-results:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("active read after Close = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the active pipe read")
	}
	for range 4 {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-reader.done:
	default:
		t.Fatal("Close returned before the reader worker stopped")
	}
}

func windowsStdinHandleCount(t *testing.T) uint32 {
	t.Helper()
	var count uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	ok, _, err := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
	if ok == 0 {
		t.Fatal(err)
	}
	return count
}

func TestWindowsStdinReaderResourceBounds(t *testing.T) {
	baselineHandles := windowsStdinHandleCount(t)
	baselineGoroutines := runtime.NumGoroutine()
	for range 64 {
		stdin, input, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		read, closeReader, err := resolveClientStdinRead(&ClientConfig{Stdin: stdin})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := read(ctx, make([]byte, 1)); !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-canceled read = %v", err)
		}
		if err := closeReader(); err != nil {
			t.Fatal(err)
		}
		if err := stdin.Close(); err != nil {
			t.Fatal(err)
		}
		if err := input.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := resolveClientStdinRead(&ClientConfig{Stdin: stdin}); err == nil {
			t.Fatal("closed stdin was accepted")
		}
	}
	if count := windowsStdinHandleCount(t); count > baselineHandles+8 {
		t.Fatalf("stdin handle count grew from %d to %d", baselineHandles, count)
	}
	if count := runtime.NumGoroutine(); count > baselineGoroutines+2 {
		t.Fatalf("stdin goroutine count grew from %d to %d", baselineGoroutines, count)
	}
}

func TestWindowsStdinHelperProcess(t *testing.T) {
	mode := os.Getenv("RSTREAM_WEBTTY_WINDOWS_STDIN_HELPER")
	if mode == "" {
		return
	}
	if mode == "echo" {
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			os.Exit(2)
		}
	} else if mode == "exit" {
		_, _ = os.Stdout.WriteString("finished")
	} else {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestRunClientWindowsStdinLifecycle(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	for _, mode := range []string{"echo", "exit"} {
		t.Run(mode, func(t *testing.T) {
			var group sync.WaitGroup
			results := make(chan error, 4)
			for range 4 {
				group.Go(func() {
					stdin, input, err := os.Pipe()
					if err != nil {
						results <- err
						return
					}
					defer stdin.Close()
					defer input.Close()
					payload := bytes.Repeat([]byte("binary\x00é\n"), 16384)
					written := make(chan error, 1)
					if mode == "echo" {
						go func() {
							_, err := input.Write(payload)
							written <- errors.Join(err, input.Close())
						}()
					}
					var stdout bytes.Buffer
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					code, err := RunClient(ctx, &ClientConfig{
						URL: testWebTTYURL(server.URL), Stdin: stdin, Stdout: &stdout,
						CmdArgs:      []string{executable, "-test.run=^TestWindowsStdinHelperProcess$"},
						EnvVars:      []string{"RSTREAM_WEBTTY_WINDOWS_STDIN_HELPER=" + mode},
						OpenDeadline: durationPtr(2 * time.Second), CloseDeadline: durationPtr(time.Second),
					})
					if mode == "echo" {
						_ = stdin.Close()
						err = errors.Join(err, <-written)
					} else {
						payload = []byte("finished")
					}
					if err != nil || code != 0 || !bytes.Equal(stdout.Bytes(), payload) {
						results <- errors.Join(err, fmt.Errorf("Windows stdin %s: exit = %d, output bytes = %d, want %d", mode, code, stdout.Len(), len(payload)))
					}
				})
			}
			group.Wait()
			close(results)
			for err := range results {
				t.Error(err)
			}
		})
	}
}

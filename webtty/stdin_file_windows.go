// See LICENSE file in the project root for license information.

package webtty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var clientCancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

type windowsStdinResult struct {
	n   int
	err error
}

type windowsStdinReader struct {
	requests  chan []byte
	results   chan windowsStdinResult
	gate      chan struct{}
	closed    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	thread    windows.Handle
	closeErr  error
}

func clientFileStdinRead(file *os.File) (clientStdinReadFunc, func() error, error) {
	if err := file.SetReadDeadline(time.Time{}); err == nil {
		return nil, nil, nil
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var fileType uint32
	var duplicate windows.Handle
	var handleErr error
	err = raw.Control(func(fd uintptr) {
		fileType, handleErr = windows.GetFileType(windows.Handle(fd))
		if handleErr == nil && (fileType == windows.FILE_TYPE_PIPE || fileType == windows.FILE_TYPE_CHAR) {
			process := windows.CurrentProcess()
			handleErr = windows.DuplicateHandle(process, windows.Handle(fd), process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS)
		}
	})
	if err != nil || handleErr != nil {
		return nil, nil, fmt.Errorf("failed to prepare stdin handle: %w", errors.Join(err, handleErr))
	}
	if fileType == windows.FILE_TYPE_DISK {
		return func(ctx context.Context, buffer []byte) (int, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return file.Read(buffer)
		}, nil, nil
	}
	if duplicate == 0 {
		return nil, nil, fmt.Errorf("unsupported stdin handle type: %d", fileType)
	}
	read := func(buffer []byte) (int, error) {
		var n uint32
		err := windows.ReadFile(duplicate, buffer, &n, nil)
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_HANDLE_EOF) || (n == 0 && err == nil) {
			err = io.EOF
		}
		return int(n), err
	}
	closeFile := func() error { return windows.CloseHandle(duplicate) }
	var mode uint32
	if fileType == windows.FILE_TYPE_CHAR && windows.GetConsoleMode(duplicate, &mode) == nil {
		// os.File preserves UTF-16 console input and partial UTF-8 reads.
		console := os.NewFile(uintptr(duplicate), file.Name())
		read, closeFile = console.Read, console.Close
	}
	reader, err := newWindowsStdinReader(read, closeFile)
	if err != nil {
		return nil, nil, err
	}
	return reader.readContext, reader.close, nil
}

func newWindowsStdinReader(read func([]byte) (int, error), closeFile func() error) (*windowsStdinReader, error) {
	if err := clientCancelSynchronousIO.Find(); err != nil {
		return nil, errors.Join(err, closeFile())
	}
	r := &windowsStdinReader{
		requests: make(chan []byte), results: make(chan windowsStdinResult),
		gate: make(chan struct{}, 1), closed: make(chan struct{}), done: make(chan struct{}),
	}
	r.gate <- struct{}{}
	ready := make(chan error, 1)
	go r.run(read, closeFile, ready)
	if err := <-ready; err != nil {
		<-r.done
		return nil, errors.Join(err, r.closeErr)
	}
	return r, nil
}

func (r *windowsStdinReader) run(read func([]byte) (int, error), closeFile func() error, ready chan<- error) {
	defer close(r.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() { r.closeErr = errors.Join(r.closeErr, closeFile()) }()
	var err error
	r.thread, err = windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		ready <- fmt.Errorf("failed to open stdin reader thread: %w", err)
		return
	}
	defer func() { r.closeErr = errors.Join(r.closeErr, windows.CloseHandle(r.thread)) }()
	ready <- nil
	for {
		select {
		case buffer := <-r.requests:
			n, err := read(buffer)
			r.results <- windowsStdinResult{n: n, err: err}
		case <-r.closed:
			return
		}
	}
}

func (r *windowsStdinReader) readContext(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	select {
	case <-r.gate:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-r.closed:
		return 0, os.ErrClosed
	}
	defer func() { r.gate <- struct{}{} }()
	select {
	case r.requests <- buffer:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-r.closed:
		return 0, os.ErrClosed
	}
	select {
	case result := <-r.results:
		return result.n, result.err
	case <-ctx.Done():
		return r.cancelRead(ctx.Err())
	case <-r.closed:
		return r.cancelRead(os.ErrClosed)
	}
}

func (r *windowsStdinReader) cancelRead(cause error) (int, error) {
	var cancelErr error
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case result := <-r.results:
			if result.n == 0 && (result.err == nil || errors.Is(result.err, windows.ERROR_OPERATION_ABORTED)) {
				return 0, errors.Join(cause, cancelErr)
			}
			return result.n, errors.Join(result.err, cancelErr)
		case <-timer.C:
			ok, _, err := clientCancelSynchronousIO.Call(uintptr(r.thread))
			if ok == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) && cancelErr == nil {
				cancelErr = fmt.Errorf("failed to cancel stdin read: %w", err)
			}
			// Cancellation can arrive before ReadFile starts. Retry only during cancellation.
			timer.Reset(10 * time.Millisecond)
		}
	}
}

func (r *windowsStdinReader) close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	<-r.done
	return r.closeErr
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const DefaultCommandOutputLimit = 8 * 1024 * 1024

var ErrCommandOutputLimit = errors.New("WebTTY command output limit exceeded")

// CommandResult keeps local collection failures separate from native completion.
// ExitCode is nil unless the process supplied a Close message, including when
// output collection or the caller's context failed.
type CommandResult struct {
	ExitCode        *int
	Stdout          string
	Stderr          string
	OutputTruncated bool
}

// CaptureCommand runs a command with bounded retained output. Interactive and
// TTY modes use the same terminal input, resizing and restoration as RunClient.
// maxOutputBytes bounds stdout and stderr together; zero selects 8 MiB.
// On overflow it requests explicit termination and drains output for up to 65
// seconds while retaining any confirmed process result. Unsupported termination
// or caller cancellation disconnects the reader and leaves completion unknown.
// Managed callers must first check Engine's explicit_termination capability.
func CaptureCommand(ctx context.Context, cfg *ClientConfig, maxOutputBytes int) (result CommandResult, runErr error) {
	if maxOutputBytes < 0 {
		return result, fmt.Errorf("WebTTY output limit must not be negative")
	}
	if maxOutputBytes == 0 {
		maxOutputBytes = DefaultCommandOutputLimit
	}
	copyConfig := cloneClientConfig(cfg)
	if !copyConfig.Interactive && !copyConfig.AllocateTTY && copyConfig.Stdin == nil && copyConfig.StdinReadContext == nil {
		copyConfig.Stdin = strings.NewReader("")
	}
	resolved, err := resolveClientConfig(copyConfig)
	if err != nil {
		return result, err
	}
	runtime := &clientRuntime{cfg: resolved, logger: resolved.Logger}
	restoreTerminal, err := runtime.prepareTerminal()
	if err != nil {
		return result, err
	}
	if restoreTerminal != nil {
		defer restoreTerminal()
	}
	forwardStdin := resolved.Interactive || !runtime.hasStdinFD || !term.IsTerminal(runtime.stdinFD)
	var readInput clientStdinReadFunc
	if forwardStdin {
		var closeInput func() error
		readInput, closeInput, err = resolveClientStdinRead(resolved)
		if err != nil {
			return result, err
		}
		if closeInput != nil {
			defer func() {
				if err := closeInput(); err != nil {
					resolved.Logger.Error("failed to close stdin reader", "error", err)
				}
			}()
		}
	}
	session, err := OpenClientSession(ctx, resolved.sessionConfig())
	if err != nil {
		return result, err
	}
	loopCtx, cancel := context.WithCancel(ctx)
	var loops sync.WaitGroup
	var stdout, stderr bytes.Buffer
	defer func() {
		cancel()
		_ = session.Close()
		loops.Wait()
		if code, err := session.Wait(); err == nil {
			result.ExitCode = &code
		}
		result.Stdout, result.Stderr = stdout.String(), stderr.String()
	}()
	inputErrors := make(chan error, 1)
	completed := make(chan clientSessionResult, 1)
	if forwardStdin {
		loops.Add(1)
		go func() {
			defer loops.Done()
			runtime.stdinSessionLoop(loopCtx, session, inputErrors, readInput)
		}()
	}
	if resolved.AllocateTTY {
		loops.Add(1)
		go func() {
			defer loops.Done()
			runtime.resizeSessionLoop(loopCtx, session, inputErrors)
		}()
	}
	loops.Add(1)
	go func() {
		defer loops.Done()
		code, err := session.Wait()
		completed <- clientSessionResult{exitCode: code, err: err}
	}()
	retain := func(event ClientSessionEvent) {
		remaining := maxOutputBytes - stdout.Len() - stderr.Len()
		count := min(remaining, len(event.Data))
		if event.Stream == ClientSessionStderr {
			_, _ = stderr.Write(event.Data[:count])
		} else {
			_, _ = stdout.Write(event.Data[:count])
		}
		result.OutputTruncated = result.OutputTruncated || count < len(event.Data)
	}
	var terminationDone <-chan error
	var terminationDeadline <-chan time.Time
	var terminationTimer *time.Timer
	defer func() {
		if terminationTimer != nil {
			terminationTimer.Stop()
		}
	}()
	events := session.Events()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			retain(event)
			if result.OutputTruncated && terminationTimer == nil {
				runErr = ErrCommandOutputLimit
				terminationTimer = time.NewTimer(65 * time.Second)
				terminationDeadline = terminationTimer.C
				requested := make(chan error, 1)
				terminationDone = requested
				loops.Add(1)
				go func() {
					defer loops.Done()
					requested <- session.RequestTermination(loopCtx)
				}()
			}
		case finished := <-completed:
			for event := range session.Events() {
				retain(event)
			}
			if result.OutputTruncated {
				runErr = errors.Join(runErr, ErrCommandOutputLimit)
			}
			return result, errors.Join(runErr, finished.err)
		case err := <-terminationDone:
			terminationDone = nil
			if err != nil {
				return result, errors.Join(runErr, err)
			}
		case err := <-inputErrors:
			return result, errors.Join(runErr, err)
		case <-terminationDeadline:
			return result, errors.Join(runErr, context.DeadlineExceeded)
		case <-ctx.Done():
			return result, errors.Join(runErr, ctx.Err())
		}
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

func runNetcatClient(ctx context.Context, cfg *netcatClientConfig) error {
	if cfg == nil {
		return fmt.Errorf("client config is required")
	}
	if cfg.Dial == nil {
		return fmt.Errorf("client dialer is required")
	}
	if cfg.Stdin == nil {
		cfg.Stdin = bytes.NewReader(nil)
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	var readStdin func(context.Context, []byte) (int, error)
	if cfg.Interactive {
		var err error
		readStdin, err = resolveNetcatStdinRead(cfg)
		if err != nil {
			return err
		}
	}
	defer closeNetcatTransport(cfg.CloseTransport, cfg.Logger)
	conn, err := cfg.Dial(ctx)
	if err != nil {
		return err
	}
	cfg.Logger.Debug("netcat client connected", "target", cfg.Target, "interactive", cfg.Interactive)
	if cfg.Exec != nil {
		defer conn.Close()
		return runNetcatExecSession(ctx, conn, cfg.Exec, cfg.HalfClose, cfg.Logger)
	}
	loopCtx, stopLoops := context.WithCancel(ctx)
	var loopWG sync.WaitGroup
	defer func() {
		stopLoops()
		_ = conn.Close()
		loopWG.Wait()
	}()
	outputErrCh := make(chan error, 1)
	loopWG.Add(1)
	go func() {
		defer loopWG.Done()
		n, err := io.Copy(cfg.Stdout, conn)
		cfg.Logger.Debug("netcat client output completed", "target", cfg.Target, "bytes", n, "error", err)
		outputErrCh <- normalizeNetcatCopyError(err)
	}()
	var inputErrCh <-chan error
	if cfg.Interactive {
		ch := make(chan error, 1)
		loopWG.Add(1)
		go func() {
			defer loopWG.Done()
			err := copyNetcatInputContext(loopCtx, conn, readStdin, cfg.HalfClose)
			cfg.Logger.Debug("netcat client input completed", "target", cfg.Target, "error", err)
			ch <- err
		}()
		inputErrCh = ch
	}
	for {
		select {
		case err := <-outputErrCh:
			_ = conn.Close()
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		case err := <-inputErrCh:
			if err != nil {
				_ = conn.Close()
				return err
			}
			inputErrCh = nil
		case <-ctx.Done():
			_ = conn.Close()
			return ctx.Err()
		}
	}
}

func copyNetcatInputContext(ctx context.Context, dst net.Conn, read func(context.Context, []byte) (int, error), halfClose bool) error {
	buffer := make([]byte, 32*1024)
	for {
		n, err := read(ctx, buffer)
		if n > 0 {
			if _, writeErr := writeNetcatAll(dst, buffer[:n]); writeErr != nil {
				return normalizeNetcatCopyError(writeErr)
			}
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		if !errors.Is(err, io.EOF) {
			return normalizeNetcatCopyError(err)
		}
		if halfClose {
			return closeNetcatWrite(dst)
		}
		return nil
	}
}

func writeNetcatAll(writer io.Writer, data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n < 0 || n > len(data) {
			return total, io.ErrShortWrite
		}
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

func copyNetcatInput(dst net.Conn, src io.Reader, halfClose bool) error {
	_, err := io.Copy(dst, src)
	if err != nil {
		return normalizeNetcatCopyError(err)
	}
	if halfClose {
		if err := closeNetcatWrite(dst); err != nil {
			return err
		}
	}
	return nil
}

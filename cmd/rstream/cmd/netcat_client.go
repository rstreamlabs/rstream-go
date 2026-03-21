// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	conn, err := cfg.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	cfg.Logger.Debug("netcat client connected", "target", cfg.Target, "interactive", cfg.Interactive)
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-doneCh:
		}
	}()
	outputErrCh := make(chan error, 1)
	go func() {
		n, err := io.Copy(cfg.Stdout, conn)
		cfg.Logger.Debug("netcat client output completed", "target", cfg.Target, "bytes", n, "error", err)
		outputErrCh <- normalizeNetcatCopyError(err)
	}()
	var inputErrCh <-chan error
	if cfg.Interactive {
		ch := make(chan error, 1)
		go func() {
			err := copyNetcatInput(conn, cfg.Stdin, cfg.HalfClose)
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

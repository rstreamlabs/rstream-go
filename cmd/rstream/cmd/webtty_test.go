// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/rstreamlabs/rstream-go/webtty/pb"
	"github.com/spf13/cobra"
)

func newTestWebTTYServerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "server"}
	cmd.Flags().String("listen", "127.0.0.1:8080", "")
	cmd.Flags().Bool("rstream", false, "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("server-id", "", "")
	cmd.Flags().String("server-enrollment", "", "")
	cmd.Flags().String("webtty-config", "", "")
	cmd.Flags().Bool("publish", false, "")
	cmd.Flags().Bool("no-publish", false, "")
	cmd.Flags().Bool("retry", false, "")
	cmd.Flags().Bool("no-retry", false, "")
	cmd.Flags().Int64("retry-interval", 5000, "")
	cmd.Flags().Int64("shutdown-timeout", 5000, "")
	cmd.Flags().String("auth-token-file", "", "")
	cmd.Flags().Bool("allow-unauthenticated", false, "")
	cmd.Flags().StringArray("allowed-origin", nil, "")
	cmd.Flags().String("execution-mode", "", "")
	cmd.Flags().String("login-user", "", "")
	cmd.Flags().Bool("allow-client-user", false, "")
	cmd.Flags().String("transport", string(webtty.WebTTYTransportWebSocket), "")
	cmd.Flags().String("tls-cert-file", "", "")
	cmd.Flags().String("tls-key-file", "", "")
	cmd.Flags().Bool("e2e", false, "")
	cmd.Flags().String("identity", "", "")
	cmd.Flags().String("identity-file", "", "")
	cmd.Flags().StringArray("authorized-client-key", nil, "")
	cmd.Flags().String("authorized-clients-file", "", "")
	cmd.Flags().StringArray("label", nil, "")
	cmd.Flags().String("fs-root", "", "")
	cmd.Flags().Bool("fs-read-only", false, "")
	cmd.Flags().Int64("fs-max-upload-size", defaultWebTTYFSMaxUploadSize, "")
	return cmd
}

func newTestWebTTYClientCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "client"}
	addWebTTYClientFlags(cmd, "text")
	return cmd
}

func TestWebTTYCommandGroupsRejectPositionalArgs(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{
		{name: "webtty", cmd: webttyCmd, args: []string{"unexpected"}},
		{name: "webtty server", cmd: webttyServerCmd, args: []string{"unexpected"}},
		{name: "webtty server enroll", cmd: webttyServerEnrollCmd, args: []string{"server-id", "unexpected"}},
		{name: "webtty identity", cmd: webttyIdentityCmd, args: []string{"unexpected"}},
		{name: "webtty known-server", cmd: webttyKnownServerCmd, args: []string{"unexpected"}},
		{name: "webtty fs", cmd: webttyFSCmd, args: []string{"unexpected"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cmd.Args == nil {
				t.Fatalf("%s does not validate positional arguments", tt.name)
			}
			if err := tt.cmd.Args(tt.cmd, tt.args); err == nil {
				t.Fatalf("%s accepted unexpected positional arguments", tt.name)
			}
		})
	}
}

func TestWebTTYHelpSeparatesCommandWorkflows(t *testing.T) {
	var out bytes.Buffer
	webttyCmd.SetOut(&out)
	defer webttyCmd.SetOut(nil)
	if err := webttyCmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	text := out.String()
	for _, section := range []string{"Connection Commands:", "Server Commands:", "Managed Session Commands:"} {
		if !strings.Contains(text, section) {
			t.Fatalf("webtty help missing %q: %s", section, text)
		}
	}
	if strings.Index(text, "Connection Commands:") > strings.Index(text, "Server Commands:") ||
		strings.Index(text, "Server Commands:") > strings.Index(text, "Managed Session Commands:") {
		t.Fatalf("webtty help groups are not ordered by workflow: %s", text)
	}
}

func TestWebTTYListHelpDoesNotExposeAlias(t *testing.T) {
	var out bytes.Buffer
	webttyListCmd.SetOut(&out)
	defer webttyListCmd.SetOut(nil)
	if err := webttyListCmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	text := out.String()
	if strings.Contains(text, "Aliases:") {
		t.Fatalf("webtty list help should not expose aliases: %s", text)
	}
}

func TestWebTTYShutdownContextIgnoresCanceledParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(t.Context())
	cancelParent()
	ctx, cancel := webTTYShutdownContext(parent, time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("shutdown context should not inherit parent cancellation: %v", ctx.Err())
	default:
	}
}

func TestWebTTYShutdownContextZeroTimeoutDoesNotExpireImmediately(t *testing.T) {
	ctx, cancel := webTTYShutdownContext(t.Context(), 0)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("zero timeout should wait for explicit cancellation: %v", ctx.Err())
	default:
	}
}

func TestCmdWebTTYInterruptHelperProcess(t *testing.T) {
	if os.Getenv("RSTREAM_CMD_WEBTTY_TEST_INTERRUPT_HELPER") != "1" {
		return
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	defer signal.Stop(signalCh)
	if _, err := os.Stdout.Write([]byte("ready\n")); err != nil {
		os.Exit(8)
	}
	select {
	case <-signalCh:
		os.Exit(7)
	case <-time.After(10 * time.Second):
		os.Exit(9)
	}
}

func TestCmdWebTTYEchoStdinHelperProcess(t *testing.T) {
	if os.Getenv("RSTREAM_CMD_WEBTTY_TEST_STDIN_HELPER") != "1" {
		return
	}
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		os.Exit(8)
	}
	os.Exit(0)
}

func TestRunWebTTYClientCaptureForwardsPipedStdin(t *testing.T) {
	zero := time.Duration(0)
	allowUnauthenticated := true
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{HeartbeatInterval: &zero, AllowUnauthenticated: &allowUnauthenticated})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(t.Context())
	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer stdin.Close()
	if _, err := input.WriteString("capture-pipe\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := input.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	deadline := time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result, err := runWebTTYClientCapture(ctx, &webtty.ClientConfig{
		URL:           "ws" + strings.TrimPrefix(server.URL, "http"),
		Interactive:   false,
		Stdin:         stdin,
		CmdArgs:       []string{os.Args[0], "-test.run=^TestCmdWebTTYEchoStdinHelperProcess$"},
		EnvVars:       []string{"RSTREAM_CMD_WEBTTY_TEST_STDIN_HELPER=1"},
		OpenDeadline:  &deadline,
		CloseDeadline: &deadline,
	})
	if err != nil {
		t.Fatalf("runWebTTYClientCapture() error = %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "capture-pipe\n" || result.Stderr != "" {
		t.Fatalf("runWebTTYClientCapture() = %#v", result)
	}
}

func TestServePlainWebTTYGracefulShutdownDeliversProtocolClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal trap test")
	}
	zero := time.Duration(0)
	closeDeadline := 2 * time.Second
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{
		HeartbeatInterval:    &zero,
		SessionCloseDeadline: &closeDeadline,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- servePlainWebTTY(ctx, listener, handler, closeDeadline, slog.Default())
	}()
	clientOpenDeadline := time.Second
	clientCloseDeadline := time.Second
	session, err := webtty.OpenClientSession(t.Context(), &webtty.SessionConfig{
		URL:           "tcp://" + listener.Addr().String(),
		CmdArgs:       []string{os.Args[0], "-test.run=^TestCmdWebTTYInterruptHelperProcess$"},
		EnvVars:       []string{"RSTREAM_CMD_WEBTTY_TEST_INTERRUPT_HELPER=1"},
		OpenDeadline:  &clientOpenDeadline,
		CloseDeadline: &clientCloseDeadline,
	})
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	waitForCmdWebTTYStdout(t, session, "ready\n")
	cancel()
	exitCode, err := session.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("shutdown exit code = %d, want trapped interrupt exit code 7", exitCode)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("servePlainWebTTY() error = %v", err)
		}
	case <-time.After(closeDeadline + time.Second):
		t.Fatalf("servePlainWebTTY() did not return after context cancellation")
	}
}

func waitForCmdWebTTYStdout(t *testing.T, session *webtty.ClientSession, want string) {
	t.Helper()
	var stdout strings.Builder
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				t.Fatalf("webtty session events closed before stdout %q", want)
			}
			if event.Stream != webtty.ClientSessionStdout {
				continue
			}
			stdout.Write(event.Data)
			if strings.Contains(stdout.String(), want) {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for stdout %q; got %q", want, stdout.String())
		}
	}
}

func TestWebTTYServerHelpShowsPrimaryWorkflowsWithoutNoisyFlags(t *testing.T) {
	var out bytes.Buffer
	webttyServerCmd.SetOut(&out)
	defer webttyServerCmd.SetOut(nil)
	if err := webttyServerCmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"rstream webtty server -v --rstream --name shell",
		"rstream webtty server -v --server-id server_id --login-user <local-username>",
		"rstream webtty server -v --webtty-config /etc/rstream/webtty/prod-shell.yaml",
		"rstream webtty server -v --listen 127.0.0.1:8080 --allow-unauthenticated",
		"existing local OS username used for every login session",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("server help missing workflow example %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{"server-binding", "e2e-policy"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("server help exposes noisy flag %q: %s", forbidden, text)
		}
	}
}

func TestWebTTYClientHelpDoesNotExposeNoHeartbeat(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: "client", cmd: webttyClientCmd},
		{name: "exec", cmd: webttyExecCmd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tt.cmd.SetOut(&out)
			defer tt.cmd.SetOut(nil)
			if err := tt.cmd.Help(); err != nil {
				t.Fatalf("Help() error = %v", err)
			}
			if strings.Contains(out.String(), "no-heartbeat") {
				t.Fatalf("%s help exposes --no-heartbeat: %s", tt.name, out.String())
			}
		})
	}
}

func TestWebTTYServerUsesRstream(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	if webttyServerUsesRstream(cmd) {
		t.Fatalf("expected --rstream to be disabled by default")
	}
	if err := cmd.Flags().Set("rstream", "true"); err != nil {
		t.Fatalf("failed to set --rstream: %v", err)
	}
	if !webttyServerUsesRstream(cmd) {
		t.Fatalf("expected --rstream to enable rstream mode")
	}
}

func TestWebTTYServerAutoReconnectDefaultsToRstreamMode(t *testing.T) {
	tests := []struct {
		name string
		set  func(*cobra.Command) error
		want bool
	}{
		{
			name: "local server does not reconnect by default",
			set:  func(*cobra.Command) error { return nil },
			want: false,
		},
		{
			name: "rstream server reconnects by default",
			set: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("rstream", "true")
			},
			want: true,
		},
		{
			name: "registered server reconnects by default",
			set: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("server-id", "prod-shell")
			},
			want: true,
		},
		{
			name: "no retry disables rstream reconnect",
			set: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("rstream", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("no-retry", "true")
			},
			want: false,
		},
		{
			name: "explicit retry enables local reconnect",
			set: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("retry", "true")
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestWebTTYServerCommand()
			if err := tt.set(cmd); err != nil {
				t.Fatalf("set flags: %v", err)
			}
			if got := webTTYServerAutoReconnect(cmd); got != tt.want {
				t.Fatalf("webTTYServerAutoReconnect() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWebTTYServerRetryInterval(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	got, err := webTTYServerRetryInterval(cmd)
	if err != nil {
		t.Fatalf("webTTYServerRetryInterval() error = %v", err)
	}
	if got != 5*time.Second {
		t.Fatalf("webTTYServerRetryInterval() = %s, want 5s", got)
	}
	if err := cmd.Flags().Set("retry-interval", "2500"); err != nil {
		t.Fatalf("failed to set --retry-interval: %v", err)
	}
	got, err = webTTYServerRetryInterval(cmd)
	if err != nil {
		t.Fatalf("webTTYServerRetryInterval() error = %v", err)
	}
	if got != 2500*time.Millisecond {
		t.Fatalf("webTTYServerRetryInterval() = %s, want 2500ms", got)
	}
	cmd = newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("retry-interval", "0"); err != nil {
		t.Fatalf("failed to set --retry-interval: %v", err)
	}
	if _, err := webTTYServerRetryInterval(cmd); err == nil || !strings.Contains(err.Error(), "--retry-interval") {
		t.Fatalf("webTTYServerRetryInterval() error = %v, want retry interval validation", err)
	}
}

func TestWebTTYServerRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "control channel eof",
			err:  errors.New("control channel closed: failed to read message: EOF"),
			want: true,
		},
		{
			name: "connection refused",
			err:  errors.New("failed to dial engine: dial tcp 127.0.0.1:443: connect: connection refused"),
			want: true,
		},
		{
			name: "closed network listener",
			err:  net.ErrClosed,
			want: true,
		},
		{
			name: "engine service unavailable",
			err:  fmt.Errorf("create tunnel: %w", &rstream.EngineError{Code: rstream.EngineErrorCodeServiceUnavailable}),
			want: true,
		},
		{
			name: "engine feature unavailable",
			err:  &rstream.EngineError{Code: rstream.EngineErrorCodeFeatureNotAvailable},
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "configuration error",
			err:  errors.New("WebTTY E2E requires an authorized client source"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webTTYServerRetryableError(tt.err); got != tt.want {
				t.Fatalf("webTTYServerRetryableError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestRunWebTTYServerRetryLoopRetriesControlChannelClosure(t *testing.T) {
	attempts := 0
	err := runWebTTYServerRetryLoop(
		t.Context(),
		slog.Default(),
		true,
		time.Millisecond,
		func() error {
			attempts++
			if attempts == 1 {
				return errors.New("control channel closed: failed to read message: EOF")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runWebTTYServerRetryLoop() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("runWebTTYServerRetryLoop() attempts = %d, want 2", attempts)
	}
}

func TestRunWebTTYServerRetryLoopHonorsDisabledReconnect(t *testing.T) {
	attempts := 0
	wantErr := errors.New("control channel closed: failed to read message: EOF")
	err := runWebTTYServerRetryLoop(
		t.Context(),
		slog.Default(),
		false,
		time.Millisecond,
		func() error {
			attempts++
			return wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWebTTYServerRetryLoop() error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("runWebTTYServerRetryLoop() attempts = %d, want 1", attempts)
	}
}

func TestRunWebTTYServerRetryLoopStopsOnNonRetryableError(t *testing.T) {
	attempts := 0
	wantErr := errors.New("WebTTY E2E requires an authorized client source")
	err := runWebTTYServerRetryLoop(
		t.Context(),
		slog.Default(),
		true,
		time.Millisecond,
		func() error {
			attempts++
			return wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWebTTYServerRetryLoop() error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("runWebTTYServerRetryLoop() attempts = %d, want 1", attempts)
	}
}

func TestRunWebTTYServerRetryLoopStopsWhenContextIsCanceledDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	err := runWebTTYServerRetryLoop(
		ctx,
		slog.Default(),
		true,
		time.Hour,
		func() error {
			attempts++
			cancel()
			return errors.New("unexpected EOF")
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runWebTTYServerRetryLoop() error = %v, want context canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("runWebTTYServerRetryLoop() attempts = %d, want 1", attempts)
	}
}

func TestApplyWebTTYServerDerivedDefaults(t *testing.T) {
	tests := []struct {
		name string
		set  func(*cobra.Command) error
		want webtty.ExecutionMode
	}{
		{
			name: "local server keeps spawn",
			set:  func(*cobra.Command) error { return nil },
			want: webtty.WebTTYExecutionModeSpawn,
		},
		{
			name: "registered server defaults to login",
			set: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("server-id", "prod-shell")
			},
			want: webtty.WebTTYExecutionModeLogin,
		},
		{
			name: "registered server keeps explicit spawn",
			set: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "prod-shell"); err != nil {
					return err
				}
				return cmd.Flags().Set("execution-mode", string(webtty.WebTTYExecutionModeSpawn))
			},
			want: webtty.WebTTYExecutionModeSpawn,
		},
		{
			name: "registered server keeps explicit login",
			set: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "prod-shell"); err != nil {
					return err
				}
				return cmd.Flags().Set("execution-mode", string(webtty.WebTTYExecutionModeLogin))
			},
			want: webtty.WebTTYExecutionModeLogin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestWebTTYServerCommand()
			if err := tt.set(cmd); err != nil {
				t.Fatalf("set flags: %v", err)
			}
			if err := applyWebTTYServerDerivedDefaults(cmd); err != nil {
				t.Fatalf("applyWebTTYServerDerivedDefaults() error = %v", err)
			}
			got, err := webTTYExecutionModeFromFlag(cmd)
			if err != nil {
				t.Fatalf("webTTYExecutionModeFromFlag() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("execution-mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebTTYServerAllowedOrigins(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	got, err := webTTYServerAllowedOrigins(cmd, false)
	if err != nil {
		t.Fatalf("webTTYServerAllowedOrigins returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("local webtty server should not allow cross-origin requests by default: %#v", got)
	}
	if err := cmd.Flags().Set("allowed-origin", "http://127.0.0.1:3000"); err != nil {
		t.Fatalf("failed to set --allowed-origin: %v", err)
	}
	got, err = webTTYServerAllowedOrigins(cmd, false)
	if err != nil {
		t.Fatalf("webTTYServerAllowedOrigins returned error: %v", err)
	}
	if len(got) != 1 || got[0] != "http://127.0.0.1:3000" {
		t.Fatalf("local webtty server should honor explicit browser origins, got %#v", got)
	}
	cmd = newTestWebTTYServerCommand()
	got, err = webTTYServerAllowedOrigins(cmd, true)
	if err != nil {
		t.Fatalf("webTTYServerAllowedOrigins returned error: %v", err)
	}
	if len(got) != 1 || got[0] != "*" {
		t.Fatalf("rstream webtty server should accept browser origins through tunnel auth, got %#v", got)
	}
}

func TestResolveWebTTYExecURLUsesAdvertisedPath(t *testing.T) {
	tests := []struct {
		raw      string
		execPath string
		want     string
	}{
		{raw: "rstrm://shell", execPath: "/exec", want: "rstrm://shell/exec"},
		{raw: "rstrm://shell/base", execPath: "/exec", want: "rstrm://shell/base/exec"},
		{raw: "ws://127.0.0.1:8080", execPath: "/exec", want: "ws://127.0.0.1:8080/exec"},
		{raw: "wss://shell.example/base?token=1", execPath: "/exec", want: "wss://shell.example/base/exec?token=1"},
		{raw: "rstrm://shell", execPath: "/", want: "rstrm://shell/"},
		{raw: "rstrm://shell/exec", execPath: "/exec", want: "rstrm://shell/exec"},
	}
	for _, tt := range tests {
		t.Run(tt.raw+" "+tt.execPath, func(t *testing.T) {
			got, err := resolveWebTTYExecURL(tt.raw, tt.execPath)
			if err != nil {
				t.Fatalf("resolveWebTTYExecURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveWebTTYExecURL(%q, %q) = %q, want %q", tt.raw, tt.execPath, got, tt.want)
			}
		})
	}
}

func TestWebTTYURLWithRstreamTarget(t *testing.T) {
	got, err := webTTYURLWithRstreamTarget("rstrm://prod-shell/exec?mode=interactive", "cmq-server-id")
	if err != nil {
		t.Fatalf("webTTYURLWithRstreamTarget() error = %v", err)
	}
	if got != "rstrm://cmq-server-id/exec?mode=interactive" {
		t.Fatalf("rewritten URL = %q", got)
	}
	if _, err := webTTYURLWithRstreamTarget("ws://127.0.0.1:8080", "cmq-server-id"); err == nil {
		t.Fatalf("expected non-rstream URL to fail")
	}
}

func TestWebTTYURLWithManagedSessionMode(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		interactive bool
		want        string
	}{
		{
			name:        "non interactive rstream url",
			raw:         "rstrm://prod-shell/exec",
			interactive: false,
			want:        "rstrm://prod-shell/exec?session_mode=non-interactive",
		},
		{
			name:        "interactive rstream url",
			raw:         "rstrm://prod-shell/exec",
			interactive: true,
			want:        "rstrm://prod-shell/exec?session_mode=interactive",
		},
		{
			name:        "preserves explicit session mode",
			raw:         "rstrm://prod-shell/exec?session_mode=non-interactive",
			interactive: true,
			want:        "rstrm://prod-shell/exec?session_mode=non-interactive",
		},
		{
			name:        "preserves existing query",
			raw:         "rstrm://prod-shell/exec?origin=codex",
			interactive: false,
			want:        "rstrm://prod-shell/exec?origin=codex&session_mode=non-interactive",
		},
		{
			name:        "leaves websocket url unchanged",
			raw:         "ws://127.0.0.1:8080/exec",
			interactive: false,
			want:        "ws://127.0.0.1:8080/exec",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := webTTYURLWithManagedSessionMode(tt.raw, tt.interactive)
			if err != nil {
				t.Fatalf("webTTYURLWithManagedSessionMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("webTTYURLWithManagedSessionMode(%q, %v) = %q, want %q", tt.raw, tt.interactive, got, tt.want)
			}
		})
	}
}

func TestResolveControlPlaneWebTTYServerTargetUsesExactNameOrID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/tunnels/project-1/webtty/servers" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("q") != "prod-shell" || r.URL.Query().Get("pageSize") != "100" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{
			Servers: []controlplane.WebTTYServer{
				{ID: "cmq-other", Name: "prod-shell-backup"},
				{ID: "cmq-prod", Name: "prod-shell"},
			},
			Page: 1, PageSize: 100, Total: 2, TotalPages: 1,
		})
	}))
	defer server.Close()
	client := controlplane.NewClient(server.URL, "token")
	got, err := resolveControlPlaneWebTTYServerTarget(t.Context(), client, "project-1", "prod-shell")
	if err != nil {
		t.Fatalf("resolveControlPlaneWebTTYServerTarget() error = %v", err)
	}
	if got == nil || got.ID != "cmq-prod" {
		t.Fatalf("resolved server = %#v", got)
	}
}

func TestValidateWebTTYServerFlags(t *testing.T) {
	tests := []struct {
		name    string
		config  func(*cobra.Command) error
		wantErr bool
	}{
		{
			name: "registered server implies rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("server-id", "server-1")
			},
			wantErr: false,
		},
		{
			name: "registered server rejects listen override",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "server-1"); err != nil {
					return err
				}
				return cmd.Flags().Set("listen", ":9090")
			},
			wantErr: true,
		},
		{
			name: "registered server accepts private tunnel",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("rstream", "true"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("server-id", "server-1"); err != nil {
					return err
				}
				return cmd.Flags().Set("no-publish", "true")
			},
			wantErr: false,
		},
		{
			name: "registered server rejects private webtransport tunnel",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "server-1"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("transport", "webtransport"); err != nil {
					return err
				}
				return cmd.Flags().Set("no-publish", "true")
			},
			wantErr: true,
		},
		{
			name: "registered login mode requires OS user policy",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "server-1"); err != nil {
					return err
				}
				return cmd.Flags().Set("execution-mode", "login")
			},
			wantErr: true,
		},
		{
			name: "registered login mode accepts default OS user",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "server-1"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("execution-mode", "login"); err != nil {
					return err
				}
				return cmd.Flags().Set("login-user", "operator")
			},
			wantErr: false,
		},
		{
			name: "registered login mode accepts client-selected OS user policy",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("server-id", "server-1"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("execution-mode", "login"); err != nil {
					return err
				}
				return cmd.Flags().Set("allow-client-user", "true")
			},
			wantErr: false,
		},
		{
			name: "name requires rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("name", "shell")
			},
			wantErr: true,
		},
		{
			name: "publish requires rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("publish", "true")
			},
			wantErr: true,
		},
		{
			name: "no publish requires rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("no-publish", "true")
			},
			wantErr: true,
		},
		{
			name: "listen conflicts with rstream",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", ":9090"); err != nil {
					return err
				}
				return cmd.Flags().Set("rstream", "true")
			},
			wantErr: true,
		},
		{
			name: "fs read only requires fs root",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("fs-read-only", "true")
			},
			wantErr: true,
		},
		{
			name: "fs max upload requires fs root",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("fs-max-upload-size", "1024")
			},
			wantErr: true,
		},
		{
			name: "fs root accepts fs settings",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("fs-root", "."); err != nil {
					return err
				}
				return cmd.Flags().Set("fs-read-only", "true")
			},
			wantErr: false,
		},
		{
			name: "plain transport rejects filesystem sidecar",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "plain"); err != nil {
					return err
				}
				return cmd.Flags().Set("fs-root", ".")
			},
			wantErr: true,
		},
		{
			name: "e2e rejects filesystem sidecar",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("fs-root", "."); err != nil {
					return err
				}
				return cmd.Flags().Set("e2e", "true")
			},
			wantErr: true,
		},
		{
			name: "webtransport requires TLS files",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("transport", "webtransport")
			},
			wantErr: true,
		},
		{
			name: "webtransport accepts rstream server without local TLS files",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "webtransport"); err != nil {
					return err
				}
				return cmd.Flags().Set("rstream", "true")
			},
			wantErr: false,
		},
		{
			name: "webtransport rstream rejects local TLS files",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "webtransport"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("rstream", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("tls-cert-file", "server.crt")
			},
			wantErr: true,
		},
		{
			name: "webtransport accepts local TLS files",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "webtransport"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("tls-cert-file", "server.crt"); err != nil {
					return err
				}
				return cmd.Flags().Set("tls-key-file", "server.key")
			},
			wantErr: false,
		},
		{
			name: "plain accepts local TLS files",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "plain"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("tls-cert-file", "server.crt"); err != nil {
					return err
				}
				return cmd.Flags().Set("tls-key-file", "server.key")
			},
			wantErr: false,
		},
		{
			name: "plain rejects partial TLS files",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "plain"); err != nil {
					return err
				}
				return cmd.Flags().Set("tls-cert-file", "server.crt")
			},
			wantErr: true,
		},
		{
			name: "plain TLS rejects rstream server",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("transport", "plain"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("tls-cert-file", "server.crt"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("tls-key-file", "server.key"); err != nil {
					return err
				}
				return cmd.Flags().Set("rstream", "true")
			},
			wantErr: true,
		},
		{
			name: "websocket rejects TLS files",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("tls-cert-file", "server.crt"); err != nil {
					return err
				}
				return cmd.Flags().Set("tls-key-file", "server.key")
			},
			wantErr: true,
		},
		{
			name: "invalid execution mode",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("execution-mode", "sudo")
			},
			wantErr: true,
		},
		{
			name: "login settings require login mode",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("login-user", "alice")
			},
			wantErr: true,
		},
		{
			name: "login mode requires a local user policy",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("execution-mode", "login")
			},
			wantErr: true,
		},
		{
			name: "login mode accepts default user",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("execution-mode", "login"); err != nil {
					return err
				}
				return cmd.Flags().Set("login-user", "alice")
			},
			wantErr: false,
		},
		{
			name: "rstream without listen override is valid",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("rstream", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("name", "shell")
			},
			wantErr: false,
		},
		{
			name: "rstream rejects local auth token file",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("rstream", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("auth-token-file", "webtty.token")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestWebTTYServerCommand()
			if err := tt.config(cmd); err != nil {
				t.Fatalf("failed to configure flags: %v", err)
			}
			err := validateWebTTYServerFlags(cmd)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewWebTTYServerTunnelProperties(t *testing.T) {
	t.Run("published by default", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if props.Publish == nil || !*props.Publish {
			t.Fatalf("expected published tunnel by default")
		}
		if props.Protocol == nil || *props.Protocol != "http" {
			t.Fatalf("expected HTTP protocol for published tunnel, got %#v", props.Protocol)
		}
		if props.HTTPVersion == nil || *props.HTTPVersion != "http/1.1" {
			t.Fatalf("expected HTTP/1.1 for published tunnel, got %#v", props.HTTPVersion)
		}
		if props.TokenAuth == nil || !*props.TokenAuth {
			t.Fatalf("expected token auth for published tunnel")
		}
		if got := props.Labels[webtty.WebTTYApplicationProtocolKey]; got != webtty.WebTTYApplicationProtocol {
			t.Fatalf("unexpected application-protocol label: got %q want %q", got, webtty.WebTTYApplicationProtocol)
		}
		if got := props.Labels[webtty.WebTTYCapabilitiesLabelKey]; got != webtty.WebTTYCapabilityExec {
			t.Fatalf("unexpected capabilities label: got %q want %q", got, webtty.WebTTYCapabilityExec)
		}
		if got := props.Labels[webtty.WebTTYExecPathLabelKey]; got != webtty.WebTTYDefaultExecPath {
			t.Fatalf("unexpected exec path label: got %q want %q", got, webtty.WebTTYDefaultExecPath)
		}
		if got := props.Labels[webtty.WebTTYExecutionModeLabelKey]; got != string(webtty.WebTTYExecutionModeSpawn) {
			t.Fatalf("unexpected execution mode label: got %q want %q", got, webtty.WebTTYExecutionModeSpawn)
		}
	})
	t.Run("login execution mode is advertised", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("execution-mode", "login"); err != nil {
			t.Fatalf("failed to set --execution-mode: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if got := props.Labels[webtty.WebTTYExecutionModeLabelKey]; got != string(webtty.WebTTYExecutionModeLogin) {
			t.Fatalf("unexpected execution mode label: got %q want %q", got, webtty.WebTTYExecutionModeLogin)
		}
	})
	t.Run("plain tunnel server remains an HTTP-labelled WebTTY tunnel", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("transport", "plain"); err != nil {
			t.Fatalf("failed to set --transport: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if props.Publish == nil || !*props.Publish {
			t.Fatalf("expected published tunnel by default")
		}
		if props.Protocol == nil || *props.Protocol != "http" {
			t.Fatalf("expected HTTP protocol for tunnel server, got %#v", props.Protocol)
		}
		if props.Type != nil {
			t.Fatalf("tunnel WebTTY server should not force type, got %#v", props.Type)
		}
		if props.HTTPVersion == nil || *props.HTTPVersion != "http/1.1" {
			t.Fatalf("tunnel WebTTY server should use HTTP/1.1, got %#v", props.HTTPVersion)
		}
	})
	t.Run("webtransport tunnel server uses HTTP3 datagram tunnel", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("transport", "webtransport"); err != nil {
			t.Fatalf("failed to set --transport: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if props.Protocol == nil || *props.Protocol != "http" {
			t.Fatalf("expected HTTP protocol for WebTransport tunnel server, got %#v", props.Protocol)
		}
		if props.Type == nil || *props.Type != "datagram" {
			t.Fatalf("expected datagram tunnel type for WebTransport tunnel server, got %#v", props.Type)
		}
		if props.HTTPVersion == nil || *props.HTTPVersion != "h3" {
			t.Fatalf("expected HTTP/3 for WebTransport tunnel server, got %#v", props.HTTPVersion)
		}
	})
	t.Run("registered server uses managed webtty protocol", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		identity, err := webtty.GenerateE2EIdentity()
		if err != nil {
			t.Fatalf("GenerateE2EIdentity() error = %v", err)
		}
		enrollment := &webTTYServerEnrollmentFile{
			ServerID:          "server-1",
			ServerPublicKey:   webtty.EncodeE2EKeyMaterial(identity.PublicKey),
			ServerFingerprint: webTTYServerPublicKeyFingerprint(identity.PublicKey),
		}
		props := newWebTTYServerTunnelProperties(cmd, enrollment)
		if props.Name == nil || *props.Name != "server-1" {
			t.Fatalf("expected registered server tunnel name to default to server ID, got %#v", props.Name)
		}
		if props.Protocol == nil || *props.Protocol != "webtty" {
			t.Fatalf("expected WebTTY protocol for registered server, got %#v", props.Protocol)
		}
		if props.Type == nil || *props.Type != "bytestream" {
			t.Fatalf("expected bytestream tunnel type for registered WebTTY, got %#v", props.Type)
		}
		if props.HTTPVersion != nil {
			t.Fatalf("managed WebTTY tunnel should not set HTTP version, got %#v", props.HTTPVersion)
		}
		if got := props.Labels[webtty.WebTTYServerIDLabelKey]; got != "server-1" {
			t.Fatalf("unexpected registered server label: got %q want server-1", got)
		}
		if got := props.Labels[webtty.WebTTYEncryptionPolicyLabelKey]; got != "" {
			t.Fatalf("registered server without encryption policy should not advertise policy, got %q", got)
		}
		if got := props.Labels[webtty.WebTTYE2ELabelKey]; got != webtty.WebTTYE2EDisabled {
			t.Fatalf("unexpected registered E2E label: got %q want %q", got, webtty.WebTTYE2EDisabled)
		}
		if got := props.Labels[webtty.WebTTYClientProofLabelKey]; got != webtty.WebTTYClientProofNone {
			t.Fatalf("unexpected registered client proof label: got %q want %q", got, webtty.WebTTYClientProofNone)
		}
		if got := props.Labels[webtty.WebTTYHostKeyIDLabelKey]; got != "" {
			t.Fatalf("registered server with disabled encryption should not advertise host key id, got %q", got)
		}
		enrollment.EncryptionPolicy = webTTYServerEncryptionPolicyExplicitKey
		props = newWebTTYServerTunnelProperties(cmd, enrollment)
		if got := props.Labels[webtty.WebTTYEncryptionPolicyLabelKey]; got != webTTYServerEncryptionPolicyExplicitKey {
			t.Fatalf("unexpected encryption policy label: got %q want %q", got, webTTYServerEncryptionPolicyExplicitKey)
		}
		if got := props.Labels[webtty.WebTTYE2ELabelKey]; got != webtty.WebTTYE2ERequired {
			t.Fatalf("unexpected registered E2E label: got %q want %q", got, webtty.WebTTYE2ERequired)
		}
		if got := props.Labels[webtty.WebTTYClientProofLabelKey]; got != webtty.WebTTYClientProofRequired {
			t.Fatalf("unexpected registered client proof label: got %q want %q", got, webtty.WebTTYClientProofRequired)
		}
		if got := props.Labels[webtty.WebTTYHostKeyIDLabelKey]; got != webtty.EncodeE2EKeyMaterial(webtty.E2EKeyID(identity.PublicKey)) {
			t.Fatalf("unexpected E2E host key id label: got %q", got)
		}
	})
	t.Run("registered webtransport server uses managed datagram protocol", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("transport", "webtransport"); err != nil {
			t.Fatalf("failed to set --transport: %v", err)
		}
		enrollment := &webTTYServerEnrollmentFile{ServerID: "server-1"}
		props := newWebTTYServerTunnelProperties(cmd, enrollment)
		if props.Protocol == nil || *props.Protocol != "webtty" {
			t.Fatalf("expected WebTTY protocol for registered WebTransport server, got %#v", props.Protocol)
		}
		if props.Type == nil || *props.Type != "datagram" {
			t.Fatalf("expected datagram tunnel type for registered WebTransport server, got %#v", props.Type)
		}
		if props.HTTPVersion != nil {
			t.Fatalf("managed WebTransport tunnel should not set HTTP version, got %#v", props.HTTPVersion)
		}
	})
	t.Run("private registered server keeps managed webtty protocol", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("no-publish", "true"); err != nil {
			t.Fatalf("failed to set --no-publish: %v", err)
		}
		enrollment := &webTTYServerEnrollmentFile{ServerID: "server-1"}
		props := newWebTTYServerTunnelProperties(cmd, enrollment)
		if props.Publish == nil || *props.Publish {
			t.Fatalf("expected private registered tunnel")
		}
		if props.Protocol == nil || *props.Protocol != "webtty" {
			t.Fatalf("expected WebTTY protocol for private registered server, got %#v", props.Protocol)
		}
		if props.Type == nil || *props.Type != "bytestream" {
			t.Fatalf("expected bytestream private registered server, got %#v", props.Type)
		}
		if props.TokenAuth != nil {
			t.Fatalf("expected private registered server to omit edge token auth, got %#v", props.TokenAuth)
		}
	})
	t.Run("private tunnel omits HTTP edge settings", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("name", "shell"); err != nil {
			t.Fatalf("failed to set --name: %v", err)
		}
		if err := cmd.Flags().Set("no-publish", "true"); err != nil {
			t.Fatalf("failed to set --no-publish: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if props.Name == nil || *props.Name != "shell" {
			t.Fatalf("unexpected tunnel name: %#v", props.Name)
		}
		if props.Publish == nil || *props.Publish {
			t.Fatalf("expected private tunnel")
		}
		if props.Protocol != nil {
			t.Fatalf("expected protocol to be unset for private tunnel, got %#v", props.Protocol)
		}
		if props.HTTPVersion != nil {
			t.Fatalf("expected HTTP version to be unset for private tunnel, got %#v", props.HTTPVersion)
		}
		if props.TokenAuth != nil {
			t.Fatalf("expected token auth to be unset for private tunnel, got %#v", props.TokenAuth)
		}
	})
	t.Run("filesystem sidecar adds capability labels", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("fs-root", "."); err != nil {
			t.Fatalf("failed to set --fs-root: %v", err)
		}
		if err := cmd.Flags().Set("fs-read-only", "true"); err != nil {
			t.Fatalf("failed to set --fs-read-only: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if got := props.Labels[webtty.WebTTYCapabilitiesLabelKey]; got != "exec,fs" {
			t.Fatalf("unexpected capabilities label: got %q want exec,fs", got)
		}
		if got := props.Labels[webtty.WebTTYFSPathLabelKey]; got != webtty.WebTTYDefaultFSPath {
			t.Fatalf("unexpected fs path label: got %q want %q", got, webtty.WebTTYDefaultFSPath)
		}
		if got := props.Labels[webtty.WebTTYFSModeLabelKey]; got != webtty.WebTTYFSModeReadOnly {
			t.Fatalf("unexpected fs mode label: got %q want %q", got, webtty.WebTTYFSModeReadOnly)
		}
	})
	t.Run("custom labels are scoped to webtty inventory", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("label", "role=codex"); err != nil {
			t.Fatalf("failed to set --label: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd, nil)
		if got := props.Labels[webtty.WebTTYCustomLabelPrefix+"role"]; got != "codex" {
			t.Fatalf("unexpected custom label: got %q want codex", got)
		}
	})
}

func TestApplyWebTTYRuntimeSecurityLabels(t *testing.T) {
	t.Run("lightweight plaintext", func(t *testing.T) {
		labels := map[string]string{}
		applyWebTTYRuntimeSecurityLabels(labels, webTTYServerPayloadCryptoConfig{}, nil, false, "")
		if got := labels[webtty.WebTTYE2ELabelKey]; got != webtty.WebTTYE2EDisabled {
			t.Fatalf("unexpected E2E label: got %q want %q", got, webtty.WebTTYE2EDisabled)
		}
		if got := labels[webtty.WebTTYClientProofLabelKey]; got != webtty.WebTTYClientProofNone {
			t.Fatalf("unexpected client proof label: got %q want %q", got, webtty.WebTTYClientProofNone)
		}
		if got := labels[webtty.WebTTYEncryptionPolicyLabelKey]; got != webTTYServerEncryptionPolicyDisabled {
			t.Fatalf("unexpected encryption policy label: got %q want %q", got, webTTYServerEncryptionPolicyDisabled)
		}
	})
	t.Run("lightweight explicit key e2e", func(t *testing.T) {
		labels := map[string]string{}
		applyWebTTYRuntimeSecurityLabels(labels, webTTYServerPayloadCryptoConfig{
			EndpointIdentity: &webtty.WebTTYEndpointIdentity{},
		}, nil, true, "host-key")
		if got := labels[webtty.WebTTYHostKeyIDLabelKey]; got != "host-key" {
			t.Fatalf("unexpected host key id label: got %q want host-key", got)
		}
		if got := labels[webtty.WebTTYE2ELabelKey]; got != webtty.WebTTYE2ERequired {
			t.Fatalf("unexpected E2E label: got %q want %q", got, webtty.WebTTYE2ERequired)
		}
		if got := labels[webtty.WebTTYClientProofLabelKey]; got != webtty.WebTTYClientProofRequired {
			t.Fatalf("unexpected client proof label: got %q want %q", got, webtty.WebTTYClientProofRequired)
		}
		if got := labels[webtty.WebTTYEncryptionPolicyLabelKey]; got != webTTYServerEncryptionPolicyExplicitKey {
			t.Fatalf("unexpected encryption policy label: got %q want %q", got, webTTYServerEncryptionPolicyExplicitKey)
		}
	})
	t.Run("registered policy wins", func(t *testing.T) {
		labels := map[string]string{
			webtty.WebTTYEncryptionPolicyLabelKey: webTTYServerEncryptionPolicyWorkspaceManaged,
		}
		applyWebTTYRuntimeSecurityLabels(labels, webTTYServerPayloadCryptoConfig{
			EndpointIdentity: &webtty.WebTTYEndpointIdentity{},
		}, &webTTYServerEnrollmentFile{
			EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
		}, true, "host-key")
		if got := labels[webtty.WebTTYEncryptionPolicyLabelKey]; got != webTTYServerEncryptionPolicyWorkspaceManaged {
			t.Fatalf("unexpected encryption policy label: got %q want %q", got, webTTYServerEncryptionPolicyWorkspaceManaged)
		}
		if got := labels[webtty.WebTTYE2ELabelKey]; got != webtty.WebTTYE2ERequired {
			t.Fatalf("unexpected E2E label: got %q want %q", got, webtty.WebTTYE2ERequired)
		}
		if got := labels[webtty.WebTTYClientProofLabelKey]; got != webtty.WebTTYClientProofRequired {
			t.Fatalf("unexpected client proof label: got %q want %q", got, webtty.WebTTYClientProofRequired)
		}
		if got := labels[webtty.WebTTYHostKeyIDLabelKey]; got != "host-key" {
			t.Fatalf("unexpected host key id label: got %q want host-key", got)
		}
	})
}

func TestApplyWebTTYServerAdmissionLabelSignsRegisteredPlainServer(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	identityPath := filepath.Join(t.TempDir(), "server.identity.json")
	data, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	if err := os.WriteFile(identityPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	enrollment := &webTTYServerEnrollmentFile{
		WorkspaceID:            "workspace-1",
		ProjectID:              "project-1",
		ServerID:               "server-1",
		IdentityFile:           identityPath,
		ServerPublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		ServerSigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		ServerSigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ServerFingerprint:      webTTYServerPublicKeyFingerprint(identity.Encryption.PublicKey),
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:       webTTYServerEncryptionPolicyDisabled,
	}
	cmd := newTestWebTTYServerCommand()
	props := newWebTTYServerTunnelProperties(cmd, enrollment)
	applyWebTTYRuntimeSecurityLabels(props.Labels, webTTYServerPayloadCryptoConfig{}, enrollment, false, "")
	if err := applyWebTTYServerAdmissionLabel(&props, enrollment); err != nil {
		t.Fatalf("applyWebTTYServerAdmissionLabel() error = %v", err)
	}
	label := props.Labels[webtty.WebTTYServerAdmissionLabelKey]
	if label == "" {
		t.Fatal("expected registered WebTTY server admission label")
	}
	proofData, err := webtty.DecodeE2EKeyMaterial(label, 0, "server admission label")
	if err != nil {
		t.Fatalf("DecodeE2EKeyMaterial(label) error = %v", err)
	}
	var proof webtty.WebTTYServerAdmissionProof
	if err := json.Unmarshal(proofData, &proof); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if proof.ServerID != "server-1" || proof.ProjectID != "project-1" || proof.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected admission proof target: %#v", proof)
	}
	if proof.SigningKeyID != webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID) {
		t.Fatalf("unexpected signing key id: got %q", proof.SigningKeyID)
	}
	if proof.LabelsSHA256 != webtty.EncodeE2EKeyMaterial(webtty.WebTTYServerAdmissionLabelsHash(props.Labels)) {
		t.Fatalf("admission proof labels hash does not match tunnel labels")
	}
}

func TestApplyWebTTYServerAdmissionLabelSkipsLightweightHTTPServer(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	props := newWebTTYServerTunnelProperties(cmd, nil)
	if err := applyWebTTYServerAdmissionLabel(&props, nil); err != nil {
		t.Fatalf("applyWebTTYServerAdmissionLabel() error = %v", err)
	}
	if got := props.Labels[webtty.WebTTYServerAdmissionLabelKey]; got != "" {
		t.Fatalf("lightweight HTTP server should not advertise admission proof, got %q", got)
	}
}

func TestWebTTYClientSecurityScopeFromServerInfo(t *testing.T) {
	target := "registered-shell"
	hostKeyID := "sha256:host-key"
	tests := []struct {
		name            string
		server          *webtty.ServerInfo
		wantTarget      string
		wantHostKeyID   string
		wantE2ERequired bool
		wantClientProof bool
	}{
		{
			name:       "plain server has no security requirement",
			server:     &webtty.ServerInfo{Target: target},
			wantTarget: target,
		},
		{
			name: "explicit E2E requires encryption without client proof",
			server: &webtty.ServerInfo{
				Target: target,
				E2E:    rstream.StringPtr(webtty.WebTTYE2ERequired),
			},
			wantTarget:      target,
			wantE2ERequired: true,
		},
		{
			name: "client proof implies E2E",
			server: &webtty.ServerInfo{
				Target:      target,
				ClientProof: rstream.StringPtr(webtty.WebTTYClientProofRequired),
			},
			wantTarget:      target,
			wantE2ERequired: true,
			wantClientProof: true,
		},
		{
			name: "host key keeps target scoped E2E resolution",
			server: &webtty.ServerInfo{
				Target:    target,
				HostKeyID: &hostKeyID,
			},
			wantTarget:      target,
			wantHostKeyID:   hostKeyID,
			wantE2ERequired: true,
		},
		{
			name:       "nil inventory keeps requested target",
			wantTarget: target,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := webTTYClientSecurityScopeFromServerInfo(target, tt.server)
			if scope.Target != tt.wantTarget {
				t.Fatalf("target = %q, want %q", scope.Target, tt.wantTarget)
			}
			if scope.HostKeyID != tt.wantHostKeyID {
				t.Fatalf("host key id = %q, want %q", scope.HostKeyID, tt.wantHostKeyID)
			}
			if scope.E2ERequired != tt.wantE2ERequired {
				t.Fatalf("E2ERequired = %t, want %t", scope.E2ERequired, tt.wantE2ERequired)
			}
			if scope.ClientProofRequired != tt.wantClientProof {
				t.Fatalf("ClientProofRequired = %t, want %t", scope.ClientProofRequired, tt.wantClientProof)
			}
		})
	}
}

func TestWebTTYClientRstreamTargetHelpers(t *testing.T) {
	if !webttyClientUsesRstream(" RSTRM://shell ") {
		t.Fatalf("expected rstrm URL to use rstream")
	}
	if webttyClientUsesRstream("wss://example.com") {
		t.Fatalf("unexpected rstream mode for websocket URL")
	}
	target, err := extractWebTTYTunnelTarget("shell:443")
	if err != nil || target != "shell" {
		t.Fatalf("extractWebTTYTunnelTarget(host:port) = %q, %v", target, err)
	}
	target, err = extractWebTTYTunnelTarget("shell")
	if err != nil || target != "shell" {
		t.Fatalf("extractWebTTYTunnelTarget(host) = %q, %v", target, err)
	}
	if _, err := extractWebTTYTunnelTarget(":443"); err == nil || !strings.Contains(err.Error(), "missing tunnel") {
		t.Fatalf("expected missing tunnel error, got %v", err)
	}
	if _, err := extractWebTTYTunnelTarget("shell:bad:addr"); err == nil || !strings.Contains(err.Error(), "failed to extract") {
		t.Fatalf("expected malformed target error, got %v", err)
	}
}

func TestCommandExitErrorExitCodeClampsToShellRange(t *testing.T) {
	cases := []struct {
		code int
		want int
	}{
		{code: -1, want: 1},
		{code: 0, want: 1},
		{code: 42, want: 42},
		{code: 300, want: 255},
	}
	for _, tc := range cases {
		err := &commandExitError{code: tc.code}
		if got := err.ExitCode(); got != tc.want {
			t.Fatalf("ExitCode(%d) = %d, want %d", tc.code, got, tc.want)
		}
		if !strings.Contains(err.Error(), "remote command exited") {
			t.Fatalf("unexpected error string: %q", err.Error())
		}
	}
}

func TestWebTTYServerPayloadCryptoResolverLoadsIdentityFile(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	identityPath := filepath.Join(t.TempDir(), "identity.json")
	if err := cmd.Flags().Set("identity-file", identityPath); err != nil {
		t.Fatalf("failed to set --identity-file: %v", err)
	}
	cryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, nil)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	if cryptoConfig.Resolver == nil || cryptoConfig.EndpointIdentity == nil || cryptoConfig.HostKeyID == "" {
		t.Fatalf("expected resolver, endpoint identity, and host key id, got %#v", cryptoConfig)
	}
	if _, err := os.Stat(identityPath); err != nil {
		t.Fatalf("identity file was not created: %v", err)
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityPath)
	if err != nil {
		t.Fatalf("LoadWebTTYEndpointIdentityFile() error = %v", err)
	}
	if cryptoConfig.HostKeyID != webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID) {
		t.Fatalf("host key id = %q, want %q", cryptoConfig.HostKeyID, webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID))
	}
}

func TestWebTTYServerPayloadCryptoResolverAcceptsIdentityFileEnv(t *testing.T) {
	identityPath := filepath.Join(t.TempDir(), "identity.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	data, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	if err := os.WriteFile(identityPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv(webTTYIdentityFileEnv, identityPath)
	cmd := newTestWebTTYServerCommand()
	cryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, nil)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	if cryptoConfig.Resolver == nil || cryptoConfig.EndpointIdentity == nil {
		t.Fatalf("expected resolver from identity file env, got %#v", cryptoConfig)
	}
	if cryptoConfig.HostKeyID != webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID) {
		t.Fatalf("host key id = %q, want %q", cryptoConfig.HostKeyID, webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID))
	}
}

func TestWebTTYServerPayloadCryptoResolverAcceptsInlineIdentityEnv(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	data, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	t.Setenv(webTTYIdentityEnv, string(data))
	cmd := newTestWebTTYServerCommand()
	cryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, nil)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	if cryptoConfig.Resolver == nil || cryptoConfig.EndpointIdentity == nil {
		t.Fatalf("expected resolver from inline identity env, got %#v", cryptoConfig)
	}
	if cryptoConfig.HostKeyID != webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID) {
		t.Fatalf("host key id = %q, want %q", cryptoConfig.HostKeyID, webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID))
	}
}

func TestWebTTYServerPayloadCryptoResolverRejectsInlineIdentityEnvWithExplicitFile(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	data, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	t.Setenv(webTTYIdentityEnv, string(data))
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("identity-file", filepath.Join(t.TempDir(), "identity.json")); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	_, err = webTTYServerPayloadCryptoResolver(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), webTTYIdentityEnv) {
		t.Fatalf("expected inline identity conflict error, got %v", err)
	}
}

func TestWebTTYAuthorizedClientSigningKeysAcceptsFlagAndEnv(t *testing.T) {
	first, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(first) error = %v", err)
	}
	second, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(second) error = %v", err)
	}
	cmd := newTestWebTTYServerCommand()
	firstKey := webtty.EncodeE2EKeyMaterial(first.Signing.KeyID) + ":" + webtty.EncodeE2EKeyMaterial(first.Signing.PublicKey)
	if err := cmd.Flags().Set("authorized-client-key", firstKey); err != nil {
		t.Fatalf("set authorized-client-key: %v", err)
	}
	t.Setenv(webTTYAuthorizedClientKeysEnv, webtty.EncodeE2EKeyMaterial(second.Signing.PublicKey))
	keys, err := webTTYAuthorizedClientSigningKeys(cmd)
	if err != nil {
		t.Fatalf("webTTYAuthorizedClientSigningKeys() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("authorized client key count = %d, want 2", len(keys))
	}
	if !bytes.Equal(keys[string(first.Signing.KeyID)], first.Signing.PublicKey) {
		t.Fatalf("first authorized client key was not loaded")
	}
	if !bytes.Equal(keys[string(second.Signing.KeyID)], second.Signing.PublicKey) {
		t.Fatalf("second authorized client key was not loaded")
	}
}

func TestWebTTYAuthorizedClientSigningKeysRejectsMismatchedKeyID(t *testing.T) {
	first, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(first) error = %v", err)
	}
	second, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(second) error = %v", err)
	}
	cmd := newTestWebTTYServerCommand()
	raw := webtty.EncodeE2EKeyMaterial(first.Signing.KeyID) + ":" + webtty.EncodeE2EKeyMaterial(second.Signing.PublicKey)
	if err := cmd.Flags().Set("authorized-client-key", raw); err != nil {
		t.Fatalf("set authorized-client-key: %v", err)
	}
	_, err = webTTYAuthorizedClientSigningKeys(cmd)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected key id mismatch error, got %v", err)
	}
}

func TestWebTTYExplicitAuthorizedClientSourceConfigured(t *testing.T) {
	tests := []struct {
		name      string
		configure func(t *testing.T, cmd *cobra.Command)
		want      bool
	}{
		{
			name: "none",
		},
		{
			name: "authorized client key flag",
			configure: func(t *testing.T, cmd *cobra.Command) {
				if err := cmd.Flags().Set("authorized-client-key", "key"); err != nil {
					t.Fatalf("set authorized-client-key: %v", err)
				}
			},
			want: true,
		},
		{
			name: "empty authorized client key flag",
			configure: func(t *testing.T, cmd *cobra.Command) {
				if err := cmd.Flags().Set("authorized-client-key", " "); err != nil {
					t.Fatalf("set authorized-client-key: %v", err)
				}
			},
		},
		{
			name: "authorized clients environment",
			configure: func(t *testing.T, cmd *cobra.Command) {
				t.Setenv(webTTYAuthorizedClientKeysEnv, "key")
			},
			want: true,
		},
		{
			name: "empty authorized clients environment",
			configure: func(t *testing.T, cmd *cobra.Command) {
				t.Setenv(webTTYAuthorizedClientKeysEnv, " ")
			},
		},
		{
			name: "authorized clients file flag",
			configure: func(t *testing.T, cmd *cobra.Command) {
				if err := cmd.Flags().Set("authorized-clients-file", "/tmp/authorized-clients.json"); err != nil {
					t.Fatalf("set authorized-clients-file: %v", err)
				}
			},
			want: true,
		},
		{
			name: "empty authorized clients file flag",
			configure: func(t *testing.T, cmd *cobra.Command) {
				if err := cmd.Flags().Set("authorized-clients-file", " "); err != nil {
					t.Fatalf("set authorized-clients-file: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(webTTYAuthorizedClientKeysEnv, "")
			cmd := newTestWebTTYServerCommand()
			if tt.configure != nil {
				tt.configure(t, cmd)
			}
			if got := webTTYExplicitAuthorizedClientSourceConfigured(cmd); got != tt.want {
				t.Fatalf("webTTYExplicitAuthorizedClientSourceConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebTTYAuthorizedClientSigningKeyResolverIsDisabledForWorkspaceManaged(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	enrollment := &webTTYServerEnrollmentFile{EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged}
	resolver, err := webTTYAuthorizedClientSigningKeyResolver(cmd, enrollment, true)
	if err != nil {
		t.Fatalf("webTTYAuthorizedClientSigningKeyResolver() error = %v", err)
	}
	if resolver != nil {
		t.Fatalf("workspace-managed WebTTY server must not use explicit authorized client resolver")
	}
}

func TestWebTTYE2EIdentityFilePathExpandsHomePathSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	want := filepath.Join(home, "webtty", "server.identity.json")
	t.Run("flag", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("identity-file", "~/webtty/server.identity.json"); err != nil {
			t.Fatalf("set identity-file: %v", err)
		}
		got, _, _, err := webTTYE2EIdentityFilePath(cmd, nil)
		if err != nil {
			t.Fatalf("webTTYE2EIdentityFilePath() error = %v", err)
		}
		if got != want {
			t.Fatalf("identity path = %q, want %q", got, want)
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv(webTTYIdentityFileEnv, "~/webtty/server.identity.json")
		cmd := newTestWebTTYServerCommand()
		got, _, _, err := webTTYE2EIdentityFilePath(cmd, nil)
		if err != nil {
			t.Fatalf("webTTYE2EIdentityFilePath() error = %v", err)
		}
		if got != want {
			t.Fatalf("identity path = %q, want %q", got, want)
		}
	})
	t.Run("enrollment", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		got, _, _, err := webTTYE2EIdentityFilePath(cmd, &webTTYServerEnrollmentFile{IdentityFile: "~/webtty/server.identity.json"})
		if err != nil {
			t.Fatalf("webTTYE2EIdentityFilePath() error = %v", err)
		}
		if got != want {
			t.Fatalf("identity path = %q, want %q", got, want)
		}
	})
}

func TestWebTTYServerPayloadCryptoResolverCreatesDefaultIdentityWhenRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("e2e", "true"); err != nil {
		t.Fatalf("failed to set --e2e: %v", err)
	}
	cryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, nil)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	if cryptoConfig.Resolver == nil || cryptoConfig.EndpointIdentity == nil || cryptoConfig.HostKeyID == "" {
		t.Fatalf("expected resolver, endpoint identity, and host key id, got %#v", cryptoConfig)
	}
	path, err := webtty.DefaultE2EIdentityPath()
	if err != nil {
		t.Fatalf("DefaultE2EIdentityPath() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default identity file was not created: %v", err)
	}
}

func TestWebTTYServerPayloadCryptoResolverInfersRequiredFromRegisteredEnrollment(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	identityPath := filepath.Join(t.TempDir(), "identity.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	data, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	if err := os.WriteFile(identityPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	enrollment := &webTTYServerEnrollmentFile{
		IdentityFile:           identityPath,
		ServerPublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		ServerSigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		ServerSigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ServerFingerprint:      webTTYServerPublicKeyFingerprint(identity.Encryption.PublicKey),
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:       webTTYServerEncryptionPolicyExplicitKey,
	}
	cryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, enrollment)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	if cryptoConfig.Resolver == nil || cryptoConfig.EndpointIdentity == nil || cryptoConfig.HostKeyID == "" {
		t.Fatalf("expected resolver, endpoint identity, and host key id, got %#v", cryptoConfig)
	}
	if _, err := os.Stat(identityPath); err != nil {
		t.Fatalf("identity file was not created from registered enrollment: %v", err)
	}
}

func TestWebTTYServerPayloadCryptoResolverKeepsDisabledEnrollmentPlain(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	enrollment := &webTTYServerEnrollmentFile{
		IdentityFile:     filepath.Join(t.TempDir(), "identity.json"),
		EncryptionPolicy: webTTYServerEncryptionPolicyDisabled,
	}
	cryptoConfig, err := webTTYServerPayloadCryptoResolver(cmd, enrollment)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	if cryptoConfig.Resolver != nil || cryptoConfig.EndpointIdentity != nil || cryptoConfig.HostKeyID != "" {
		t.Fatalf("disabled enrollment should not enable E2E, got %#v", cryptoConfig)
	}
}

func TestWebTTYClientPayloadCryptoRequiresKnownServerKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("e2e", "true"); err != nil {
		t.Fatalf("failed to set --e2e: %v", err)
	}
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err == nil || !strings.Contains(err.Error(), "requires --known-server-key") {
		t.Fatalf("expected missing known server key error, got crypto=%#v err=%v", crypto, err)
	}
}

func TestWebTTYClientPayloadCryptoKeepsPlainWhenNoE2ESignal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cmd := newTestWebTTYClientCommand()
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto != nil {
		t.Fatalf("expected no payload crypto without an E2E signal, got %#v", crypto)
	}
}

func TestWebTTYClientPayloadCryptoDoesNotEnableE2EFromUnscopedDefaultStore(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:      "prod-shell",
			KeyID:     webtty.EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: webtty.EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto != nil {
		t.Fatalf("expected no payload crypto without scoped E2E signal, got %#v", crypto)
	}
}

func TestWebTTYClientPayloadCryptoAcceptsServerKeyFlag(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	serverKey := webtty.EncodeE2EKeyMaterial(identity.KeyID) + ":" + webtty.EncodeE2EKeyMaterial(identity.PublicKey)
	if err := cmd.Flags().Set("known-server-key", serverKey); err != nil {
		t.Fatalf("failed to set --known-server-key: %v", err)
	}
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto == nil || crypto.SessionKeyGrant == nil || len(crypto.SessionKeyGrant.KeyEnvelopes) != 1 {
		t.Fatalf("expected session key grant with one key envelope, got %#v", crypto)
	}
	if len(crypto.SessionKeyGrant.KeyContext) == 0 {
		t.Fatalf("expected implicit key context for explicit server key")
	}
}

func TestWebTTYClientEndpointIdentityForNameOrExplicitSourcesDoesNotCreateDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cmd := newTestWebTTYClientCommand()
	identity, err := webTTYClientEndpointIdentityForNameOrExplicitSources(cmd, "")
	if err != nil {
		t.Fatalf("webTTYClientEndpointIdentityForNameOrExplicitSources() error = %v", err)
	}
	if identity != nil {
		t.Fatalf("expected no implicit default client identity, got %#v", identity)
	}
	defaultPath, err := webtty.DefaultE2EIdentityPath()
	if err != nil {
		t.Fatalf("DefaultE2EIdentityPath() error = %v", err)
	}
	if _, err := os.Stat(defaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default identity path should not be created implicitly, stat error = %v", err)
	}
}

func TestWebTTYClientCryptoRequiresAssociatedIdentityForClientProof(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	scope := webTTYClientSecurityScope{
		Target:              "prod-shell",
		HostKeyID:           webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		E2ERequired:         true,
		ClientProofRequired: true,
	}
	cryptoConfig, err := webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, scope)
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntimeAndScope() error = %v", err)
	}
	if cryptoConfig.ExpectedServerIdentity == nil {
		t.Fatalf("expected server endpoint identity from known server")
	}
	if cryptoConfig.EndpointIdentity != nil || cryptoConfig.ClientIdentityName != "" {
		t.Fatalf("expected no implicit client endpoint identity, got %#v", cryptoConfig)
	}
	err = webTTYMissingClientEndpointIdentityError(scope, cryptoConfig)
	for _, want := range []string{
		"known-server add prod-shell",
		"--client-identity <identity>",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected actionable known-server add error containing %q, got %v", want, err)
		}
	}
}

func TestWebTTYClientCryptoRequiresKnownServerWhenScopeRequiresE2E(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(webTTYKnownServerKeyEnv, "")
	t.Setenv(webTTYKnownServersFileEnv, "")
	cmd := newTestWebTTYClientCommand()
	_, err := webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		Target:              "prod-shell",
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err == nil || !strings.Contains(err.Error(), "known-server add prod-shell") {
		t.Fatalf("expected known-server add guidance, got %v", err)
	}
}

func TestWebTTYClientCryptoRejectsEncryptionOnlyKeyWhenClientProofIsRequired(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("known-server-key", webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey)); err != nil {
		t.Fatalf("failed to set --known-server-key: %v", err)
	}
	_, err = webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		Target:              "prod-shell",
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err == nil || !strings.Contains(err.Error(), "requires authenticated E2E") {
		t.Fatalf("expected authenticated E2E guidance, got %v", err)
	}
}

func TestWebTTYClientCryptoAcceptsEndpointServerIdentityFlag(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	endpoint := webtty.KnownServerEndpointIdentityString(identity.Public())
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("known-server-key", endpoint); err != nil {
		t.Fatalf("failed to set --known-server-key: %v", err)
	}
	cryptoConfig, err := webTTYClientCryptoWithRuntime(t.Context(), cmd, nil)
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntime() error = %v", err)
	}
	if cryptoConfig.PayloadCrypto == nil || cryptoConfig.PayloadCrypto.SessionKeyGrant == nil {
		t.Fatalf("expected E2E payload crypto, got %#v", cryptoConfig.PayloadCrypto)
	}
	if cryptoConfig.ExpectedServerIdentity == nil {
		t.Fatalf("expected server endpoint identity")
	}
	if !bytes.Equal(cryptoConfig.ExpectedServerIdentity.SigningKeyID, identity.Signing.KeyID) {
		t.Fatalf("expected signing key id to be propagated")
	}
}

func TestWebTTYClientEndpointIdentityLoadsInlineEnvironment(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	data, err := webtty.EncodeWebTTYEndpointIdentityJSON(*identity)
	if err != nil {
		t.Fatalf("EncodeWebTTYEndpointIdentityJSON() error = %v", err)
	}
	t.Setenv(webTTYIdentityEnv, string(data))
	cmd := newTestWebTTYClientCommand()
	loaded, err := webTTYClientEndpointIdentity(cmd)
	if err != nil {
		t.Fatalf("webTTYClientEndpointIdentity() error = %v", err)
	}
	if !bytes.Equal(loaded.Signing.KeyID, identity.Signing.KeyID) {
		t.Fatalf("loaded signing key id mismatch")
	}
}

func TestWebTTYClientPayloadCryptoAcceptsServerKeyEnv(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	serverKey := webtty.EncodeE2EKeyMaterial(identity.KeyID) + ":" + webtty.EncodeE2EKeyMaterial(identity.PublicKey)
	t.Setenv(webTTYKnownServerKeyEnv, serverKey)
	cmd := newTestWebTTYClientCommand()
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto == nil || crypto.SessionKeyGrant == nil || len(crypto.SessionKeyGrant.KeyEnvelopes) != 1 {
		t.Fatalf("expected env known server session key grant, got %#v", crypto)
	}
}

func TestWebTTYClientPayloadCryptoAcceptsServerKeysFile(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:      "server",
			KeyID:     webtty.EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: webtty.EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "known_servers.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("known-servers-file", path); err != nil {
		t.Fatalf("failed to set --known-servers-file: %v", err)
	}
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto == nil || crypto.SessionKeyGrant == nil || len(crypto.SessionKeyGrant.KeyEnvelopes) != 1 {
		t.Fatalf("expected session key grant with one key envelope, got %#v", crypto)
	}
}

func TestWebTTYClientPayloadCryptoAcceptsServerKeysFileEnv(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:      "server",
			KeyID:     webtty.EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: webtty.EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "known_servers.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv(webTTYKnownServersFileEnv, path)
	cmd := newTestWebTTYClientCommand()
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto == nil || crypto.SessionKeyGrant == nil || len(crypto.SessionKeyGrant.KeyEnvelopes) != 1 {
		t.Fatalf("expected env known servers file session key grant, got %#v", crypto)
	}
}

func TestWebTTYE2EServerKeysFilePathExpandsHomePathSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	want := filepath.Join(home, "webtty", "known_servers.json")
	t.Run("flag", func(t *testing.T) {
		cmd := newTestWebTTYClientCommand()
		if err := cmd.Flags().Set("known-servers-file", "~/webtty/known_servers.json"); err != nil {
			t.Fatalf("set known-servers-file: %v", err)
		}
		got, ok, err := webTTYE2EServerKeysFilePath(cmd)
		if err != nil {
			t.Fatalf("webTTYE2EServerKeysFilePath() error = %v", err)
		}
		if !ok || got != want {
			t.Fatalf("known servers path = %q %v, want %q true", got, ok, want)
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv(webTTYKnownServersFileEnv, "~/webtty/known_servers.json")
		cmd := newTestWebTTYClientCommand()
		got, ok, err := webTTYE2EServerKeysFilePath(cmd)
		if err != nil {
			t.Fatalf("webTTYE2EServerKeysFilePath() error = %v", err)
		}
		if !ok || got != want {
			t.Fatalf("known servers path = %q %v, want %q true", got, ok, want)
		}
	})
}

func TestWebTTYClientPayloadCryptoLoadsDefaultKnownServerKeysFile(t *testing.T) {
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:      "server",
			KeyID:     webtty.EncodeE2EKeyMaterial(identity.KeyID),
			PublicKey: webtty.EncodeE2EKeyMaterial(identity.PublicKey),
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("e2e", "true"); err != nil {
		t.Fatalf("set e2e: %v", err)
	}
	crypto, err := webTTYClientPayloadCrypto(cmd)
	if err != nil {
		t.Fatalf("webTTYClientPayloadCrypto() error = %v", err)
	}
	if crypto == nil || crypto.SessionKeyGrant == nil || len(crypto.SessionKeyGrant.KeyEnvelopes) != 1 {
		t.Fatalf("expected default known server session key grant, got %#v", crypto)
	}
}

func TestWebTTYClientCryptoLoadsScopedKnownServerIdentity(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
			ClientIdentity:   "operator-laptop",
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := writeTestWebTTYEndpointIdentity(t, "operator-laptop"); err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	cryptoConfig, err := webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		Target:              "server-1",
		HostKeyID:           webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntimeAndScope() error = %v", err)
	}
	if cryptoConfig.PayloadCrypto == nil || cryptoConfig.ExpectedServerIdentity == nil {
		t.Fatalf("expected scoped E2E crypto, got %#v", cryptoConfig)
	}
	if cryptoConfig.ClientIdentityName != "operator-laptop" {
		t.Fatalf("client identity name = %q, want operator-laptop", cryptoConfig.ClientIdentityName)
	}
}

func TestWebTTYClientCryptoUsesNamedKnownServer(t *testing.T) {
	targetIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	otherIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() other error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{
			{
				Name:             "other-shell",
				KeyID:            webtty.EncodeE2EKeyMaterial(otherIdentity.Encryption.KeyID),
				PublicKey:        webtty.EncodeE2EKeyMaterial(otherIdentity.Encryption.PublicKey),
				SigningKeyID:     webtty.EncodeE2EKeyMaterial(otherIdentity.Signing.KeyID),
				SigningPublicKey: webtty.EncodeE2EKeyMaterial(otherIdentity.Signing.PublicKey),
			},
			{
				Name:             "prod-shell",
				KeyID:            webtty.EncodeE2EKeyMaterial(targetIdentity.Encryption.KeyID),
				PublicKey:        webtty.EncodeE2EKeyMaterial(targetIdentity.Encryption.PublicKey),
				SigningKeyID:     webtty.EncodeE2EKeyMaterial(targetIdentity.Signing.KeyID),
				SigningPublicKey: webtty.EncodeE2EKeyMaterial(targetIdentity.Signing.PublicKey),
				ClientIdentity:   "operator-laptop",
			},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := writeTestWebTTYEndpointIdentity(t, "operator-laptop"); err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("known-server", "prod-shell"); err != nil {
		t.Fatalf("set known-server: %v", err)
	}
	cryptoConfig, err := webTTYClientCryptoWithRuntime(t.Context(), cmd, nil)
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntime() error = %v", err)
	}
	if cryptoConfig.PayloadCrypto == nil || cryptoConfig.ExpectedServerIdentity == nil || cryptoConfig.EndpointIdentity == nil {
		t.Fatalf("expected named known-server crypto and endpoint identity, got %#v", cryptoConfig)
	}
	if !bytes.Equal(cryptoConfig.ExpectedServerIdentity.SigningKeyID, targetIdentity.Signing.KeyID) {
		t.Fatalf("known-server selected wrong server identity")
	}
	if cryptoConfig.ClientIdentityName != "operator-laptop" {
		t.Fatalf("client identity name = %q, want operator-laptop", cryptoConfig.ClientIdentityName)
	}
}

func TestWebTTYClientCryptoDoesNotUseUnrelatedKnownServerForRequiredScope(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestKnownWebTTYServers(t, []webtty.KnownServerKeyEntry{{
		Name:             "other-shell",
		KeyID:            webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ClientIdentity:   "operator-laptop",
	}})
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		Target:              "prod-shell",
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err == nil || !strings.Contains(err.Error(), "known-server add prod-shell") {
		t.Fatalf("expected target-scoped known-server guidance, got %v", err)
	}
}

func TestWebTTYClientCryptoRejectsScopedKnownServerHostKeyMismatch(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	advertisedIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(advertised) error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestKnownWebTTYServers(t, []webtty.KnownServerKeyEntry{{
		Name:             "prod-shell",
		KeyID:            webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ClientIdentity:   "operator-laptop",
	}})
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		Target:              "prod-shell",
		HostKeyID:           webtty.EncodeE2EKeyMaterial(advertisedIdentity.Encryption.KeyID),
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match advertised host key") {
		t.Fatalf("expected host-key mismatch error, got %v", err)
	}
}

func TestWebTTYClientCryptoRejectsConflictingScopedClientIdentities(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	hostKeyID := webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestKnownWebTTYServers(t, []webtty.KnownServerKeyEntry{
		{
			Name:             "prod-shell-primary",
			KeyID:            hostKeyID,
			PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
			ClientIdentity:   "operator-laptop",
		},
		{
			Name:           "prod-shell-secondary",
			KeyID:          hostKeyID,
			PublicKey:      webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			ClientIdentity: "automation-runner",
		},
	})
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		HostKeyID:           hostKeyID,
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err == nil || !strings.Contains(err.Error(), "multiple WebTTY client identities are associated") {
		t.Fatalf("expected conflicting client identity error, got %v", err)
	}
}

func TestWebTTYClientCryptoReportsMissingAssociatedIdentityWithRecoveryCommand(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestKnownWebTTYServers(t, []webtty.KnownServerKeyEntry{{
		Name:             "prod-shell",
		KeyID:            webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
		SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
		SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
		ClientIdentity:   "operator-laptop",
	}})
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, nil, webTTYClientSecurityScope{
		Target:              "prod-shell",
		HostKeyID:           webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	for _, want := range []string{
		`client endpoint identity "operator-laptop"`,
		"rstream webtty identity create --name operator-laptop",
		"rstream webtty known-server set-identity prod-shell --identity <identity>",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected actionable missing identity error containing %q, got %v", want, err)
		}
	}
}

func TestWebTTYKnownServerNameForScopeRejectsAmbiguousHostKeyMatches(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	hostKeyID := webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestKnownWebTTYServers(t, []webtty.KnownServerKeyEntry{
		{
			Name:             "prod-shell-primary",
			KeyID:            hostKeyID,
			PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
			ClientIdentity:   "operator-laptop",
		},
		{
			Name:             "prod-shell-secondary",
			KeyID:            hostKeyID,
			PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
			ClientIdentity:   "operator-laptop",
		},
	})
	_, err = webTTYKnownServerNameForScope(webTTYClientSecurityScope{HostKeyID: hostKeyID}, nil)
	if err == nil || !strings.Contains(err.Error(), "multiple known WebTTY server entries match this target") {
		t.Fatalf("expected ambiguous known-server guidance, got %v", err)
	}
}

func TestWebTTYClientCryptoRejectsKnownServerWithExplicitKey(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("known-server", "prod-shell"); err != nil {
		t.Fatalf("set known-server: %v", err)
	}
	if err := cmd.Flags().Set("known-server-key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set known-server-key: %v", err)
	}
	_, err = webTTYClientCryptoWithRuntime(t.Context(), cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected known-server conflict error, got %v", err)
	}
}

func TestWebTTYClientRstreamControlPlanePolicy(t *testing.T) {
	serverID := "server-1"
	tests := []struct {
		name       string
		serverInfo *webtty.ServerInfo
		want       bool
	}{
		{
			name: "lightweight server resolved from inventory stays local",
			serverInfo: &webtty.ServerInfo{
				Target: "shell",
			},
		},
		{
			name: "registered disabled server stays local",
			serverInfo: &webtty.ServerInfo{
				Target:           "shell",
				ServerID:         &serverID,
				EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyDisabled),
			},
		},
		{
			name: "registered explicit key server stays local",
			serverInfo: &webtty.ServerInfo{
				Target:           "shell",
				ServerID:         &serverID,
				EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyExplicitKey),
			},
		},
		{
			name: "registered workspace managed server needs control plane",
			serverInfo: &webtty.ServerInfo{
				Target:           "shell",
				ServerID:         &serverID,
				EncryptionPolicy: rstream.StringPtr(webTTYServerEncryptionPolicyWorkspaceManaged),
			},
			want: true,
		},
		{
			name: "registered server without policy asks control plane",
			serverInfo: &webtty.ServerInfo{
				Target:   "shell",
				ServerID: &serverID,
			},
			want: true,
		},
		{
			name: "missing inventory can still try registered name lookup",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webTTYClientRstreamNeedsControlPlane(tt.serverInfo); got != tt.want {
				t.Fatalf("webTTYClientRstreamNeedsControlPlane() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveWebTTYClientRstreamUsesEngineInventoryForExplicitKeyServer(t *testing.T) {
	serverID := "server-1"
	engineServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/tunnels" {
			http.Error(w, "unexpected engine request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{
			TunnelProperties: rstream.TunnelProperties{
				ID:       rstream.StringPtr("tunnel-1"),
				Name:     rstream.StringPtr(serverID),
				Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
				Type:     rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
				Labels: map[string]string{
					webtty.WebTTYServerIDLabelKey:         serverID,
					webtty.WebTTYServerNameLabelKey:       "prod-shell",
					webtty.WebTTYEncryptionPolicyLabelKey: webTTYServerEncryptionPolicyExplicitKey,
					webtty.WebTTYE2ELabelKey:              webtty.WebTTYE2ERequired,
					webtty.WebTTYClientProofLabelKey:      webtty.WebTTYClientProofRequired,
					webtty.WebTTYHostKeyIDLabelKey:        "host-key",
					webtty.WebTTYApplicationProtocolKey:   webtty.WebTTYApplicationProtocol,
					webtty.WebTTYCapabilitiesLabelKey:     webtty.WebTTYCapabilityExec,
					webtty.WebTTYExecutionModeLabelKey:    string(webtty.WebTTYExecutionModeSpawn),
				},
			},
			Status: "online",
		}})
	}))
	defer engineServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(engineServer.Certificate())
	engineAddress := strings.TrimPrefix(engineServer.URL, "https://")
	token := "token"
	engineClient := &rstream.Client{
		EngineURL:       &engineAddress,
		Token:           &token,
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "control plane should not be called for explicit-key runtime", http.StatusBadRequest)
	}))
	defer controlServer.Close()
	resolution, err := resolveWebTTYClientRstream(t.Context(), &resolvedRuntime{Resolved: config.Resolved{
		APIURL: controlServer.URL,
		Token:  token,
		Context: &config.Context{
			ProjectEndpoint: "project-endpoint",
		},
	}}, engineClient, "rstrm://prod-shell")
	if err != nil {
		t.Fatalf("resolveWebTTYClientRstream() error = %v", err)
	}
	if resolution.RuntimeE2E != nil {
		t.Fatalf("explicit-key server should not require control plane context")
	}
	if resolution.URL != "rstrm://"+serverID {
		t.Fatalf("resolved URL = %q, want rstrm://%s", resolution.URL, serverID)
	}
	if !resolution.Scope.E2ERequired || !resolution.Scope.ClientProofRequired {
		t.Fatalf("expected E2E and client-proof requirements from inventory labels, got %#v", resolution.Scope)
	}
	if resolution.Scope.Target != "prod-shell" || resolution.Scope.HostKeyID != "host-key" {
		t.Fatalf("unexpected security scope: %#v", resolution.Scope)
	}
}

func TestResolveWebTTYClientRstreamUsesEngineInventoryForDisabledRegisteredServer(t *testing.T) {
	serverID := "server-plain"
	engineServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/tunnels" {
			http.Error(w, "unexpected engine request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{
			TunnelProperties: rstream.TunnelProperties{
				ID:       rstream.StringPtr("tunnel-plain"),
				Name:     rstream.StringPtr(serverID),
				Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
				Type:     rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
				Labels: map[string]string{
					webtty.WebTTYServerIDLabelKey:         serverID,
					webtty.WebTTYServerNameLabelKey:       "plain-shell",
					webtty.WebTTYEncryptionPolicyLabelKey: webTTYServerEncryptionPolicyDisabled,
					webtty.WebTTYE2ELabelKey:              webtty.WebTTYE2EDisabled,
					webtty.WebTTYClientProofLabelKey:      webtty.WebTTYClientProofNone,
					webtty.WebTTYApplicationProtocolKey:   webtty.WebTTYApplicationProtocol,
					webtty.WebTTYCapabilitiesLabelKey:     webtty.WebTTYCapabilityExec,
					webtty.WebTTYExecutionModeLabelKey:    string(webtty.WebTTYExecutionModeSpawn),
				},
			},
			Status: "online",
		}})
	}))
	defer engineServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(engineServer.Certificate())
	engineAddress := strings.TrimPrefix(engineServer.URL, "https://")
	token := "token"
	engineClient := &rstream.Client{
		EngineURL:       &engineAddress,
		Token:           &token,
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "control plane should not be called for disabled registered runtime", http.StatusBadRequest)
	}))
	defer controlServer.Close()
	resolution, err := resolveWebTTYClientRstream(t.Context(), &resolvedRuntime{Resolved: config.Resolved{
		APIURL: controlServer.URL,
		Token:  token,
		Context: &config.Context{
			ProjectEndpoint: "project-endpoint",
		},
	}}, engineClient, "rstrm://plain-shell")
	if err != nil {
		t.Fatalf("resolveWebTTYClientRstream() error = %v", err)
	}
	if resolution.RuntimeE2E != nil {
		t.Fatalf("disabled registered server should not require control plane context")
	}
	if resolution.URL != "rstrm://"+serverID {
		t.Fatalf("resolved URL = %q, want rstrm://%s", resolution.URL, serverID)
	}
	if resolution.Scope.E2ERequired || resolution.Scope.ClientProofRequired {
		t.Fatalf("plain registered server must not inherit E2E requirements, got %#v", resolution.Scope)
	}
	if resolution.Scope.Target != "plain-shell" {
		t.Fatalf("unexpected security scope target: %#v", resolution.Scope)
	}
}

func TestResolveWebTTYClientRstreamUsesEngineInventoryProjectForWorkspaceManagedServer(t *testing.T) {
	serverID := "server-workspace"
	engineServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/tunnels" {
			http.Error(w, "unexpected engine request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{
			TunnelProperties: rstream.TunnelProperties{
				ID:       rstream.StringPtr("tunnel-workspace"),
				Name:     rstream.StringPtr(serverID),
				Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
				Type:     rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
				Labels: map[string]string{
					webtty.WebTTYServerIDLabelKey:         serverID,
					webtty.WebTTYServerNameLabelKey:       "workspace-shell",
					webtty.WebTTYEncryptionPolicyLabelKey: webTTYServerEncryptionPolicyWorkspaceManaged,
					webtty.WebTTYE2ELabelKey:              webtty.WebTTYE2ERequired,
					webtty.WebTTYClientProofLabelKey:      webtty.WebTTYClientProofRequired,
					webtty.WebTTYHostKeyIDLabelKey:        "workspace-host-key",
					webtty.WebTTYApplicationProtocolKey:   webtty.WebTTYApplicationProtocol,
					webtty.WebTTYCapabilitiesLabelKey:     webtty.WebTTYCapabilityExec,
					webtty.WebTTYExecutionModeLabelKey:    string(webtty.WebTTYExecutionModeSpawn),
				},
			},
			WorkspaceID: "workspace-1",
			ProjectID:   "project-1",
			Status:      "online",
		}})
	}))
	defer engineServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(engineServer.Certificate())
	engineAddress := strings.TrimPrefix(engineServer.URL, "https://")
	token := "token"
	engineClient := &rstream.Client{
		EngineURL:       &engineAddress,
		Token:           &token,
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "control plane should not be called to resolve project scope already present in engine inventory", http.StatusBadRequest)
	}))
	defer controlServer.Close()
	resolution, err := resolveWebTTYClientRstream(t.Context(), &resolvedRuntime{Resolved: config.Resolved{
		APIURL: controlServer.URL,
		Token:  token,
		Context: &config.Context{
			ProjectEndpoint: "project-endpoint",
		},
	}}, engineClient, "rstrm://workspace-shell")
	if err != nil {
		t.Fatalf("resolveWebTTYClientRstream() error = %v", err)
	}
	if resolution.RuntimeE2E == nil {
		t.Fatal("workspace-managed server should create runtime E2E context")
	}
	if resolution.RuntimeE2E.project.ID != "project-1" || resolution.RuntimeE2E.project.WorkspaceID != "workspace-1" || resolution.RuntimeE2E.serverID != serverID {
		t.Fatalf("unexpected runtime E2E context: %#v", resolution.RuntimeE2E)
	}
	if resolution.URL != "rstrm://"+serverID {
		t.Fatalf("resolved URL = %q, want rstrm://%s", resolution.URL, serverID)
	}
	if !resolution.Scope.E2ERequired || !resolution.Scope.ClientProofRequired || resolution.Scope.HostKeyID != "workspace-host-key" {
		t.Fatalf("unexpected workspace-managed security scope: %#v", resolution.Scope)
	}
}

func TestResolveWebTTYClientPublishedUsesEngineInventoryHostForWorkspaceManagedServer(t *testing.T) {
	serverID := "server-workspace"
	hostname := "shell.example.com"
	port := uint32(8443)
	engineServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/tunnels" {
			http.Error(w, "unexpected engine request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{
			TunnelProperties: rstream.TunnelProperties{
				ID:       rstream.StringPtr("tunnel-workspace"),
				Name:     rstream.StringPtr(serverID),
				Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
				Type:     rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
				Hostname: &hostname,
				Port:     &port,
				Labels: map[string]string{
					webtty.WebTTYServerIDLabelKey:         serverID,
					webtty.WebTTYServerNameLabelKey:       "workspace-shell",
					webtty.WebTTYEncryptionPolicyLabelKey: webTTYServerEncryptionPolicyWorkspaceManaged,
					webtty.WebTTYE2ELabelKey:              webtty.WebTTYE2ERequired,
					webtty.WebTTYClientProofLabelKey:      webtty.WebTTYClientProofRequired,
					webtty.WebTTYHostKeyIDLabelKey:        "workspace-host-key",
					webtty.WebTTYApplicationProtocolKey:   webtty.WebTTYApplicationProtocol,
				},
			},
			WorkspaceID: "workspace-1",
			ProjectID:   "project-1",
			Status:      "online",
		}})
	}))
	defer engineServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(engineServer.Certificate())
	engineAddress := strings.TrimPrefix(engineServer.URL, "https://")
	token := "token"
	engineClient := &rstream.Client{
		EngineURL:       &engineAddress,
		Token:           &token,
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "control plane should not be called to resolve project scope already present in engine inventory", http.StatusBadRequest)
	}))
	defer controlServer.Close()
	resolution, err := resolveWebTTYClientPublished(t.Context(), &resolvedRuntime{Resolved: config.Resolved{
		APIURL: controlServer.URL,
		Token:  token,
		Context: &config.Context{
			ProjectEndpoint: "project-endpoint",
		},
	}}, engineClient, "https://shell.example.com:8443/webtty/exec")
	if err != nil {
		t.Fatalf("resolveWebTTYClientPublished() error = %v", err)
	}
	if resolution == nil || resolution.RuntimeE2E == nil {
		t.Fatal("workspace-managed published server should create runtime E2E context")
	}
	if resolution.RuntimeE2E.project.ID != "project-1" || resolution.RuntimeE2E.project.WorkspaceID != "workspace-1" || resolution.RuntimeE2E.serverID != serverID {
		t.Fatalf("unexpected runtime E2E context: %#v", resolution.RuntimeE2E)
	}
	if !resolution.Scope.E2ERequired || !resolution.Scope.ClientProofRequired || resolution.Scope.HostKeyID != "workspace-host-key" {
		t.Fatalf("unexpected workspace-managed security scope: %#v", resolution.Scope)
	}
}

func TestWebTTYRuntimeHostMatchesTarget(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		target string
		want   bool
	}{
		{name: "exact host and port", host: "shell.example.com:8443", target: "shell.example.com:8443", want: true},
		{name: "case insensitive", host: "Shell.Example.com:8443", target: "shell.example.com:8443", want: true},
		{name: "default TLS port", host: "shell.example.com", target: "shell.example.com:443", want: true},
		{name: "trailing dot", host: "shell.example.com.", target: "shell.example.com", want: true},
		{name: "different port", host: "shell.example.com:8443", target: "shell.example.com:9443"},
		{name: "different host", host: "shell.example.com", target: "other.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webTTYRuntimeHostMatchesTarget(tt.host, tt.target); got != tt.want {
				t.Fatalf("webTTYRuntimeHostMatchesTarget(%q, %q) = %v, want %v", tt.host, tt.target, got, tt.want)
			}
		})
	}
}

func TestResolveWebTTYClientRstreamFallsBackToControlPlaneProjectWhenInventoryScopeMissing(t *testing.T) {
	serverID := "server-workspace"
	engineServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/tunnels" {
			http.Error(w, "unexpected engine request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{
			TunnelProperties: rstream.TunnelProperties{
				ID:       rstream.StringPtr("tunnel-workspace"),
				Name:     rstream.StringPtr(serverID),
				Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
				Type:     rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
				Labels: map[string]string{
					webtty.WebTTYServerIDLabelKey:         serverID,
					webtty.WebTTYServerNameLabelKey:       "workspace-shell",
					webtty.WebTTYEncryptionPolicyLabelKey: webTTYServerEncryptionPolicyWorkspaceManaged,
					webtty.WebTTYE2ELabelKey:              webtty.WebTTYE2ERequired,
					webtty.WebTTYClientProofLabelKey:      webtty.WebTTYClientProofRequired,
					webtty.WebTTYApplicationProtocolKey:   webtty.WebTTYApplicationProtocol,
				},
			},
			Status: "online",
		}})
	}))
	defer engineServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(engineServer.Certificate())
	engineAddress := strings.TrimPrefix(engineServer.URL, "https://")
	token := "token"
	engineClient := &rstream.Client{
		EngineURL:       &engineAddress,
		Token:           &token,
		TLSClientConfig: &tls.Config{RootCAs: roots},
	}
	resolveProjectCalls := 0
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/projects/tunnels/resolve/project-endpoint" {
			http.Error(w, "unexpected control plane request", http.StatusBadRequest)
			return
		}
		resolveProjectCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.Project{ID: "project-1", WorkspaceID: "workspace-1", Endpoint: "project-endpoint", Routing: "regional"})
	}))
	defer controlServer.Close()
	resolution, err := resolveWebTTYClientRstream(t.Context(), &resolvedRuntime{Resolved: config.Resolved{
		APIURL: controlServer.URL,
		Token:  token,
		Context: &config.Context{
			ProjectEndpoint: "project-endpoint",
		},
	}}, engineClient, "rstrm://workspace-shell")
	if err != nil {
		t.Fatalf("resolveWebTTYClientRstream() error = %v", err)
	}
	if resolveProjectCalls != 1 {
		t.Fatalf("control plane resolve calls = %d, want 1", resolveProjectCalls)
	}
	if resolution.RuntimeE2E == nil {
		t.Fatal("workspace-managed server should create runtime E2E context")
	}
	if resolution.RuntimeE2E.project.ID != "project-1" || resolution.RuntimeE2E.project.WorkspaceID != "workspace-1" || resolution.RuntimeE2E.serverID != serverID {
		t.Fatalf("unexpected runtime E2E context: %#v", resolution.RuntimeE2E)
	}
}

func TestWebTTYClientCryptoPreservesKnownServerClientIdentityWithRegisteredRuntime(t *testing.T) {
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             "prod-shell",
			KeyID:            webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey),
			ClientIdentity:   "operator-laptop",
		}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := writeTestWebTTYEndpointIdentity(t, "operator-laptop"); err != nil {
		t.Fatalf("writeTestWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(identity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey)
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               "server-1",
			WorkspaceID:            "workspace-1",
			ProjectID:              "project-1",
			EncryptionPolicy:       "explicit_key",
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
		})
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	cryptoConfig, err := webTTYClientCryptoWithRuntimeAndScope(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: "project-1", WorkspaceID: "workspace-1"},
		serverID:      "server-1",
	}, webTTYClientSecurityScope{
		Target:              "prod-shell",
		HostKeyID:           webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID),
		E2ERequired:         true,
		ClientProofRequired: true,
	})
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntimeAndScope() error = %v", err)
	}
	if cryptoConfig.PayloadCrypto == nil || cryptoConfig.ExpectedServerIdentity == nil {
		t.Fatalf("expected registered E2E crypto, got %#v", cryptoConfig)
	}
	if got := len(cryptoConfig.PayloadCrypto.SessionKeyGrant.KeyEnvelopes); got != 2 {
		t.Fatalf("session key grant envelopes = %d, want 2", got)
	}
	if cryptoConfig.ClientIdentityName != "operator-laptop" {
		t.Fatalf("client identity name = %q, want operator-laptop", cryptoConfig.ClientIdentityName)
	}
}

func TestWebTTYClientPayloadCryptoResolvesWorkspaceManagedServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	projectID := "project-1"
	serverID := "server-1"
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	var seen controlplane.ResolveWebTTYServerClientRequest
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(seen.DeviceProofs) != 1 || seen.DeviceProofs[0].DeviceFingerprint != device.Fingerprint {
			http.Error(w, "missing device proof", http.StatusBadRequest)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
		payload := workspaceDeviceLookupPayload(workspaceID, device.Fingerprint, seen.DeviceProofs[0].Challenge, seen.DeviceProofs[0].SignedAt)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.DeviceProofs[0].Signature) {
			http.Error(w, "invalid device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               serverID,
			WorkspaceID:            workspaceID,
			ProjectID:              projectID,
			EncryptionPolicy:       "workspace_managed",
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
			CurrentDevice:          testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle),
		})
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	cryptoConfig, err := webTTYClientCryptoWithRuntime(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntime() error = %v", err)
	}
	payloadCrypto := cryptoConfig.PayloadCrypto
	if payloadCrypto == nil || payloadCrypto.SessionKeyGrant == nil || len(payloadCrypto.SessionKeyGrant.KeyEnvelopes) != 2 {
		t.Fatalf("expected workspace-managed server and device key envelopes, got %#v", payloadCrypto)
	}
	if len(payloadCrypto.SessionKeyGrant.KeyContext) == 0 {
		t.Fatalf("expected typed workspace-managed key context")
	}
	if cryptoConfig.ExpectedServerIdentity == nil {
		t.Fatalf("expected authenticated workspace-managed server identity")
	}
	if cryptoConfig.EndpointIdentity == nil {
		t.Fatalf("expected trusted workspace device endpoint identity")
	}
	if len(cryptoConfig.ClientCredential) == 0 {
		t.Fatalf("expected trusted workspace device client credential")
	}
	enrollment := &webTTYServerEnrollmentFile{
		Version:                         webTTYServerEnrollmentVersion,
		ServerID:                        serverID,
		WorkspaceID:                     workspaceID,
		ProjectID:                       projectID,
		ServerPublicKey:                 webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey),
		ServerSigningKeyID:              webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.KeyID),
		ServerSigningPublicKey:          webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.PublicKey),
		ServerFingerprint:               webTTYServerPublicKeyFingerprint(serverIdentity.Encryption.PublicKey),
		ServerKeyAlgorithm:              webTTYServerKeyAlgorithmX25519,
		WorkspaceTrustKeysetID:          "keyset-1",
		WorkspaceTrustKeysetFingerprint: keysetBundle.Fingerprint,
		WorkspaceTrustPublicSigningKey:  keysetBundle.PublicSigningKey,
		EncryptionPolicy:                webTTYServerEncryptionPolicyWorkspaceManaged,
		EnrollmentStatus:                webTTYServerEnrollmentStatusOK,
	}
	verifiedKey, err := verifyWebTTYWorkspaceClientCredential(enrollment, webtty.ClientProofVerification{
		Proof: &pb.ClientProof{
			SigningKeyId:     cryptoConfig.EndpointIdentity.Signing.KeyID,
			SigningPublicKey: cryptoConfig.EndpointIdentity.Signing.PublicKey,
		},
		Credential: cryptoConfig.ClientCredential,
	})
	if err != nil {
		t.Fatalf("workspace-managed client credential was not accepted by server verifier: %v", err)
	}
	if !bytes.Equal(verifiedKey, cryptoConfig.EndpointIdentity.Signing.PublicKey) {
		t.Fatalf("verified client signing key mismatch")
	}
}

func TestWebTTYClientOpensWorkspaceManagedE2ERuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command runtime uses sh")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-runtime"
	projectID := "project-runtime"
	serverID := "server-runtime"
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-runtime"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	enrollment := &webTTYServerEnrollmentFile{
		Version:                         webTTYServerEnrollmentVersion,
		ServerID:                        serverID,
		WorkspaceID:                     workspaceID,
		ProjectID:                       projectID,
		ServerPublicKey:                 webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey),
		ServerSigningKeyID:              webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.KeyID),
		ServerSigningPublicKey:          webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.PublicKey),
		ServerFingerprint:               webTTYServerPublicKeyFingerprint(serverIdentity.Encryption.PublicKey),
		ServerKeyAlgorithm:              webTTYServerKeyAlgorithmX25519,
		WorkspaceTrustKeysetID:          "keyset-1",
		WorkspaceTrustKeysetFingerprint: keysetBundle.Fingerprint,
		WorkspaceTrustPublicSigningKey:  keysetBundle.PublicSigningKey,
		EncryptionPolicy:                webTTYServerEncryptionPolicyWorkspaceManaged,
		EnrollmentStatus:                webTTYServerEnrollmentStatusOK,
	}
	allowUnauthenticated := true
	requireSessionKeyGrant := true
	requireClientProof := true
	heartbeat := time.Duration(0)
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{
		AllowUnauthenticated:   &allowUnauthenticated,
		HeartbeatInterval:      &heartbeat,
		PayloadCryptoResolver:  webtty.NewE2EServerPayloadCryptoResolver(serverIdentity.Encryption),
		RequireSessionKeyGrant: &requireSessionKeyGrant,
		EndpointIdentity:       serverIdentity,
		RequireClientProof:     &requireClientProof,
		ClientProofVerifier:    webTTYWorkspaceClientProofVerifier(enrollment),
		WorkspaceID:            workspaceID,
		ProjectID:              projectID,
		ServerID:               serverID,
	})
	terminalServer := httptest.NewServer(handler)
	defer terminalServer.Close()
	defer handler.Shutdown(t.Context())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	var seen controlplane.ResolveWebTTYServerClientRequest
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-runtime/webtty/servers/server-runtime/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(seen.DeviceProofs) != 1 || seen.DeviceProofs[0].DeviceFingerprint != device.Fingerprint {
			http.Error(w, "missing device proof", http.StatusBadRequest)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
		payload := workspaceDeviceLookupPayload(workspaceID, device.Fingerprint, seen.DeviceProofs[0].Challenge, seen.DeviceProofs[0].SignedAt)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, seen.DeviceProofs[0].Signature) {
			http.Error(w, "invalid device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               serverID,
			WorkspaceID:            workspaceID,
			ProjectID:              projectID,
			EncryptionPolicy:       webTTYServerEncryptionPolicyWorkspaceManaged,
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
			ServerKeyAlgorithm:     rstream.StringPtr("x25519-hkdf-sha256-aes-256-gcm"),
			CurrentDevice:          testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle),
		})
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	cryptoConfig, err := webTTYClientCryptoWithRuntime(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntime() error = %v", err)
	}
	cfg := &webtty.ClientConfig{
		URL:                    "ws" + strings.TrimPrefix(terminalServer.URL, "http"),
		Transport:              webtty.WebTTYTransportWebSocket,
		Interactive:            false,
		AllocateTTY:            false,
		SendHeartbeat:          false,
		CmdArgs:                []string{"sh", "-lc", "echo cli-workspace-managed-ok"},
		PayloadCrypto:          cryptoConfig.PayloadCrypto,
		EndpointIdentity:       cryptoConfig.EndpointIdentity,
		ExpectedServerIdentity: cryptoConfig.ExpectedServerIdentity,
		ClientCredential:       cryptoConfig.ClientCredential,
	}
	session, err := webtty.OpenClientSession(t.Context(), webTTYSessionConfigFromClientConfig(cfg))
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectUITestSessionOutput(session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v, stderr=%q", exitCode, err, stderr)
	}
	if !strings.Contains(stdout, "cli-workspace-managed-ok") {
		t.Fatalf("stdout=%q, want cli-workspace-managed-ok", stdout)
	}
	rejected := *cfg
	rejected.ClientCredential = nil
	if _, err := webtty.OpenClientSession(t.Context(), webTTYSessionConfigFromClientConfig(&rejected)); err == nil || !strings.Contains(err.Error(), "client credential is required") {
		t.Fatalf("expected missing workspace credential rejection, got %v", err)
	}
}

func TestWebTTYServerCommandAcceptsWorkspaceManagedDeviceCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command runtime uses sh")
	}
	serverHome := t.TempDir()
	clientHome := t.TempDir()
	workspaceID := "workspace-command-runtime"
	projectID := "project-command-runtime"
	serverID := "server-command-runtime"
	serverIdentityPath := filepath.Join(serverHome, ".rstream", "webtty", "identities", serverID+".identity.json")
	serverEnrollmentPath := filepath.Join(serverHome, ".rstream", "webtty", "enrollments", serverID+".yaml")
	serverIdentity, err := webtty.LoadOrCreateWebTTYEndpointIdentityFile(serverIdentityPath)
	if err != nil {
		t.Fatalf("LoadOrCreateWebTTYEndpointIdentityFile() error = %v", err)
	}
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-command-runtime"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	setWorkspaceDeviceTestHome(t, clientHome)
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	enrollment := webTTYServerEnrollmentFile{
		Version:                         webTTYServerEnrollmentVersion,
		ServerID:                        serverID,
		ServerName:                      "command-runtime",
		WorkspaceID:                     workspaceID,
		ProjectID:                       projectID,
		IdentityFile:                    serverIdentityPath,
		ServerPublicKey:                 webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey),
		ServerSigningKeyID:              webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.KeyID),
		ServerSigningPublicKey:          webtty.EncodeE2EKeyMaterial(serverIdentity.Signing.PublicKey),
		ServerFingerprint:               webTTYServerPublicKeyFingerprint(serverIdentity.Encryption.PublicKey),
		ServerKeyAlgorithm:              webTTYServerKeyAlgorithmX25519,
		WorkspaceTrustKeysetID:          "keyset-1",
		WorkspaceTrustKeysetFingerprint: keysetBundle.Fingerprint,
		WorkspaceTrustPublicSigningKey:  keysetBundle.PublicSigningKey,
		EncryptionPolicy:                webTTYServerEncryptionPolicyWorkspaceManaged,
		EnrollmentStatus:                webTTYServerEnrollmentStatusOK,
		EnrolledAt:                      time.Now().UTC().Truncate(time.Second),
	}
	if err := writeWebTTYServerEnrollmentFile(serverEnrollmentPath, enrollment); err != nil {
		t.Fatalf("writeWebTTYServerEnrollmentFile() error = %v", err)
	}
	serverCmd := newTestWebTTYServerCommand()
	if err := serverCmd.Flags().Set("server-id", serverID); err != nil {
		t.Fatalf("set server-id flag: %v", err)
	}
	t.Setenv("HOME", serverHome)
	t.Setenv("USERPROFILE", serverHome)
	loadedEnrollment, _, err := readWebTTYServerEnrollmentFromFlags(serverCmd)
	if err != nil {
		t.Fatalf("readWebTTYServerEnrollmentFromFlags() error = %v", err)
	}
	payloadCryptoConfig, err := webTTYServerPayloadCryptoResolver(serverCmd, loadedEnrollment)
	if err != nil {
		t.Fatalf("webTTYServerPayloadCryptoResolver() error = %v", err)
	}
	clientProofVerifier := webTTYWorkspaceClientProofVerifier(loadedEnrollment)
	if clientProofVerifier == nil {
		t.Fatalf("workspace-managed enrollment did not configure a client proof verifier")
	}
	allowUnauthenticated := true
	requireSessionKeyGrant := true
	requireClientProof := true
	heartbeat := time.Duration(0)
	handler := webtty.NewWebTTYHandler(&webtty.ServerConfig{
		AllowUnauthenticated:   &allowUnauthenticated,
		HeartbeatInterval:      &heartbeat,
		PayloadCryptoResolver:  payloadCryptoConfig.Resolver,
		RequireSessionKeyGrant: &requireSessionKeyGrant,
		EndpointIdentity:       payloadCryptoConfig.EndpointIdentity,
		RequireClientProof:     &requireClientProof,
		ClientProofVerifier:    clientProofVerifier,
		WorkspaceID:            webTTYServerEnrollmentWorkspaceID(loadedEnrollment),
		ProjectID:              webTTYServerEnrollmentProjectID(loadedEnrollment),
		ServerID:               webTTYServerEnrollmentServerID(loadedEnrollment),
	})
	terminalServer := httptest.NewServer(handler)
	defer terminalServer.Close()
	defer handler.Shutdown(t.Context())
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-command-runtime/webtty/servers/server-command-runtime/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var request controlplane.ResolveWebTTYServerClientRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(request.DeviceProofs) != 1 || request.DeviceProofs[0].DeviceFingerprint != device.Fingerprint {
			http.Error(w, "missing trusted device proof", http.StatusForbidden)
			return
		}
		signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
		payload := workspaceDeviceLookupPayload(workspaceID, device.Fingerprint, request.DeviceProofs[0].Challenge, request.DeviceProofs[0].SignedAt)
		if !verifyWorkspaceDeviceSignature(t, signingKey, payload, request.DeviceProofs[0].Signature) {
			http.Error(w, "invalid trusted device proof", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               serverID,
			WorkspaceID:            workspaceID,
			ProjectID:              projectID,
			EncryptionPolicy:       webTTYServerEncryptionPolicyWorkspaceManaged,
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
			ServerKeyAlgorithm:     rstream.StringPtr("x25519-hkdf-sha256-aes-256-gcm"),
			CurrentDevice:          testWebTTYCurrentDeviceResolution(t, device, keysetPrivate, keysetBundle),
		})
	}))
	defer controlServer.Close()
	t.Setenv("HOME", clientHome)
	t.Setenv("USERPROFILE", clientHome)
	clientCmd := newTestWebTTYClientCommand()
	cryptoConfig, err := webTTYClientCryptoWithRuntime(t.Context(), clientCmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err != nil {
		t.Fatalf("webTTYClientCryptoWithRuntime() error = %v", err)
	}
	cfg := &webtty.ClientConfig{
		URL:                    "ws" + strings.TrimPrefix(terminalServer.URL, "http"),
		Transport:              webtty.WebTTYTransportWebSocket,
		Interactive:            false,
		AllocateTTY:            false,
		SendHeartbeat:          false,
		CmdArgs:                []string{"sh", "-lc", "echo cli-workspace-managed-command-ok"},
		PayloadCrypto:          cryptoConfig.PayloadCrypto,
		EndpointIdentity:       cryptoConfig.EndpointIdentity,
		ExpectedServerIdentity: cryptoConfig.ExpectedServerIdentity,
		ClientCredential:       cryptoConfig.ClientCredential,
	}
	session, err := webtty.OpenClientSession(t.Context(), webTTYSessionConfigFromClientConfig(cfg))
	if err != nil {
		t.Fatalf("OpenClientSession() error = %v", err)
	}
	stdout, stderr, exitCode, err := collectUITestSessionOutput(session)
	if err != nil || exitCode != 0 {
		t.Fatalf("Wait() = %d, %v, stderr=%q", exitCode, err, stderr)
	}
	if !strings.Contains(stdout, "cli-workspace-managed-command-ok") {
		t.Fatalf("stdout=%q, want cli-workspace-managed-command-ok", stdout)
	}
}

func TestWebTTYClientPayloadCryptoWorkspaceManagedRequiresResolvedTrustedDevice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	projectID := "project-1"
	serverID := "server-1"
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	requestSeen := false
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		requestSeen = true
		var seen controlplane.ResolveWebTTYServerClientRequest
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(seen.DeviceProofs) != 0 {
			http.Error(w, "unexpected device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               serverID,
			WorkspaceID:            workspaceID,
			ProjectID:              projectID,
			EncryptionPolicy:       webTTYServerEncryptionPolicyWorkspaceManaged,
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
		})
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntime(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace-managed WebTTY E2E requires this machine to be a trusted workspace device") {
		t.Fatalf("expected trusted workspace device error, got %v", err)
	}
	if !requestSeen {
		t.Fatalf("expected control-plane resolution request")
	}
}

func TestWebTTYClientPayloadCryptoWorkspaceManagedIgnoresForeignWorkspaceDevice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	projectID := "project-1"
	serverID := "server-1"
	material, err := generateWorkspaceDeviceMaterial("workspace-other", workspaceDeviceKindCLI, "Other workspace CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "other-workspace-device"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	requestSeen := false
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		requestSeen = true
		var seen controlplane.ResolveWebTTYServerClientRequest
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(seen.DeviceProofs) != 0 {
			http.Error(w, "foreign workspace device proof must not be sent", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               serverID,
			WorkspaceID:            workspaceID,
			ProjectID:              projectID,
			EncryptionPolicy:       webTTYServerEncryptionPolicyWorkspaceManaged,
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
		})
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntime(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace-managed WebTTY E2E requires this machine to be a trusted workspace device") {
		t.Fatalf("expected trusted workspace device error, got %v", err)
	}
	if !requestSeen {
		t.Fatalf("expected control-plane resolution request")
	}
}

func TestWebTTYClientPayloadCryptoWorkspaceManagedMapsForbiddenToTrustedDeviceHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	projectID := "project-1"
	serverID := "server-1"
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	_, err := webTTYClientCryptoWithRuntime(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace-managed WebTTY E2E requires this machine to be a trusted workspace device") {
		t.Fatalf("expected trusted workspace device hint, got %v", err)
	}
}

func TestWebTTYClientPayloadCryptoWorkspaceManagedRejectsUnexpectedResolvedDevice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspaceID := "workspace-1"
	projectID := "project-1"
	serverID := "server-1"
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	device := material.file
	device.DeviceKeyID = "device-1"
	device.Status = workspaceDeviceStatusActive
	device.CreatedAt = time.Now().UTC().Truncate(time.Second)
	device.UpdatedAt = device.CreatedAt
	otherMaterial, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindCLI, "Other CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial(other) error = %v", err)
	}
	otherDevice := otherMaterial.file
	otherDevice.DeviceKeyID = "other-device"
	otherDevice.Status = workspaceDeviceStatusActive
	otherDevice.CreatedAt = device.CreatedAt
	otherDevice.UpdatedAt = device.UpdatedAt
	keysetPrivate, _, keysetBundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, device, "keyset-1")
	device.DeviceEnvelope = &envelope
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, device, material.webttyIdentity)
	serverIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverEndpointIdentity := webtty.KnownServerEndpointIdentityString(serverIdentity.Public())
	serverPublicKey := webtty.EncodeE2EKeyMaterial(serverIdentity.Encryption.PublicKey)
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project-1/webtty/servers/server-1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var seen controlplane.ResolveWebTTYServerClientRequest
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(seen.DeviceProofs) != 1 || seen.DeviceProofs[0].DeviceFingerprint != device.Fingerprint {
			http.Error(w, "missing local device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ResolveWebTTYServerClientResponse{
			ServerID:               serverID,
			WorkspaceID:            workspaceID,
			ProjectID:              projectID,
			EncryptionPolicy:       webTTYServerEncryptionPolicyWorkspaceManaged,
			E2ERequired:            true,
			ServerPublicKey:        &serverPublicKey,
			ServerEndpointIdentity: &serverEndpointIdentity,
			CurrentDevice:          testWebTTYCurrentDeviceResolution(t, otherDevice, keysetPrivate, keysetBundle),
		})
	}))
	defer controlServer.Close()
	cmd := newTestWebTTYClientCommand()
	_, err = webTTYClientCryptoWithRuntime(t.Context(), cmd, &webTTYClientRuntimeE2EContext{
		controlClient: controlplane.NewClient(controlServer.URL, "token"),
		project:       controlplane.Project{ID: projectID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err == nil || !strings.Contains(err.Error(), "trusted workspace device other-device was not found locally") {
		t.Fatalf("expected mismatched trusted device error, got %v", err)
	}
}

func writeTestWebTTYEndpointIdentity(t *testing.T, name string) (*webtty.WebTTYEndpointIdentity, error) {
	t.Helper()
	path, err := defaultNamedWebTTYIdentityPath(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return webtty.LoadOrCreateWebTTYEndpointIdentityFile(path)
}

func writeTestKnownWebTTYServers(t *testing.T, entries []webtty.KnownServerKeyEntry) string {
	t.Helper()
	path, err := webtty.DefaultKnownServerKeysPath()
	if err != nil {
		t.Fatalf("DefaultKnownServerKeysPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := webtty.KnownServerKeysFile{
		Version:      webtty.E2EIdentityFileVersion,
		CryptoSuite:  webtty.E2EKeyFileCryptoSuite,
		KnownServers: entries,
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestWebTTYE2EServerKeysDetectConflictingKeyID(t *testing.T) {
	first, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	second, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() second error = %v", err)
	}
	cmd := newTestWebTTYClientCommand()
	serverKey := webtty.EncodeE2EKeyMaterial(first.KeyID) + ":" + webtty.EncodeE2EKeyMaterial(second.PublicKey)
	if err := cmd.Flags().Set("known-server-key", serverKey); err != nil {
		t.Fatalf("failed to set --known-server-key: %v", err)
	}
	if _, _, err := webTTYE2EServerKeysFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected conflicting known server key error, got %v", err)
	}
}

func TestWebTTYClientTLSConfig(t *testing.T) {
	if cfg, err := webTTYClientTLSConfig(newTestWebTTYClientCommand()); err != nil || cfg != nil {
		t.Fatalf("default webTTYClientTLSConfig() = %#v, %v", cfg, err)
	}
	serverNameCmd := newTestWebTTYClientCommand()
	if err := serverNameCmd.Flags().Set("tls-server-name", "terminal.example"); err != nil {
		t.Fatalf("failed to set --tls-server-name: %v", err)
	}
	cfg, err := webTTYClientTLSConfig(serverNameCmd)
	if err != nil {
		t.Fatalf("webTTYClientTLSConfig(server name) error = %v", err)
	}
	if cfg == nil || cfg.ServerName != "terminal.example" || cfg.InsecureSkipVerify {
		t.Fatalf("unexpected server name TLS config: %#v", cfg)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("TLS MinVersion = %d, want TLS 1.3", cfg.MinVersion)
	}
	insecureCmd := newTestWebTTYClientCommand()
	if err := insecureCmd.Flags().Set("tls-insecure-skip-verify", "true"); err != nil {
		t.Fatalf("failed to set --tls-insecure-skip-verify: %v", err)
	}
	cfg, err = webTTYClientTLSConfig(insecureCmd)
	if err != nil {
		t.Fatalf("webTTYClientTLSConfig(insecure) error = %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatalf("expected insecure TLS config, got %#v", cfg)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, testWebTTYCACertificatePEM(t), 0o600); err != nil {
		t.Fatalf("WriteFile(ca) error = %v", err)
	}
	caCmd := newTestWebTTYClientCommand()
	if err := caCmd.Flags().Set("tls-ca-file", caPath); err != nil {
		t.Fatalf("failed to set --tls-ca-file: %v", err)
	}
	cfg, err = webTTYClientTLSConfig(caCmd)
	if err != nil {
		t.Fatalf("webTTYClientTLSConfig(ca) error = %v", err)
	}
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatalf("expected RootCAs in TLS config, got %#v", cfg)
	}
	conflictCmd := newTestWebTTYClientCommand()
	if err := conflictCmd.Flags().Set("tls-ca-file", caPath); err != nil {
		t.Fatalf("failed to set conflict --tls-ca-file: %v", err)
	}
	if err := conflictCmd.Flags().Set("tls-insecure-skip-verify", "true"); err != nil {
		t.Fatalf("failed to set conflict --tls-insecure-skip-verify: %v", err)
	}
	if _, err := webTTYClientTLSConfig(conflictCmd); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected CA/insecure conflict, got %v", err)
	}
	invalidCmd := newTestWebTTYClientCommand()
	invalidPath := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(invalidPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	if err := invalidCmd.Flags().Set("tls-ca-file", invalidPath); err != nil {
		t.Fatalf("failed to set invalid --tls-ca-file: %v", err)
	}
	if _, err := webTTYClientTLSConfig(invalidCmd); err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("expected invalid CA file error, got %v", err)
	}
}

func TestWebTTYPlainServerTLSConfig(t *testing.T) {
	if cfg, err := webTTYPlainServerTLSConfig(newTestWebTTYServerCommand()); err != nil || cfg != nil {
		t.Fatalf("default webTTYPlainServerTLSConfig() = %#v, %v", cfg, err)
	}
	certPath, keyPath := testWebTTYServerCertificateFiles(t)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("tls-cert-file", certPath); err != nil {
		t.Fatalf("failed to set --tls-cert-file: %v", err)
	}
	if err := cmd.Flags().Set("tls-key-file", keyPath); err != nil {
		t.Fatalf("failed to set --tls-key-file: %v", err)
	}
	cfg, err := webTTYPlainServerTLSConfig(cmd)
	if err != nil {
		t.Fatalf("webTTYPlainServerTLSConfig() error = %v", err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatalf("expected one TLS certificate, got %#v", cfg)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("TLS MinVersion = %d, want TLS 1.3", cfg.MinVersion)
	}
	invalidCmd := newTestWebTTYServerCommand()
	if err := invalidCmd.Flags().Set("tls-cert-file", filepath.Join(t.TempDir(), "missing.crt")); err != nil {
		t.Fatalf("failed to set invalid --tls-cert-file: %v", err)
	}
	if err := invalidCmd.Flags().Set("tls-key-file", filepath.Join(t.TempDir(), "missing.key")); err != nil {
		t.Fatalf("failed to set invalid --tls-key-file: %v", err)
	}
	if _, err := webTTYPlainServerTLSConfig(invalidCmd); err == nil || !strings.Contains(err.Error(), "failed to load") {
		t.Fatalf("expected invalid server certificate error, got %v", err)
	}
}

func TestWebTTYSessionConfigFromClientConfigKeepsTransportDialers(t *testing.T) {
	tlsConfig := &tls.Config{ServerName: "terminal.example"}
	packetDialer := func(context.Context, string) (net.PacketConn, net.Addr, error) {
		return nil, nil, nil
	}
	endpointIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	serverIdentity := endpointIdentity.Public()
	cfg := &webtty.ClientConfig{
		URL:                    "https://terminal.example/",
		Transport:              webtty.WebTTYTransportWebTransport,
		DialPacketContext:      packetDialer,
		TLSConfig:              tlsConfig,
		EndpointIdentity:       endpointIdentity,
		ExpectedServerIdentity: &serverIdentity,
		ClientCredential:       []byte("workspace-managed-client-credential"),
		ClientPrincipalID:      "user-1",
		ClientDeviceID:         "device-1",
		ClientBrowserID:        "browser-1",
	}
	sessionCfg := webTTYSessionConfigFromClientConfig(cfg)
	if sessionCfg.TLSConfig == nil {
		t.Fatalf("expected TLSConfig to be propagated")
	}
	if sessionCfg.TLSConfig.ServerName != "terminal.example" {
		t.Fatalf("TLSConfig.ServerName = %q, want terminal.example", sessionCfg.TLSConfig.ServerName)
	}
	if sessionCfg.DialPacketContext == nil {
		t.Fatalf("expected DialPacketContext to be propagated")
	}
	if sessionCfg.EndpointIdentity != endpointIdentity {
		t.Fatalf("expected endpoint identity to be propagated")
	}
	if sessionCfg.ExpectedServerIdentity != &serverIdentity {
		t.Fatalf("expected server identity to be propagated")
	}
	if !bytes.Equal(sessionCfg.ClientCredential, []byte("workspace-managed-client-credential")) {
		t.Fatalf("client credential was not propagated: %q", sessionCfg.ClientCredential)
	}
	cfg.ClientCredential[0] = 'X'
	if bytes.Equal(sessionCfg.ClientCredential, cfg.ClientCredential) {
		t.Fatalf("client credential was not defensively copied")
	}
	if sessionCfg.ClientPrincipalID != "user-1" || sessionCfg.ClientDeviceID != "device-1" || sessionCfg.ClientBrowserID != "browser-1" {
		t.Fatalf("client identity context was not propagated: %#v", sessionCfg)
	}
}

func TestWebTTYWebTransportQUICConfig(t *testing.T) {
	direct := webTTYWebTransportQUICConfig(false)
	if direct.InitialPacketSize != 0 || direct.DisablePathMTUDiscovery {
		t.Fatalf("direct config unexpectedly constrains MTU: %#v", direct)
	}
	tunneled := webTTYWebTransportQUICConfig(true)
	if tunneled.InitialPacketSize != tunneledWebTTYWebTransportInitialPacketSize || !tunneled.DisablePathMTUDiscovery {
		t.Fatalf("tunneled config does not constrain MTU: %#v", tunneled)
	}
	if !tunneled.EnableDatagrams || !tunneled.EnableStreamResetPartialDelivery {
		t.Fatalf("tunneled config lost WebTransport features: %#v", tunneled)
	}
}

func testWebTTYCACertificatePEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := x509.Certificate{
		BasicConstraintsValid: true,
		IsCA:                  true,
		SerialNumber:          big.NewInt(1),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func testWebTTYServerCertificateFiles(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := x509.Certificate{
		BasicConstraintsValid: true,
		SerialNumber:          big.NewInt(2),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile(cert) error = %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	return certPath, keyPath
}

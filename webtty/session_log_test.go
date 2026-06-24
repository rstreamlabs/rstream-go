// See LICENSE file in the project root for license information.

package webtty

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestSessionAcceptedLogIncludesAuditFields(t *testing.T) {
	var buf bytes.Buffer
	required := true
	mode := WebTTYExecutionModeSpawn
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	s := &session{
		cfg: &ServerConfig{
			WorkspaceID:        "workspace-1",
			ProjectID:          "project-1",
			ServerID:           "server-1",
			RequireClientProof: &required,
			ExecutionMode:      &mode,
		},
		logger:          logger,
		acceptedAt:      time.Now(),
		clientPrincipal: "device-1",
		clientDeviceID:  "device-1",
		clientBrowserID: "browser-1",
		clientKeyID:     "client-key-1",
		serverKeyID:     "server-key-1",
	}
	s.logSessionAccepted()
	out := buf.String()
	for _, want := range []string{
		`msg="session accepted"`,
		`workspace_id=workspace-1`,
		`project_id=project-1`,
		`server_id=server-1`,
		`client_principal_id=device-1`,
		`client_device_id=device-1`,
		`client_browser_id=browser-1`,
		`client_signing_key_id=client-key-1`,
		`server_signing_key_id=server-key-1`,
		`client_proof_required=true`,
		`execution_mode=spawn`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("accepted log missing %q in %s", want, out)
		}
	}
}

func TestSessionAcceptedLogEmitsWhenAuthenticatedAtWasPreset(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	s := &session{
		cfg: &ServerConfig{
			ServerID: "server-1",
		},
		logger:          logger,
		acceptedAt:      time.Now(),
		authenticatedAt: time.Now(),
	}
	s.logSessionAccepted()
	s.logSessionAccepted()
	out := buf.String()
	if count := strings.Count(out, `msg="session accepted"`); count != 1 {
		t.Fatalf("accepted log count = %d, want 1 in %s", count, out)
	}
	if !strings.Contains(out, `server_id=server-1`) {
		t.Fatalf("accepted log missing server_id in %s", out)
	}
}

func TestSessionRejectedLogUsesStableReasonCode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	s := &session{
		cfg: &ServerConfig{
			ServerID: "server-1",
		},
		logger:      logger,
		acceptedAt:  time.Now(),
		clientKeyID: "client-key-1",
	}
	s.logSessionRejected(errors.Join(errWebTTYClientProofUnauthorized, errors.New("not trusted")))
	out := buf.String()
	for _, want := range []string{
		`msg="session rejected"`,
		`server_id=server-1`,
		`client_signing_key_id=client-key-1`,
		`reason_code=client_unauthorized`,
		`e2e=false`,
		`client_proof_required=false`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rejection log missing %q in %s", want, out)
		}
	}
}

func TestSessionClosedLogIncludesAuditAndPolicyFields(t *testing.T) {
	var buf bytes.Buffer
	required := true
	mode := WebTTYExecutionModeLogin
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	s := &session{
		conn: fakeSessionLogMessageConn{},
		cfg: &ServerConfig{
			WorkspaceID:        "workspace-1",
			ProjectID:          "project-1",
			ServerID:           "server-1",
			RequireClientProof: &required,
			ExecutionMode:      &mode,
		},
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		acceptedAt:      time.Now().Add(-10 * time.Millisecond),
		opened:          true,
		childDone:       true,
		childExitCode:   7,
		authenticatedAt: time.Now(),
		clientPrincipal: "device-1",
		clientKeyID:     "client-key-1",
		serverKeyID:     "server-key-1",
		doneCh:          make(chan struct{}),
	}
	s.close()
	out := buf.String()
	for _, want := range []string{
		`msg="session closed"`,
		`workspace_id=workspace-1`,
		`project_id=project-1`,
		`server_id=server-1`,
		`server_signing_key_id=server-key-1`,
		`client_principal_id=device-1`,
		`client_signing_key_id=client-key-1`,
		`opened=true`,
		`authenticated=true`,
		`child_done=true`,
		`exit_code=7`,
		`client_proof_required=true`,
		`execution_mode=login`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("closed log missing %q in %s", want, out)
		}
	}
}

type fakeSessionLogMessageConn struct{}

func (fakeSessionLogMessageConn) Close() error { return nil }

func (fakeSessionLogMessageConn) ReadMessage() (int, []byte, error) { return 0, nil, nil }

func (fakeSessionLogMessageConn) SetReadLimit(int64) {}

func (fakeSessionLogMessageConn) SetWriteDeadline(time.Time) error { return nil }

func (fakeSessionLogMessageConn) WriteControl(int, []byte, time.Time) error { return nil }

func (fakeSessionLogMessageConn) WriteMessage(int, []byte) error { return nil }

func TestWebTTYSessionErrorReasonCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "client proof required", err: errWebTTYClientProofRequired, want: "client_proof_required"},
		{name: "client proof invalid", err: errors.Join(errWebTTYClientProofInvalid, errors.New("bad signature")), want: "client_proof_invalid"},
		{name: "client unauthorized", err: errWebTTYClientProofUnauthorized, want: "client_unauthorized"},
		{name: "operation timeout", err: errSessionOperationTimeout, want: "operation_timeout"},
		{name: "session key grant", err: errors.New("E2E session key grant is required"), want: "session_key_grant_invalid"},
		{name: "generic", err: errors.New("boom"), want: "session_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webTTYSessionErrorReasonCode(tt.err); got != tt.want {
				t.Fatalf("webTTYSessionErrorReasonCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

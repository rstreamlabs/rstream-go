// See LICENSE file in the project root for license information.

//go:build windows

package webtty

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsPTYRequestFailsWithoutStartingCommand(t *testing.T) {
	if marker := os.Getenv("WEBTTY_WINDOWS_PTY_TEST_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("unexpected execution"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	zero := time.Duration(0)
	handler := NewWebTTYHandler(testServerConfig(ServerConfig{HeartbeatInterval: &zero}))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Shutdown(ctx)
	marker := filepath.Join(t.TempDir(), "must-not-execute")
	session, err := OpenClientSession(ctx, &SessionConfig{
		URL:           testWebTTYURL(server.URL),
		AllocateTTY:   true,
		CmdArgs:       []string{os.Args[0], "-test.run=^TestWindowsPTYRequestFailsWithoutStartingCommand$"},
		EnvVars:       []string{"WEBTTY_WINDOWS_PTY_TEST_MARKER=" + marker},
		OpenDeadline:  durationPtr(time.Second),
		CloseDeadline: durationPtr(time.Second),
	})
	if err == nil {
		_, _, _, err = collectClientSessionOutput(t, session)
	}
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Windows PTY request must fail explicitly, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unsupported PTY request started the command: %v", err)
	}
}

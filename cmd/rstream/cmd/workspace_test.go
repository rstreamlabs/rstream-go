// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func newTestWorkspaceListCommand(output *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("type", "", "")
	cmd.Flags().String("membership-status", "", "")
	cmd.Flags().StringP("output", "o", "table", "")
	cmd.SetOut(output)
	cmd.SetErr(output)
	return cmd
}

func TestRunWorkspaceListTable(t *testing.T) {
	clearRstreamTestEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/workspaces" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("type"); got != "organization" {
			http.Error(w, "unexpected type filter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controlplane.ListWorkspacesResponse{Workspaces: []controlplane.Workspace{{
			ID:   "workspace-1",
			Type: "organization",
			Name: "ACME",
			Membership: &controlplane.WorkspaceMembership{
				Role:   "owner",
				Status: "active",
			},
			Enterprise: &controlplane.WorkspaceEnterpriseSummary{
				Status:           "active",
				WorkspaceKeyMode: "enabled",
			},
		}}})
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWorkspaceListCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("type", "organization"); err != nil {
		t.Fatalf("set type: %v", err)
	}
	if err := runWorkspaceList(cmd); err != nil {
		t.Fatalf("runWorkspaceList() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "PROTECTION", "ACME", "owner", "enabled", "workspace-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("workspace list output missing %q:\n%s", want, got)
		}
	}
}

func TestListWorkspacesParamsFromFlagsValidation(t *testing.T) {
	for _, tt := range []struct {
		flag  string
		value string
	}{
		{flag: "type", value: "team"},
		{flag: "membership-status", value: "pending"},
	} {
		t.Run(tt.flag, func(t *testing.T) {
			var out bytes.Buffer
			cmd := newTestWorkspaceListCommand(&out)
			if err := cmd.Flags().Set(tt.flag, tt.value); err != nil {
				t.Fatalf("set flag: %v", err)
			}
			if _, err := listWorkspacesParamsFromFlags(cmd); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

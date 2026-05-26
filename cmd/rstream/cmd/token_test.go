// See LICENSE file in the project root for license information.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newTestTokenCreateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().StringArrayP("permission", "p", nil, "")
	cmd.Flags().String("resources-json", "", "")
	cmd.Flags().String("resources-file", "", "")
	cmd.Flags().StringP("output", "o", "text", "")
	return cmd
}

func TestCreateTokenRequestFromFlags(t *testing.T) {
	cmd := newTestTokenCreateCommand()
	if err := cmd.Flags().Set("permission", " tunnels.resources.read-only "); err != nil {
		t.Fatalf("failed to set permission: %v", err)
	}
	if err := cmd.Flags().Set("resources-json", `{"tunnels":{"projects":["p1"],"scopes":{"tunnels":{"connect":true}}}}`); err != nil {
		t.Fatalf("failed to set resources: %v", err)
	}
	request, err := createTokenRequestFromFlags(cmd)
	if err != nil {
		t.Fatalf("createTokenRequestFromFlags returned error: %v", err)
	}
	if request.Permissions == nil || len(*request.Permissions) != 1 || (*request.Permissions)[0] != "tunnels.resources.read-only" {
		t.Fatalf("unexpected permissions: %#v", request.Permissions)
	}
	if request.Resources == nil || !strings.Contains(string(*request.Resources), `"tunnels"`) {
		t.Fatalf("unexpected resources: %#v", request.Resources)
	}
}

func TestCreateTokenRequestAllowsInheritedPermissions(t *testing.T) {
	request, err := createTokenRequestFromFlags(newTestTokenCreateCommand())
	if err != nil {
		t.Fatalf("createTokenRequestFromFlags returned error: %v", err)
	}
	if request.Permissions != nil || request.Resources != nil {
		t.Fatalf("expected nil permissions/resources, got %#v", request)
	}
}

func TestTokenCreateResourcesFromFile(t *testing.T) {
	cmd := newTestTokenCreateCommand()
	path := filepath.Join(t.TempDir(), "resources.json")
	if err := os.WriteFile(path, []byte(`{"tunnels":{"projects":["p1"],"scopes":{"tunnels":{"connect":true}}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := cmd.Flags().Set("resources-file", path); err != nil {
		t.Fatalf("failed to set resources file: %v", err)
	}
	resources, err := tokenCreateResourcesFromFlags(cmd)
	if err != nil {
		t.Fatalf("tokenCreateResourcesFromFlags returned error: %v", err)
	}
	if resources == nil || !strings.Contains(string(*resources), `"projects"`) {
		t.Fatalf("unexpected resources: %#v", resources)
	}
}

func TestTokenCreateRejectsInvalidResourcesJSON(t *testing.T) {
	cmd := newTestTokenCreateCommand()
	if err := cmd.Flags().Set("resources-json", `{`); err != nil {
		t.Fatalf("failed to set resources: %v", err)
	}
	if _, err := tokenCreateResourcesFromFlags(cmd); err == nil || !strings.Contains(err.Error(), "invalid resource boundary JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

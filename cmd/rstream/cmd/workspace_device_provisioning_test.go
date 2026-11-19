// See LICENSE file in the project root for license information.

package cmd

import (
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func TestWorkspaceDeviceProvisioningDoesNotImpersonateTheTarget(t *testing.T) {
	identity := controlplane.CallerIdentity{Owner: controlplane.CredentialOwner{Kind: "user", UserID: "manager"}, CredentialID: "human-pat", Name: "Administrator"}
	workspace := workspaceDeviceWorkspaceResolution{WorkspaceID: "workspace", Identity: identity, ForCredentialID: "certificate"}
	owner, credentialID := workspace.deviceOwner()
	if owner.Kind != "workspace" || owner.WorkspaceID != workspace.WorkspaceID || owner.UserID != "" || credentialID != "certificate" {
		t.Fatalf("resource owner = %#v / %q", owner, credentialID)
	}
	if !reflect.DeepEqual(workspace.Identity, identity) {
		t.Fatal("resource selection replaced the authenticated human identity")
	}
	service := workspaceDeviceFile{WorkspaceID: "workspace", CredentialID: "certificate"}
	if !workspaceDeviceMatchesOwner(service, owner, credentialID) || workspaceDeviceMatchesOwner(service, identity.Owner, identity.CredentialID) || workspaceDeviceMatchesOwner(service, owner, "other-certificate") {
		t.Fatal("target device ownership is not isolated from the human or other tokens")
	}
	workspace.ForCredentialID = ""
	owner, credentialID = workspace.deviceOwner()
	if owner != identity.Owner || credentialID != identity.CredentialID {
		t.Fatal("ordinary device ownership changed")
	}
}

func TestWorkspaceDeviceProvisioningTargetRequiresAnExplicitHumanAndTokenID(t *testing.T) {
	human := controlplane.CallerIdentity{Owner: controlplane.CredentialOwner{Kind: "user", UserID: "manager"}}
	machine := controlplane.CallerIdentity{Owner: controlplane.CredentialOwner{Kind: "workspace", WorkspaceID: "workspace"}, CredentialID: "source"}
	for _, value := range []string{"", ".", "..", "workspace/token", `workspace\token`} {
		cmd := &cobra.Command{}
		cmd.Flags().String("for-token", "", "")
		if err := cmd.Flags().Set("for-token", value); err != nil {
			t.Fatal(err)
		}
		if _, err := workspaceDeviceForToken(cmd, human); err == nil {
			t.Errorf("accepted malformed explicit target %q", value)
		}
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("for-token", "", "")
	if target, err := workspaceDeviceForToken(cmd, machine); err != nil || target != "" {
		t.Fatal("ordinary machine self-enrollment changed")
	}
	if err := cmd.Flags().Set("for-token", " certificate "); err != nil {
		t.Fatal(err)
	}
	if target, err := workspaceDeviceForToken(cmd, human); err != nil || target != "certificate" {
		t.Fatalf("explicit human target = %q / %v", target, err)
	}
	if _, err := workspaceDeviceForToken(cmd, machine); err == nil {
		t.Fatal("a machine token can select another resource owner")
	}
}

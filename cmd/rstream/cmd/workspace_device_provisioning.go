// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

// These are the existing device operations, scoped to a target resource when a
// human manager provisions a service. Authentication stays on the original client.
type workspaceDeviceClient interface {
	CreateWorkspaceDeviceKey(context.Context, string, controlplane.CreateWorkspaceDeviceKeyRequest) (controlplane.CreateWorkspaceDeviceKeyResponse, error)
	LookupWorkspaceDeviceKeys(context.Context, string, controlplane.LookupWorkspaceDeviceKeysRequest) (controlplane.LookupWorkspaceDeviceKeysResponse, error)
	RevokeWorkspaceDeviceKey(context.Context, string, string, controlplane.RevokeWorkspaceDeviceKeyRequest) error
}

type workspaceDeviceProvisioningClient struct {
	client       *controlplane.Client
	credentialID string
}

func workspaceDeviceClientForWorkspace(client *controlplane.Client, workspace workspaceDeviceWorkspaceResolution) workspaceDeviceClient {
	if workspace.ForCredentialID == "" {
		return client
	}
	return &workspaceDeviceProvisioningClient{client: client, credentialID: workspace.ForCredentialID}
}

func (c *workspaceDeviceProvisioningClient) CreateWorkspaceDeviceKey(ctx context.Context, workspaceID string, request controlplane.CreateWorkspaceDeviceKeyRequest) (controlplane.CreateWorkspaceDeviceKeyResponse, error) {
	return c.client.CreateWorkspaceCredentialDeviceKey(ctx, workspaceID, c.credentialID, request)
}

func (c *workspaceDeviceProvisioningClient) LookupWorkspaceDeviceKeys(ctx context.Context, workspaceID string, request controlplane.LookupWorkspaceDeviceKeysRequest) (controlplane.LookupWorkspaceDeviceKeysResponse, error) {
	return c.client.LookupWorkspaceCredentialDeviceKeys(ctx, workspaceID, c.credentialID, request)
}

func (c *workspaceDeviceProvisioningClient) RevokeWorkspaceDeviceKey(ctx context.Context, workspaceID string, deviceID string, request controlplane.RevokeWorkspaceDeviceKeyRequest) error {
	return c.client.RevokeWorkspaceCredentialDeviceKey(ctx, workspaceID, c.credentialID, deviceID, request)
}

func workspaceDeviceForToken(cmd *cobra.Command, identity controlplane.CallerIdentity) (string, error) {
	if cmd.Flags().Lookup("for-token") == nil || !cmd.Flags().Changed("for-token") {
		return "", nil
	}
	value, _ := cmd.Flags().GetString("for-token")
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("--for-token requires a workspace token ID")
	}
	if identity.Owner.Kind != "user" {
		return "", fmt.Errorf("--for-token requires a personal token with workspace owner or admin access")
	}
	return value, nil
}

func (workspace workspaceDeviceWorkspaceResolution) deviceOwner() (controlplane.CredentialOwner, string) {
	if workspace.ForCredentialID != "" {
		return controlplane.CredentialOwner{Kind: "workspace", WorkspaceID: workspace.WorkspaceID}, workspace.ForCredentialID
	}
	return workspace.Identity.Owner, workspace.Identity.CredentialID
}

func loadWorkspaceDeviceFilesForWorkspace(workspace workspaceDeviceWorkspaceResolution) ([]workspaceDeviceFileWithPath, error) {
	owner, credentialID := workspace.deviceOwner()
	return loadWorkspaceDeviceFilesForOwner(workspace.WorkspaceID, owner, credentialID)
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"testing"

	"github.com/rstreamlabs/rstream-go/webtty"
)

func writeTestWorkspaceDeviceWithWebTTYIdentity(t *testing.T, device workspaceDeviceFile, identity *webtty.E2EIdentity) {
	t.Helper()
	if identity == nil {
		t.Fatalf("missing test WebTTY identity")
	}
	identityPath, err := defaultWorkspaceWebTTYDeviceIdentityPath(device.WorkspaceID, device.DeviceKeyID)
	if err != nil {
		t.Fatalf("defaultWorkspaceWebTTYDeviceIdentityPath() error = %v", err)
	}
	device.WebTTYIdentityPath = identityPath
	if err := webtty.WriteE2EIdentityFile(identityPath, *identity); err != nil {
		t.Fatalf("WriteE2EIdentityFile() error = %v", err)
	}
	if _, err := writeWorkspaceDeviceFile(device); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile() error = %v", err)
	}
}

func testWorkspaceDeviceAuthorizedWebTTYSigningKeys(t *testing.T, device workspaceDeviceFile) map[string][]byte {
	t.Helper()
	signingKey := parseWorkspaceDevicePublicKey(t, device.PublicSigningKey)
	publicDER, err := webtty.MarshalWebTTYSigningPublicKey(signingKey)
	if err != nil {
		t.Fatalf("MarshalWebTTYSigningPublicKey() error = %v", err)
	}
	return map[string][]byte{
		string(webtty.WebTTYSigningKeyID(publicDER)): publicDER,
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

func newTestWebTTYAuthorizedClientCommand(output *bytes.Buffer, withKey bool) *cobra.Command {
	cmd := &cobra.Command{Use: "authorized-client"}
	cmd.Flags().String("identity", "", "")
	cmd.Flags().String("identity-file", "", "")
	cmd.Flags().String("server-id", "", "")
	cmd.Flags().String("authorized-clients-file", "", "")
	cmd.Flags().StringP("output", "o", "text", "")
	if withKey {
		cmd.Flags().String("key", "", "")
	}
	cmd.SetOut(output)
	return cmd
}

func TestWebTTYAuthorizedClientAddListReplaceAndRemove(t *testing.T) {
	authorizedClientsFile := filepath.Join(t.TempDir(), "authorized_clients.json")
	firstIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() first error = %v", err)
	}
	secondIdentity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() second error = %v", err)
	}
	var addOut bytes.Buffer
	addCmd := newTestWebTTYAuthorizedClientCommand(&addOut, true)
	if err := addCmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := addCmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(firstIdentity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYAuthorizedClientAdd(addCmd, "operator-workstation"); err != nil {
		t.Fatalf("runWebTTYAuthorizedClientAdd() error = %v", err)
	}
	doc, err := webtty.ReadAuthorizedClientKeysFile(authorizedClientsFile)
	if err != nil {
		t.Fatalf("ReadAuthorizedClientKeysFile() error = %v", err)
	}
	if len(doc.AuthorizedClients) != 1 || doc.AuthorizedClients[0].Name != "operator-workstation" {
		t.Fatalf("unexpected authorized clients file after add: %#v", doc.AuthorizedClients)
	}
	if got := addOut.String(); !strings.Contains(got, "operator-workstation") || !strings.Contains(got, doc.AuthorizedClients[0].SigningKeyID) {
		t.Fatalf("add output does not include authorized client: %q", got)
	}
	var listOut bytes.Buffer
	listCmd := newTestWebTTYAuthorizedClientCommand(&listOut, false)
	if err := listCmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := runWebTTYAuthorizedClientList(listCmd); err != nil {
		t.Fatalf("runWebTTYAuthorizedClientList() error = %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, "operator-workstation") || !strings.Contains(got, doc.AuthorizedClients[0].SigningKeyID) {
		t.Fatalf("list output does not include authorized client: %q", got)
	}
	replaceCmd := newTestWebTTYAuthorizedClientCommand(&bytes.Buffer{}, true)
	if err := replaceCmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := replaceCmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(secondIdentity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYAuthorizedClientAdd(replaceCmd, "operator-workstation"); err != nil {
		t.Fatalf("replace authorized client error = %v", err)
	}
	doc, err = webtty.ReadAuthorizedClientKeysFile(authorizedClientsFile)
	if err != nil {
		t.Fatalf("ReadAuthorizedClientKeysFile() after replace error = %v", err)
	}
	if len(doc.AuthorizedClients) != 1 || doc.AuthorizedClients[0].SigningKeyID != webtty.EncodeE2EKeyMaterial(secondIdentity.Signing.KeyID) {
		t.Fatalf("unexpected authorized clients file after replace: %#v", doc.AuthorizedClients)
	}
	removeCmd := newTestWebTTYAuthorizedClientCommand(&bytes.Buffer{}, false)
	if err := removeCmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := runWebTTYAuthorizedClientRemove(removeCmd, "operator-workstation"); err != nil {
		t.Fatalf("runWebTTYAuthorizedClientRemove() error = %v", err)
	}
	doc, err = webtty.ReadAuthorizedClientKeysFile(authorizedClientsFile)
	if err != nil {
		t.Fatalf("ReadAuthorizedClientKeysFile() after remove error = %v", err)
	}
	if len(doc.AuthorizedClients) != 0 {
		t.Fatalf("authorized client was not removed: %#v", doc.AuthorizedClients)
	}
	if err := runWebTTYAuthorizedClientRemove(removeCmd, "operator-workstation"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected missing authorized client error, got %v", err)
	}
}

func TestWebTTYAuthorizedClientDefaultPathUsesIdentityName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := newTestWebTTYAuthorizedClientCommand(&bytes.Buffer{}, false)
	if err := cmd.Flags().Set("identity", "prod-shell"); err != nil {
		t.Fatalf("set identity: %v", err)
	}
	path, err := authorizedClientsPathFromCommandFlags(cmd)
	if err != nil {
		t.Fatalf("authorizedClientsPathFromCommandFlags() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join(".rstream", "webtty", "authorized_clients", "prod-shell.json")) {
		t.Fatalf("authorized clients path = %q", path)
	}
}

func TestWebTTYAuthorizedClientAddCanDeriveName(t *testing.T) {
	authorizedClientsFile := filepath.Join(t.TempDir(), "authorized_clients.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	var out bytes.Buffer
	addCmd := newTestWebTTYAuthorizedClientCommand(&out, true)
	if err := addCmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := addCmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYAuthorizedClientAdd(addCmd, ""); err != nil {
		t.Fatalf("runWebTTYAuthorizedClientAdd() error = %v", err)
	}
	doc, err := webtty.ReadAuthorizedClientKeysFile(authorizedClientsFile)
	if err != nil {
		t.Fatalf("ReadAuthorizedClientKeysFile() error = %v", err)
	}
	if len(doc.AuthorizedClients) != 1 {
		t.Fatalf("authorized clients = %#v", doc.AuthorizedClients)
	}
	if !strings.HasPrefix(doc.AuthorizedClients[0].Name, "client-") {
		t.Fatalf("derived name = %q", doc.AuthorizedClients[0].Name)
	}
	if !strings.Contains(out.String(), doc.AuthorizedClients[0].Name) {
		t.Fatalf("output missing derived name: %q", out.String())
	}
}

func TestWebTTYAuthorizedClientAddRejectsMissingKeyBeforeWriting(t *testing.T) {
	authorizedClientsFile := filepath.Join(t.TempDir(), "authorized_clients.json")
	cmd := newTestWebTTYAuthorizedClientCommand(&bytes.Buffer{}, true)
	if err := cmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := runWebTTYAuthorizedClientAdd(cmd, "operator-workstation"); err == nil || !strings.Contains(err.Error(), "WebTTY client signing key") {
		t.Fatalf("expected missing signing key error, got %v", err)
	}
	if _, err := webtty.ReadAuthorizedClientKeysFile(authorizedClientsFile); err == nil {
		t.Fatalf("authorized client file was created despite invalid input")
	}
}

func TestWebTTYAuthorizedClientAddRejectsInvalidNameBeforeWriting(t *testing.T) {
	authorizedClientsFile := filepath.Join(t.TempDir(), "authorized_clients.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYAuthorizedClientCommand(&bytes.Buffer{}, true)
	if err := cmd.Flags().Set("authorized-clients-file", authorizedClientsFile); err != nil {
		t.Fatalf("set authorized-clients-file: %v", err)
	}
	if err := cmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYAuthorizedClientAdd(cmd, "../operator-workstation"); err == nil || !strings.Contains(err.Error(), "unsupported characters") {
		t.Fatalf("expected invalid authorized client name error, got %v", err)
	}
	if _, err := webtty.ReadAuthorizedClientKeysFile(authorizedClientsFile); err == nil {
		t.Fatalf("authorized client file was created despite invalid name")
	}
}

func TestWebTTYAuthorizedClientAddServerIDAcceptsEndpointIdentityAndDerivesName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	var out bytes.Buffer
	cmd := newTestWebTTYAuthorizedClientCommand(&out, true)
	if err := cmd.Flags().Set("server-id", "registered-shell"); err != nil {
		t.Fatalf("set server-id: %v", err)
	}
	if err := cmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYAuthorizedClientAdd(cmd, ""); err != nil {
		t.Fatalf("runWebTTYAuthorizedClientAdd() error = %v", err)
	}
	path, err := webtty.DefaultAuthorizedClientKeysPath("registered-shell")
	if err != nil {
		t.Fatalf("DefaultAuthorizedClientKeysPath() error = %v", err)
	}
	doc, err := webtty.ReadAuthorizedClientKeysFile(path)
	if err != nil {
		t.Fatalf("ReadAuthorizedClientKeysFile() error = %v", err)
	}
	if len(doc.AuthorizedClients) != 1 {
		t.Fatalf("authorized clients = %#v", doc.AuthorizedClients)
	}
	entry := doc.AuthorizedClients[0]
	if !strings.HasPrefix(entry.Name, "client-") {
		t.Fatalf("derived authorized client name = %q", entry.Name)
	}
	if entry.SigningKeyID != webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID) ||
		entry.SigningPublicKey != webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey) {
		t.Fatalf("endpoint identity signing material was not stored: %#v", entry)
	}
	if got := out.String(); !strings.Contains(got, entry.Name) || !strings.Contains(got, path) {
		t.Fatalf("output missing derived client or path: %q", got)
	}
}

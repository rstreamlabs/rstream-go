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

func newTestWebTTYIdentityCreateCommand(output *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("identity-file", "", "")
	cmd.Flags().Bool("endpoint-identity", false, "")
	cmd.Flags().StringP("output", "o", "text", "")
	cmd.SetOut(output)
	return cmd
}

func newTestWebTTYIdentityShowCommand(output *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "show"}
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("identity-file", "", "")
	cmd.Flags().Bool("endpoint-identity", false, "")
	cmd.Flags().StringP("output", "o", "text", "")
	cmd.SetOut(output)
	return cmd
}

func newTestWebTTYIdentityListCommand(output *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().StringP("output", "o", "text", "")
	cmd.SetOut(output)
	return cmd
}

func newTestWebTTYIdentityRemoveCommand(output *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "remove"}
	cmd.Flags().StringP("output", "o", "text", "")
	cmd.SetOut(output)
	return cmd
}

func newTestWebTTYKnownServerCommand(output *bytes.Buffer, withKey bool) *cobra.Command {
	cmd := &cobra.Command{Use: "known-server"}
	if withKey {
		cmd.Flags().String("key", "", "")
		cmd.Flags().String("client-identity", "", "")
	}
	cmd.Flags().String("identity", "", "")
	cmd.Flags().String("known-servers-file", "", "")
	cmd.Flags().StringP("output", "o", "text", "")
	cmd.SetOut(output)
	return cmd
}

func TestWebTTYIdentityCreateAndShowWithExplicitFile(t *testing.T) {
	identityFile := filepath.Join(t.TempDir(), "server.identity.json")
	var createOut bytes.Buffer
	createCmd := newTestWebTTYIdentityCreateCommand(&createOut)
	if err := createCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := runWebTTYIdentityCreate(createCmd); err != nil {
		t.Fatalf("runWebTTYIdentityCreate() error = %v", err)
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityFile)
	if err != nil {
		t.Fatalf("LoadWebTTYEndpointIdentityFile() error = %v", err)
	}
	createdText := createOut.String()
	public := identity.Public()
	encryptionPublicKey := webtty.EncodeE2EKeyMaterial(public.EncryptionPublicKey)
	encryptionKeyID := webtty.EncodeE2EKeyMaterial(public.EncryptionKeyID)
	signingPublicKey := webtty.EncodeE2EKeyMaterial(public.SigningPublicKey)
	signingKeyID := webtty.EncodeE2EKeyMaterial(public.SigningKeyID)
	if !strings.Contains(createdText, identityFile) ||
		!strings.Contains(createdText, encryptionPublicKey) ||
		!strings.Contains(createdText, encryptionKeyID) ||
		!strings.Contains(createdText, signingPublicKey) ||
		!strings.Contains(createdText, signingKeyID) {
		t.Fatalf("unexpected create output: %q", createdText)
	}
	var showOut bytes.Buffer
	showCmd := newTestWebTTYIdentityShowCommand(&showOut)
	if err := showCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := runWebTTYIdentityShow(showCmd); err != nil {
		t.Fatalf("runWebTTYIdentityShow() error = %v", err)
	}
	shownText := showOut.String()
	if !strings.Contains(shownText, encryptionPublicKey) ||
		!strings.Contains(shownText, encryptionKeyID) ||
		!strings.Contains(shownText, signingPublicKey) ||
		!strings.Contains(shownText, signingKeyID) {
		t.Fatalf("show output does not match created identity: %q", shownText)
	}
}

func TestWebTTYIdentityShowEndpointIdentityOnly(t *testing.T) {
	identityFile := filepath.Join(t.TempDir(), "server.identity.json")
	createCmd := newTestWebTTYIdentityCreateCommand(&bytes.Buffer{})
	if err := createCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := runWebTTYIdentityCreate(createCmd); err != nil {
		t.Fatalf("runWebTTYIdentityCreate() error = %v", err)
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityFile)
	if err != nil {
		t.Fatalf("LoadWebTTYEndpointIdentityFile() error = %v", err)
	}
	want := webtty.KnownServerEndpointIdentityString(identity.Public())
	var showOut bytes.Buffer
	showCmd := newTestWebTTYIdentityShowCommand(&showOut)
	if err := showCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := showCmd.Flags().Set("endpoint-identity", "true"); err != nil {
		t.Fatalf("set endpoint-identity: %v", err)
	}
	if err := runWebTTYIdentityShow(showCmd); err != nil {
		t.Fatalf("runWebTTYIdentityShow() error = %v", err)
	}
	if got := strings.TrimSpace(showOut.String()); got != want {
		t.Fatalf("endpoint identity output = %q, want %q", got, want)
	}
	if strings.Contains(showOut.String(), "Endpoint identity:") || strings.Contains(showOut.String(), "Encryption") {
		t.Fatalf("endpoint-only output contains labels: %q", showOut.String())
	}
}

func TestWebTTYIdentityCreateEndpointIdentityOnly(t *testing.T) {
	identityFile := filepath.Join(t.TempDir(), "server.identity.json")
	var createOut bytes.Buffer
	createCmd := newTestWebTTYIdentityCreateCommand(&createOut)
	if err := createCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := createCmd.Flags().Set("endpoint-identity", "true"); err != nil {
		t.Fatalf("set endpoint-identity: %v", err)
	}
	if err := runWebTTYIdentityCreate(createCmd); err != nil {
		t.Fatalf("runWebTTYIdentityCreate() error = %v", err)
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityFile)
	if err != nil {
		t.Fatalf("LoadWebTTYEndpointIdentityFile() error = %v", err)
	}
	want := webtty.KnownServerEndpointIdentityString(identity.Public())
	if got := strings.TrimSpace(createOut.String()); got != want {
		t.Fatalf("endpoint identity output = %q, want %q", got, want)
	}
}

func TestWebTTYIdentityEndpointIdentityOnlyRejectsStructuredOutput(t *testing.T) {
	identityFile := filepath.Join(t.TempDir(), "server.identity.json")
	createCmd := newTestWebTTYIdentityCreateCommand(&bytes.Buffer{})
	if err := createCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := runWebTTYIdentityCreate(createCmd); err != nil {
		t.Fatalf("runWebTTYIdentityCreate() error = %v", err)
	}
	showCmd := newTestWebTTYIdentityShowCommand(&bytes.Buffer{})
	if err := showCmd.Flags().Set("identity-file", identityFile); err != nil {
		t.Fatalf("set identity-file: %v", err)
	}
	if err := showCmd.Flags().Set("endpoint-identity", "true"); err != nil {
		t.Fatalf("set endpoint-identity: %v", err)
	}
	if err := showCmd.Flags().Set("output", "json"); err != nil {
		t.Fatalf("set output: %v", err)
	}
	if err := runWebTTYIdentityShow(showCmd); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected endpoint/output conflict, got %v", err)
	}
}

func TestWebTTYIdentityListAndRemoveNamedIdentities(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"alpha", "beta"} {
		createCmd := newTestWebTTYIdentityCreateCommand(&bytes.Buffer{})
		if err := createCmd.Flags().Set("name", name); err != nil {
			t.Fatalf("set name: %v", err)
		}
		if err := runWebTTYIdentityCreate(createCmd); err != nil {
			t.Fatalf("runWebTTYIdentityCreate(%q) error = %v", name, err)
		}
	}
	var listOut bytes.Buffer
	listCmd := newTestWebTTYIdentityListCommand(&listOut)
	if err := runWebTTYIdentityList(listCmd); err != nil {
		t.Fatalf("runWebTTYIdentityList() error = %v", err)
	}
	listText := listOut.String()
	if !strings.Contains(listText, "alpha") || !strings.Contains(listText, "beta") {
		t.Fatalf("list output does not include named identities: %q", listText)
	}
	removeCmd := newTestWebTTYIdentityRemoveCommand(&bytes.Buffer{})
	if err := runWebTTYIdentityRemove(removeCmd, "alpha"); err != nil {
		t.Fatalf("runWebTTYIdentityRemove() error = %v", err)
	}
	var listAfterRemove bytes.Buffer
	listCmd = newTestWebTTYIdentityListCommand(&listAfterRemove)
	if err := runWebTTYIdentityList(listCmd); err != nil {
		t.Fatalf("runWebTTYIdentityList() after remove error = %v", err)
	}
	if got := listAfterRemove.String(); strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Fatalf("unexpected list output after remove: %q", got)
	}
	if err := runWebTTYIdentityRemove(removeCmd, "alpha"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected missing identity error, got %v", err)
	}
}

func TestWebTTYKnownServerAddListReplaceAndRemove(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	first, firstKey := testKnownServerKey(t)
	second, secondKey := testKnownServerKey(t)
	var addOut bytes.Buffer
	addCmd := newTestWebTTYKnownServerCommand(&addOut, true)
	if err := addCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := addCmd.Flags().Set("key", firstKey); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYKnownServerAdd(addCmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYKnownServerAdd() error = %v", err)
	}
	doc, err := webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if len(doc.KnownServers) != 1 || doc.KnownServers[0].Name != "prod-shell" || doc.KnownServers[0].PublicKey != first {
		t.Fatalf("unexpected known server file after add: %#v", doc.KnownServers)
	}
	var listOut bytes.Buffer
	listCmd := newTestWebTTYKnownServerCommand(&listOut, false)
	if err := listCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := runWebTTYKnownServerList(listCmd); err != nil {
		t.Fatalf("runWebTTYKnownServerList() error = %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, "prod-shell") || !strings.Contains(got, doc.KnownServers[0].KeyID) {
		t.Fatalf("list output does not include known server: %q", got)
	}
	replaceCmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := replaceCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := replaceCmd.Flags().Set("key", secondKey); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYKnownServerAdd(replaceCmd, "prod-shell"); err != nil {
		t.Fatalf("replace known server error = %v", err)
	}
	doc, err = webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() after replace error = %v", err)
	}
	if len(doc.KnownServers) != 1 || doc.KnownServers[0].PublicKey != second {
		t.Fatalf("unexpected known server file after replace: %#v", doc.KnownServers)
	}
	removeCmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, false)
	if err := removeCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := runWebTTYKnownServerRemove(removeCmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYKnownServerRemove() error = %v", err)
	}
	doc, err = webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() after remove error = %v", err)
	}
	if len(doc.KnownServers) != 0 {
		t.Fatalf("known server was not removed: %#v", doc.KnownServers)
	}
	if err := runWebTTYKnownServerRemove(removeCmd, "prod-shell"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected missing known server error, got %v", err)
	}
}

func TestWebTTYKnownServerAddStoresEndpointIdentity(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	endpoint := webtty.KnownServerEndpointIdentityString(identity.Public())
	cmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := cmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := cmd.Flags().Set("key", endpoint); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYKnownServerAdd(cmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYKnownServerAdd() error = %v", err)
	}
	doc, err := webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if len(doc.KnownServers) != 1 {
		t.Fatalf("known server count = %d, want 1", len(doc.KnownServers))
	}
	entry := doc.KnownServers[0]
	if entry.SigningKeyID != webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID) ||
		entry.SigningPublicKey != webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey) {
		t.Fatalf("trusted endpoint signing identity was not stored: %#v", entry)
	}
}

func TestWebTTYKnownServerAddRequiresTargetName(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := cmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := cmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	err = runWebTTYKnownServerAdd(cmd, "")
	if err == nil || !strings.Contains(err.Error(), "target is required") {
		t.Fatalf("runWebTTYKnownServerAdd() error = %v, want target required", err)
	}
}

func TestWebTTYKnownServerAddStoresClientIdentityAssociation(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := cmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := cmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := cmd.Flags().Set("client-identity", "review-client"); err != nil {
		t.Fatalf("set client-identity: %v", err)
	}
	if err := runWebTTYKnownServerAdd(cmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYKnownServerAdd() error = %v", err)
	}
	doc, err := webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if len(doc.KnownServers) != 1 {
		t.Fatalf("known server count = %d, want 1", len(doc.KnownServers))
	}
	if got := doc.KnownServers[0].ClientIdentity; got != "review-client" {
		t.Fatalf("client identity = %q, want review-client", got)
	}
}

func TestWebTTYKnownServerAddPreservesClientIdentityWhenReplacingKey(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	first, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(first) error = %v", err)
	}
	second, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity(second) error = %v", err)
	}
	addCmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := addCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := addCmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(first.Public())); err != nil {
		t.Fatalf("set key first: %v", err)
	}
	if err := addCmd.Flags().Set("client-identity", "review-client"); err != nil {
		t.Fatalf("set client-identity: %v", err)
	}
	if err := runWebTTYKnownServerAdd(addCmd, "prod-shell"); err != nil {
		t.Fatalf("initial runWebTTYKnownServerAdd() error = %v", err)
	}
	replaceCmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := replaceCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set replace known-servers-file: %v", err)
	}
	if err := replaceCmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(second.Public())); err != nil {
		t.Fatalf("set key second: %v", err)
	}
	if err := runWebTTYKnownServerAdd(replaceCmd, "prod-shell"); err != nil {
		t.Fatalf("replace runWebTTYKnownServerAdd() error = %v", err)
	}
	doc, err := webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if len(doc.KnownServers) != 1 {
		t.Fatalf("known server count = %d, want 1", len(doc.KnownServers))
	}
	entry := doc.KnownServers[0]
	if got := entry.ClientIdentity; got != "review-client" {
		t.Fatalf("client identity after key replace = %q, want review-client", got)
	}
	if got := entry.SigningKeyID; got != webtty.EncodeE2EKeyMaterial(second.Signing.KeyID) {
		t.Fatalf("signing key id after replace = %q, want second identity", got)
	}
}

func TestWebTTYKnownServerAddRejectsInvalidClientIdentity(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	cmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := cmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := cmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := cmd.Flags().Set("client-identity", "../review-client"); err != nil {
		t.Fatalf("set client-identity: %v", err)
	}
	if err := runWebTTYKnownServerAdd(cmd, "prod-shell"); err == nil || !strings.Contains(err.Error(), "--client-identity contains unsupported characters") {
		t.Fatalf("expected invalid client identity error, got %v", err)
	}
}

func TestWebTTYKnownServerSetIdentityAssociatesExistingServer(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	identity, err := webtty.GenerateWebTTYEndpointIdentity()
	if err != nil {
		t.Fatalf("GenerateWebTTYEndpointIdentity() error = %v", err)
	}
	addCmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, true)
	if err := addCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := addCmd.Flags().Set("key", webtty.KnownServerEndpointIdentityString(identity.Public())); err != nil {
		t.Fatalf("set key: %v", err)
	}
	if err := runWebTTYKnownServerAdd(addCmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYKnownServerAdd() error = %v", err)
	}
	var setOut bytes.Buffer
	setCmd := newTestWebTTYKnownServerCommand(&setOut, false)
	if err := setCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := setCmd.Flags().Set("identity", "review-client"); err != nil {
		t.Fatalf("set identity: %v", err)
	}
	if err := runWebTTYKnownServerSetIdentity(setCmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYKnownServerSetIdentity() error = %v", err)
	}
	doc, err := webtty.ReadKnownServerKeysFile(knownServersFile)
	if err != nil {
		t.Fatalf("ReadKnownServerKeysFile() error = %v", err)
	}
	if got := doc.KnownServers[0].ClientIdentity; got != "review-client" {
		t.Fatalf("client identity = %q, want review-client", got)
	}
	if got := setOut.String(); !strings.Contains(got, "Client identity: review-client") {
		t.Fatalf("set identity output does not mention association: %q", got)
	}
	var listOut bytes.Buffer
	listCmd := newTestWebTTYKnownServerCommand(&listOut, false)
	if err := listCmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := runWebTTYKnownServerList(listCmd); err != nil {
		t.Fatalf("runWebTTYKnownServerList() error = %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, "client_identity=review-client") {
		t.Fatalf("list output does not include client identity: %q", got)
	}
}

func TestWebTTYKnownServerSetIdentityRejectsMissingServer(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	cmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, false)
	if err := cmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := cmd.Flags().Set("identity", "review-client"); err != nil {
		t.Fatalf("set identity: %v", err)
	}
	if err := runWebTTYKnownServerSetIdentity(cmd, "missing"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected missing server error, got %v", err)
	}
}

func TestWebTTYKnownServerSetIdentityRequiresIdentity(t *testing.T) {
	knownServersFile := filepath.Join(t.TempDir(), "known_servers.json")
	cmd := newTestWebTTYKnownServerCommand(&bytes.Buffer{}, false)
	if err := cmd.Flags().Set("known-servers-file", knownServersFile); err != nil {
		t.Fatalf("set known-servers-file: %v", err)
	}
	if err := runWebTTYKnownServerSetIdentity(cmd, "prod-shell"); err == nil || !strings.Contains(err.Error(), "--identity is required") {
		t.Fatalf("expected missing identity error, got %v", err)
	}
}

func testKnownServerKey(t *testing.T) (string, string) {
	t.Helper()
	identity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		t.Fatalf("GenerateE2EIdentity() error = %v", err)
	}
	publicKey := webtty.EncodeE2EKeyMaterial(identity.PublicKey)
	keyID := webtty.EncodeE2EKeyMaterial(identity.KeyID)
	return publicKey, keyID + ":" + publicKey
}

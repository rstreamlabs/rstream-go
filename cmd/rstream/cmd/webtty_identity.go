// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

var webttyIdentityCmd = &cobra.Command{
	Use:          "identity",
	Short:        "Manage local WebTTY endpoint identities",
	GroupID:      "webtty-server",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var webttyIdentityCreateCmd = &cobra.Command{
	Use:          "create",
	Short:        "Create a local WebTTY endpoint identity",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYIdentityCreate(cmd)
	},
}

var webttyIdentityShowCmd = &cobra.Command{
	Use:          "show",
	Short:        "Show a local WebTTY endpoint identity",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYIdentityShow(cmd)
	},
}

var webttyIdentityListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List local WebTTY endpoint identities",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYIdentityList(cmd)
	},
}

var webttyIdentityRemoveCmd = &cobra.Command{
	Use:          "remove name",
	Short:        "Remove a local WebTTY endpoint identity",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYIdentityRemove(cmd, args[0])
	},
}

var webttyKnownServerCmd = &cobra.Command{
	Use:          "known-server",
	Short:        "Manage known WebTTY server endpoint identities",
	GroupID:      "webtty-server",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var webttyKnownServerAddCmd = &cobra.Command{
	Use:          "add target",
	Short:        "Trust a WebTTY server endpoint identity",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYKnownServerAdd(cmd, args[0])
	},
}

var webttyKnownServerListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List known WebTTY server endpoint identities",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYKnownServerList(cmd)
	},
}

var webttyKnownServerSetIdentityCmd = &cobra.Command{
	Use:          "set-identity name",
	Short:        "Associate a WebTTY client identity with a known server",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYKnownServerSetIdentity(cmd, args[0])
	},
}

var webttyKnownServerRemoveCmd = &cobra.Command{
	Use:          "remove name",
	Short:        "Remove a known WebTTY server endpoint identity",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYKnownServerRemove(cmd, args[0])
	},
}

func init() {
	webttyIdentityCreateCmd.Flags().String("name", "", "identity name")
	webttyIdentityCreateCmd.Flags().String("identity-file", "", "local WebTTY endpoint identity file")
	webttyIdentityCreateCmd.Flags().Bool("endpoint-identity", false, "print only the public endpoint identity")
	webttyIdentityCreateCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyIdentityShowCmd.Flags().String("name", "", "identity name")
	webttyIdentityShowCmd.Flags().String("identity-file", "", "local WebTTY endpoint identity file")
	webttyIdentityShowCmd.Flags().Bool("endpoint-identity", false, "print only the public endpoint identity")
	webttyIdentityShowCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyIdentityListCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyIdentityRemoveCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyIdentityCmd.AddCommand(webttyIdentityCreateCmd)
	webttyIdentityCmd.AddCommand(webttyIdentityShowCmd)
	webttyIdentityCmd.AddCommand(webttyIdentityListCmd)
	webttyIdentityCmd.AddCommand(webttyIdentityRemoveCmd)
	webttyKnownServerAddCmd.Flags().String("key", "", "known WebTTY server endpoint identity")
	webttyKnownServerAddCmd.Flags().String("client-identity", "", "local WebTTY client identity to use with this server")
	webttyKnownServerAddCmd.Flags().String("known-servers-file", "", "known WebTTY server endpoint identities file")
	webttyKnownServerAddCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyKnownServerListCmd.Flags().String("known-servers-file", "", "known WebTTY server endpoint identities file")
	webttyKnownServerListCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyKnownServerSetIdentityCmd.Flags().String("identity", "", "local WebTTY client identity")
	webttyKnownServerSetIdentityCmd.Flags().String("known-servers-file", "", "known WebTTY server endpoint identities file")
	webttyKnownServerSetIdentityCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyKnownServerRemoveCmd.Flags().String("known-servers-file", "", "known WebTTY server endpoint identities file")
	webttyKnownServerRemoveCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyKnownServerCmd.AddCommand(webttyKnownServerAddCmd)
	webttyKnownServerCmd.AddCommand(webttyKnownServerListCmd)
	webttyKnownServerCmd.AddCommand(webttyKnownServerSetIdentityCmd)
	webttyKnownServerCmd.AddCommand(webttyKnownServerRemoveCmd)
	webttyCmd.AddCommand(webttyIdentityCmd)
	webttyCmd.AddCommand(webttyKnownServerCmd)
}

func runWebTTYIdentityCreate(cmd *cobra.Command) error {
	identityPath, name, err := webTTYIdentityPathFromFlags(cmd)
	if err != nil {
		return err
	}
	identity, err := webtty.LoadOrCreateWebTTYEndpointIdentityFile(identityPath)
	if err != nil {
		return fmt.Errorf("failed to create WebTTY identity: %w", err)
	}
	return writeWebTTYIdentityOutput(cmd, "created", name, identityPath, identity)
}

func runWebTTYIdentityShow(cmd *cobra.Command) error {
	identityPath, name, err := webTTYIdentityPathFromFlags(cmd)
	if err != nil {
		return err
	}
	identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityPath)
	if err != nil {
		return fmt.Errorf("failed to load WebTTY identity: %w", err)
	}
	return writeWebTTYIdentityOutput(cmd, "loaded", name, identityPath, identity)
}

func runWebTTYIdentityList(cmd *cobra.Command) error {
	items, err := listLocalWebTTYIdentities()
	if err != nil {
		return err
	}
	return writeWebTTYIdentityListOutput(cmd, items)
}

func listLocalWebTTYIdentities() ([]map[string]any, error) {
	identityDir, err := defaultNamedWebTTYIdentityDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(identityDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]map[string]any, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".identity.json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".identity.json")
		if err := validateWebTTYServerID(name); err != nil {
			return nil, fmt.Errorf("identity file %q has an unsupported name", entry.Name())
		}
		identityPath := filepath.Join(identityDir, entry.Name())
		identity, err := webtty.LoadWebTTYEndpointIdentityFile(identityPath)
		if err != nil {
			return nil, err
		}
		items = append(items, webTTYIdentityEntryOutput(name, identityPath, identity))
	}
	sort.Slice(items, func(i int, j int) bool {
		return fmt.Sprint(items[i]["name"]) < fmt.Sprint(items[j]["name"])
	})
	return items, nil
}

func runWebTTYIdentityRemove(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("identity name contains unsupported characters")
	}
	identityPath, err := defaultNamedWebTTYIdentityPath(name)
	if err != nil {
		return err
	}
	if err := webtty.RemoveE2EIdentityFile(identityPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("WebTTY identity %q was not found", name)
		}
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, map[string]any{"removed": name, "identity": identityPath})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed WebTTY identity: %s\n", name)
	return nil
}

func webTTYIdentityPathFromFlags(cmd *cobra.Command) (string, string, error) {
	name, _ := cmd.Flags().GetString("name")
	identityFile, _ := cmd.Flags().GetString("identity-file")
	name = strings.TrimSpace(name)
	identityFile = strings.TrimSpace(identityFile)
	if name == "" && identityFile == "" {
		return "", "", fmt.Errorf("--name or --identity-file is required")
	}
	if name != "" && identityFile != "" {
		return "", "", fmt.Errorf("--name cannot be combined with --identity-file")
	}
	if identityFile != "" {
		path, err := expandWebTTYPath(identityFile)
		return path, filepath.Base(strings.TrimSuffix(path, filepath.Ext(path))), err
	}
	path, err := defaultNamedWebTTYIdentityPath(name)
	return path, name, err
}

func writeWebTTYIdentityOutput(cmd *cobra.Command, status string, name string, identityPath string, identity *webtty.WebTTYEndpointIdentity) error {
	if identity == nil {
		return fmt.Errorf("WebTTY identity is empty")
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	result := webTTYIdentityEntryOutput(name, identityPath, identity)
	result["status"] = status
	endpointOnly, _ := cmd.Flags().GetBool("endpoint-identity")
	if endpointOnly {
		if output != "" && output != "text" {
			return fmt.Errorf("--endpoint-identity cannot be combined with --output %s", output)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", result["endpoint_identity"])
		return nil
	}
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", name)
	fmt.Fprintf(cmd.OutOrStdout(), "Identity: %s\n", identityPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Encryption key ID: %s\n", result["encryption_key_id"])
	fmt.Fprintf(cmd.OutOrStdout(), "Encryption public key: %s\n", result["encryption_public_key"])
	fmt.Fprintf(cmd.OutOrStdout(), "Encryption fingerprint: %s\n", result["encryption_fingerprint"])
	fmt.Fprintf(cmd.OutOrStdout(), "Signing key ID: %s\n", result["signing_key_id"])
	fmt.Fprintf(cmd.OutOrStdout(), "Signing public key: %s\n", result["signing_public_key"])
	fmt.Fprintf(cmd.OutOrStdout(), "Signing fingerprint: %s\n", result["signing_fingerprint"])
	fmt.Fprintf(cmd.OutOrStdout(), "Endpoint identity: %s\n", result["endpoint_identity"])
	return nil
}

func writeWebTTYIdentityListOutput(cmd *cobra.Command, items []map[string]any) error {
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, map[string]any{"identities": items})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No WebTTY identities.")
		return nil
	}
	for _, item := range items {
		fmt.Fprintf(cmd.OutOrStdout(), "%s encryption=%s signing=%s\n", item["name"], item["encryption_key_id"], item["signing_key_id"])
	}
	return nil
}

func webTTYIdentityEntryOutput(name string, identityPath string, identity *webtty.WebTTYEndpointIdentity) map[string]any {
	public := identity.Public()
	return map[string]any{
		"name":                   name,
		"identity":               identityPath,
		"encryption_key_id":      webtty.EncodeE2EKeyMaterial(public.EncryptionKeyID),
		"encryption_public_key":  webtty.EncodeE2EKeyMaterial(public.EncryptionPublicKey),
		"encryption_fingerprint": webTTYServerPublicKeyFingerprint(public.EncryptionPublicKey),
		"signing_key_id":         webtty.EncodeE2EKeyMaterial(public.SigningKeyID),
		"signing_public_key":     webtty.EncodeE2EKeyMaterial(public.SigningPublicKey),
		"signing_fingerprint":    webTTYServerPublicKeyFingerprint(public.SigningPublicKey),
		"endpoint_identity":      webtty.KnownServerEndpointIdentityString(public),
	}
}

func defaultNamedWebTTYIdentityDir() (string, error) {
	path, err := defaultNamedWebTTYIdentityPath("default")
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func runWebTTYKnownServerAdd(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	rawKey, _ := cmd.Flags().GetString("key")
	source, err := parseWebTTYKnownServerSource(rawKey)
	if err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("known server target is required")
	}
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("known server name contains unsupported characters")
	}
	path, err := knownServersPathFromFlags(cmd)
	if err != nil {
		return err
	}
	clientIdentity, _ := cmd.Flags().GetString("client-identity")
	clientIdentity = strings.TrimSpace(clientIdentity)
	if clientIdentity != "" {
		if err := validateWebTTYServerID(clientIdentity); err != nil {
			return fmt.Errorf("--client-identity contains unsupported characters")
		}
	}
	entry := webtty.KnownServerKeyEntry{
		Name:           name,
		KeyID:          webtty.EncodeE2EKeyMaterial(source.Recipient.KeyID),
		PublicKey:      webtty.EncodeE2EKeyMaterial(source.Recipient.PublicKey),
		ClientIdentity: clientIdentity,
		CreatedAt:      time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
	}
	if source.EndpointIdentity != nil {
		entry.SigningKeyID = webtty.EncodeE2EKeyMaterial(source.EndpointIdentity.SigningKeyID)
		entry.SigningPublicKey = webtty.EncodeE2EKeyMaterial(source.EndpointIdentity.SigningPublicKey)
	}
	_, err = webtty.UpdateKnownServerKeysFile(path, func(doc *webtty.KnownServerKeysFile) error {
		replaced := false
		for i := range doc.KnownServers {
			if doc.KnownServers[i].Name != name {
				continue
			}
			if entry.ClientIdentity == "" {
				entry.ClientIdentity = doc.KnownServers[i].ClientIdentity
			}
			doc.KnownServers[i] = entry
			replaced = true
			break
		}
		if !replaced {
			doc.KnownServers = append(doc.KnownServers, entry)
		}
		sort.Slice(doc.KnownServers, func(i int, j int) bool { return doc.KnownServers[i].Name < doc.KnownServers[j].Name })
		return nil
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	result := knownServerEntryOutput(path, entry)
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Known WebTTY server: %s\n", name)
	fmt.Fprintf(cmd.OutOrStdout(), "Encryption key ID: %s\n", entry.KeyID)
	fmt.Fprintf(cmd.OutOrStdout(), "Encryption public key: %s\n", entry.PublicKey)
	if entry.SigningKeyID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Signing key ID: %s\n", entry.SigningKeyID)
		fmt.Fprintf(cmd.OutOrStdout(), "Signing public key: %s\n", entry.SigningPublicKey)
	}
	if entry.ClientIdentity != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Client identity: %s\n", entry.ClientIdentity)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Known WebTTY servers: %s\n", path)
	return nil
}

func runWebTTYKnownServerList(cmd *cobra.Command) error {
	path, err := knownServersPathFromFlags(cmd)
	if err != nil {
		return err
	}
	doc, err := readKnownServersDocument(path)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		items := make([]map[string]any, 0, len(doc.KnownServers))
		for _, entry := range doc.KnownServers {
			items = append(items, knownServerEntryOutput(path, entry))
		}
		return writeStructuredOutput(output, map[string]any{"known_servers": items})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	if len(doc.KnownServers) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No known WebTTY servers.")
		return nil
	}
	for _, entry := range doc.KnownServers {
		if entry.SigningKeyID == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s encryption=%s%s\n", entry.Name, entry.KeyID, knownServerClientIdentitySuffix(entry))
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s encryption=%s signing=%s%s\n", entry.Name, entry.KeyID, entry.SigningKeyID, knownServerClientIdentitySuffix(entry))
		}
	}
	return nil
}

func runWebTTYKnownServerSetIdentity(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("known server name contains unsupported characters")
	}
	clientIdentity, _ := cmd.Flags().GetString("identity")
	clientIdentity = strings.TrimSpace(clientIdentity)
	if clientIdentity == "" {
		return fmt.Errorf("--identity is required")
	}
	if err := validateWebTTYServerID(clientIdentity); err != nil {
		return fmt.Errorf("--identity contains unsupported characters")
	}
	path, err := knownServersPathFromFlags(cmd)
	if err != nil {
		return err
	}
	var updated webtty.KnownServerKeyEntry
	_, err = webtty.UpdateKnownServerKeysFile(path, func(doc *webtty.KnownServerKeysFile) error {
		for i := range doc.KnownServers {
			if doc.KnownServers[i].Name != name {
				continue
			}
			doc.KnownServers[i].ClientIdentity = clientIdentity
			updated = doc.KnownServers[i]
			return nil
		}
		return fmt.Errorf("known WebTTY server %q was not found", name)
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	result := knownServerEntryOutput(path, updated)
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Known WebTTY server: %s\n", name)
	fmt.Fprintf(cmd.OutOrStdout(), "Client identity: %s\n", clientIdentity)
	fmt.Fprintf(cmd.OutOrStdout(), "Known WebTTY servers: %s\n", path)
	return nil
}

func runWebTTYKnownServerRemove(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("known server name contains unsupported characters")
	}
	path, err := knownServersPathFromFlags(cmd)
	if err != nil {
		return err
	}
	_, err = webtty.UpdateKnownServerKeysFile(path, func(doc *webtty.KnownServerKeysFile) error {
		next := doc.KnownServers[:0]
		removed := false
		for _, entry := range doc.KnownServers {
			if entry.Name == name {
				removed = true
				continue
			}
			next = append(next, entry)
		}
		if !removed {
			return fmt.Errorf("known WebTTY server %q was not found", name)
		}
		doc.KnownServers = next
		return nil
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, map[string]any{"removed": name, "known_servers": path})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed known server: %s\n", name)
	return nil
}

func knownServersPathFromFlags(cmd *cobra.Command) (string, error) {
	value, _ := cmd.Flags().GetString("known-servers-file")
	value = strings.TrimSpace(value)
	if value == "" {
		return webtty.DefaultKnownServerKeysPath()
	}
	return expandWebTTYPath(value)
}

func readKnownServersDocument(path string) (*webtty.KnownServerKeysFile, error) {
	doc, err := webtty.ReadKnownServerKeysFile(path)
	if err == nil {
		return doc, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return &webtty.KnownServerKeysFile{Version: webtty.E2EIdentityFileVersion, CryptoSuite: webtty.E2EKeyFileCryptoSuite}, nil
	}
	return nil, err
}

func knownServerEntryOutput(path string, entry webtty.KnownServerKeyEntry) map[string]any {
	return map[string]any{
		"name":                  entry.Name,
		"encryption_key_id":     entry.KeyID,
		"encryption_public_key": entry.PublicKey,
		"signing_key_id":        entry.SigningKeyID,
		"signing_public_key":    entry.SigningPublicKey,
		"client_identity":       entry.ClientIdentity,
		"known_servers":         path,
		"created_at":            entry.CreatedAt,
	}
}

func knownServerClientIdentitySuffix(entry webtty.KnownServerKeyEntry) string {
	if strings.TrimSpace(entry.ClientIdentity) == "" {
		return ""
	}
	return " client_identity=" + entry.ClientIdentity
}

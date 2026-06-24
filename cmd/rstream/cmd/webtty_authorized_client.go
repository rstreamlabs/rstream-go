// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

var webttyAuthorizedClientCmd = &cobra.Command{
	Use:          "authorized-client",
	Short:        "Manage WebTTY clients authorized by a server identity",
	GroupID:      "webtty-server",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var webttyAuthorizedClientAddCmd = &cobra.Command{
	Use:          "add [name]",
	Short:        "Authorize a WebTTY client endpoint identity",
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return runWebTTYAuthorizedClientAdd(cmd, name)
	},
}

var webttyAuthorizedClientListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List WebTTY clients authorized by a server identity",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYAuthorizedClientList(cmd)
	},
}

var webttyAuthorizedClientRemoveCmd = &cobra.Command{
	Use:          "remove name",
	Short:        "Remove an authorized WebTTY client",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYAuthorizedClientRemove(cmd, args[0])
	},
}

func init() {
	for _, cmd := range []*cobra.Command{webttyAuthorizedClientAddCmd, webttyAuthorizedClientListCmd, webttyAuthorizedClientRemoveCmd} {
		cmd.Flags().String("identity", "", "named local WebTTY server identity")
		cmd.Flags().String("identity-file", "", "local WebTTY server identity file")
		cmd.Flags().String("server-id", "", "registered WebTTY server ID")
		cmd.Flags().String("authorized-clients-file", "", "authorized WebTTY client keys file")
		cmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	}
	webttyAuthorizedClientAddCmd.Flags().String("key", "", "client endpoint identity or signing key")
	webttyAuthorizedClientCmd.AddCommand(webttyAuthorizedClientAddCmd)
	webttyAuthorizedClientCmd.AddCommand(webttyAuthorizedClientListCmd)
	webttyAuthorizedClientCmd.AddCommand(webttyAuthorizedClientRemoveCmd)
	webttyCmd.AddCommand(webttyAuthorizedClientCmd)
}

func runWebTTYAuthorizedClientAdd(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	rawKey, _ := cmd.Flags().GetString("key")
	if name == "" {
		derivedName, err := deriveAuthorizedClientName(rawKey)
		if err != nil {
			return err
		}
		name = derivedName
	}
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("authorized client name contains unsupported characters")
	}
	entry, err := webtty.NewAuthorizedClientKeyEntry(name, rawKey)
	if err != nil {
		return err
	}
	path, err := authorizedClientsPathFromCommandFlags(cmd)
	if err != nil {
		return err
	}
	_, err = webtty.UpdateAuthorizedClientKeysFile(path, func(doc *webtty.AuthorizedClientKeysFile) error {
		replaced := false
		for i := range doc.AuthorizedClients {
			if doc.AuthorizedClients[i].Name != name {
				continue
			}
			doc.AuthorizedClients[i] = entry
			replaced = true
			break
		}
		if !replaced {
			doc.AuthorizedClients = append(doc.AuthorizedClients, entry)
		}
		return nil
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	result := authorizedClientEntryOutput(path, entry)
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Authorized WebTTY client: %s\n", name)
	fmt.Fprintf(cmd.OutOrStdout(), "Signing key ID: %s\n", entry.SigningKeyID)
	fmt.Fprintf(cmd.OutOrStdout(), "Signing public key: %s\n", entry.SigningPublicKey)
	fmt.Fprintf(cmd.OutOrStdout(), "Authorized clients: %s\n", path)
	return nil
}

func deriveAuthorizedClientName(rawKey string) (string, error) {
	keyID, _, err := webtty.ParseAuthorizedClientSigningKey(rawKey)
	if err != nil {
		return "", err
	}
	encoded := webtty.EncodeE2EKeyMaterial(keyID)
	if len(encoded) > 12 {
		encoded = encoded[:12]
	}
	return "client-" + encoded, nil
}

func runWebTTYAuthorizedClientList(cmd *cobra.Command) error {
	path, err := authorizedClientsPathFromCommandFlags(cmd)
	if err != nil {
		return err
	}
	doc, err := readAuthorizedClientsDocument(path)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		items := make([]map[string]any, 0, len(doc.AuthorizedClients))
		for _, entry := range doc.AuthorizedClients {
			items = append(items, authorizedClientEntryOutput(path, entry))
		}
		return writeStructuredOutput(output, map[string]any{"authorized_clients": items})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	if len(doc.AuthorizedClients) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No authorized WebTTY clients.")
		return nil
	}
	for _, entry := range doc.AuthorizedClients {
		fmt.Fprintf(cmd.OutOrStdout(), "%s signing=%s\n", entry.Name, entry.SigningKeyID)
	}
	return nil
}

func runWebTTYAuthorizedClientRemove(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	if err := validateWebTTYServerID(name); err != nil {
		return fmt.Errorf("authorized client name contains unsupported characters")
	}
	path, err := authorizedClientsPathFromCommandFlags(cmd)
	if err != nil {
		return err
	}
	_, err = webtty.UpdateAuthorizedClientKeysFile(path, func(doc *webtty.AuthorizedClientKeysFile) error {
		next := doc.AuthorizedClients[:0]
		removed := false
		for _, entry := range doc.AuthorizedClients {
			if entry.Name == name {
				removed = true
				continue
			}
			next = append(next, entry)
		}
		if !removed {
			return fmt.Errorf("authorized WebTTY client %q was not found", name)
		}
		doc.AuthorizedClients = next
		return nil
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, map[string]any{"removed": name, "authorized_clients": path})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed authorized client: %s\n", name)
	return nil
}

func authorizedClientsPathFromCommandFlags(cmd *cobra.Command) (string, error) {
	return webTTYAuthorizedClientsPathFromFlags(cmd, nil)
}

func readAuthorizedClientsDocument(path string) (*webtty.AuthorizedClientKeysFile, error) {
	doc, err := webtty.ReadAuthorizedClientKeysFile(path)
	if err == nil {
		return doc, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return &webtty.AuthorizedClientKeysFile{Version: webtty.E2EIdentityFileVersion, CryptoSuite: webtty.E2EKeyFileCryptoSuite}, nil
	}
	return nil, err
}

func authorizedClientEntryOutput(path string, entry webtty.AuthorizedClientKeyEntry) map[string]any {
	return map[string]any{
		"name":               entry.Name,
		"signing_key_id":     entry.SigningKeyID,
		"signing_public_key": entry.SigningPublicKey,
		"authorized_clients": path,
		"created_at":         entry.CreatedAt,
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

const webTTYClientCredentialFileEnv = "RSTREAM_WEBTTY_CLIENT_CREDENTIAL_FILE"
const webTTYMaxClientCredentialBytes = 64 * 1024

var webttyClientPrepareCmd = &cobra.Command{
	Use:          "prepare <server-id>",
	Short:        "Prepare local workspace E2E credentials for engine-only connections",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYClientPrepare(cmd, args[0])
	},
}

func init() {
	addWebTTYRegisteredServerProjectFlag(webttyClientPrepareCmd)
	webttyClientPrepareCmd.Flags().String("directory", "", "new private directory for the client identity, credential and server trust files")
	webttyClientPrepareCmd.MarkFlagRequired("directory")
	webttyClientCmd.AddCommand(webttyClientPrepareCmd)
}

func runWebTTYClientPrepare(cmd *cobra.Command, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return fmt.Errorf("server ID is required")
	}
	directory, _ := cmd.Flags().GetString("directory")
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("preparation directory is required")
	}
	directory, err := expandWebTTYPath(directory)
	if err != nil {
		return err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(directory); err == nil {
		return fmt.Errorf("WebTTY preparation directory already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	_, client, project, err := webTTYRegisteredServerControlPlane(cmd)
	if err != nil {
		return err
	}
	workspaceID, err := webTTYRegisteredServerProjectWorkspaceID(cmd, client, project)
	if err != nil {
		return err
	}
	sources, identity, credential, _, err := webTTYClientRuntimeE2EServerSources(cmd.Context(), &webTTYClientRuntimeE2EContext{
		controlClient: client,
		project:       controlplane.Project{ID: project.ID, WorkspaceID: workspaceID},
		serverID:      serverID,
	})
	if err != nil {
		return err
	}
	if identity == nil || len(credential) == 0 || len(sources) == 0 || sources[0].EndpointIdentity == nil {
		return fmt.Errorf("client preparation requires a workspace-managed server and an approved local workspace device")
	}
	files, err := writeWebTTYClientPreparation(directory, serverID, *identity, *sources[0].EndpointIdentity, credential)
	if err != nil {
		return err
	}
	return writeStructuredOutput("json", map[string]any{
		"workspace_id": workspaceID,
		"project_id":   project.ID,
		"server_id":    serverID,
		"files":        files,
	})
}

func writeWebTTYClientPreparation(directory string, serverID string, identity webtty.WebTTYEndpointIdentity, server webtty.WebTTYEndpointIdentityPublic, credential []byte) (map[string]string, error) {
	if len(credential) > webTTYMaxClientCredentialBytes {
		return nil, fmt.Errorf("WebTTY client credential exceeds %d bytes", webTTYMaxClientCredentialBytes)
	}
	if _, err := decodeWebTTYWorkspaceClientCredential(credential); err != nil {
		return nil, fmt.Errorf("invalid WebTTY client credential")
	}
	identityJSON, err := webtty.EncodeWebTTYEndpointIdentityJSON(identity)
	if err != nil {
		return nil, err
	}
	if _, err := webtty.ParseKnownServerEndpointIdentity(webtty.KnownServerEndpointIdentityString(server)); err != nil {
		return nil, err
	}
	trustJSON, err := json.MarshalIndent(webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: webtty.E2EKeyFileCryptoSuite,
		KnownServers: []webtty.KnownServerKeyEntry{{
			Name:             serverID,
			KeyID:            webtty.EncodeE2EKeyMaterial(server.EncryptionKeyID),
			PublicKey:        webtty.EncodeE2EKeyMaterial(server.EncryptionPublicKey),
			SigningKeyID:     webtty.EncodeE2EKeyMaterial(server.SigningKeyID),
			SigningPublicKey: webtty.EncodeE2EKeyMaterial(server.SigningPublicKey),
		}},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create WebTTY preparation directory: %w", err)
	}
	complete := false
	written := []string{}
	defer func() {
		if complete {
			return
		}
		for _, path := range written {
			_ = os.Remove(path)
		}
		_ = os.Remove(directory)
	}()
	files := map[string]string{}
	for _, item := range []struct {
		name string
		data []byte
	}{
		{"identity.json", identityJSON},
		{"known-servers.json", trustJSON},
		{"client-credential.json", credential},
	} {
		path := filepath.Join(directory, item.name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		written = append(written, path)
		_, writeErr := file.Write(item.data)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files[item.name] = path
	}
	complete = true
	return files, nil
}

func withWebTTYClientCredential(config webTTYClientCryptoConfig, path string) (webTTYClientCryptoConfig, error) {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv(webTTYClientCredentialFileEnv)
	}
	if strings.TrimSpace(path) == "" {
		return config, nil
	}
	if config.ExpectedServerIdentity == nil || config.PayloadCrypto == nil {
		return webTTYClientCryptoConfig{}, fmt.Errorf("a WebTTY client credential requires authenticated E2E with a locally trusted server endpoint identity")
	}
	path, err := expandWebTTYPath(path)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return webTTYClientCryptoConfig{}, fmt.Errorf("open WebTTY client credential: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, webTTYMaxClientCredentialBytes+1))
	if err != nil {
		return webTTYClientCryptoConfig{}, fmt.Errorf("read WebTTY client credential: %w", err)
	}
	if len(raw) > webTTYMaxClientCredentialBytes {
		return webTTYClientCryptoConfig{}, fmt.Errorf("WebTTY client credential exceeds %d bytes", webTTYMaxClientCredentialBytes)
	}
	raw = bytes.TrimSpace(raw)
	if _, err := decodeWebTTYWorkspaceClientCredential(raw); err != nil {
		return webTTYClientCryptoConfig{}, fmt.Errorf("invalid WebTTY client credential file")
	}
	config.ClientCredential = raw
	return config, nil
}

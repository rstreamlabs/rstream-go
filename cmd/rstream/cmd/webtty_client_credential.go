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
	webttyClientPrepareCmd.Flags().String("for-token", "", "workspace token ID to prepare using owner or admin access")
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
	var forCredentialID string
	if cmd.Flags().Changed("for-token") {
		caller, err := client.Identity(cmd.Context())
		if err != nil {
			return mapControlPlaneError(err)
		}
		forCredentialID, err = workspaceDeviceForToken(cmd, caller)
		if err != nil {
			return err
		}
	}
	sources, identity, credential, _, err := webTTYClientRuntimeE2EServerSources(cmd.Context(), &webTTYClientRuntimeE2EContext{
		controlClient:   client,
		project:         controlplane.Project{ID: project.ID, WorkspaceID: workspaceID},
		serverID:        serverID,
		forCredentialID: forCredentialID,
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
	result := map[string]any{
		"workspace_id": workspaceID,
		"project_id":   project.ID,
		"server_id":    serverID,
		"files":        files,
	}
	if forCredentialID != "" {
		result["for_token"] = forCredentialID
	}
	return writeStructuredOutput("json", result)
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
	cryptoSuite, err := webtty.E2EKeyFileCryptoSuiteForSuite(server.KeyEnvelopeSuite)
	if err != nil {
		return nil, err
	}
	trustJSON, err := json.MarshalIndent(webtty.KnownServerKeysFile{
		Version:     webtty.E2EIdentityFileVersion,
		CryptoSuite: cryptoSuite,
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
	raw, envelope, err := readWebTTYClientCredentialFile(path)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	if raw == nil {
		return config, nil
	}
	if config.ExpectedServerIdentity == nil || config.PayloadCrypto == nil {
		return webTTYClientCryptoConfig{}, fmt.Errorf("a WebTTY client credential requires authenticated E2E with a locally trusted server endpoint identity")
	}
	if config.payloadConfig != nil {
		config, err = withWebTTYWorkspaceRecordingRecipient(config, envelope)
		if err != nil {
			return webTTYClientCryptoConfig{}, err
		}
	}
	config.ClientCredential = raw
	return config, nil
}

func readWebTTYClientCredentialFile(path string) ([]byte, webTTYWorkspaceClientCredentialEnvelope, error) {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv(webTTYClientCredentialFileEnv)
	}
	if strings.TrimSpace(path) == "" {
		return nil, webTTYWorkspaceClientCredentialEnvelope{}, nil
	}
	path, err := expandWebTTYPath(path)
	if err != nil {
		return nil, webTTYWorkspaceClientCredentialEnvelope{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("open WebTTY client credential: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, webTTYMaxClientCredentialBytes+1))
	if err != nil {
		return nil, webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("read WebTTY client credential: %w", err)
	}
	if len(raw) > webTTYMaxClientCredentialBytes {
		return nil, webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("WebTTY client credential exceeds %d bytes", webTTYMaxClientCredentialBytes)
	}
	raw = bytes.TrimSpace(raw)
	envelope, err := decodeWebTTYWorkspaceClientCredential(raw)
	if err != nil {
		return nil, webTTYWorkspaceClientCredentialEnvelope{}, fmt.Errorf("invalid WebTTY client credential file")
	}
	return raw, envelope, nil
}

func withWebTTYWorkspaceRecordingRecipient(config webTTYClientCryptoConfig, credential webTTYWorkspaceClientCredentialEnvelope) (webTTYClientCryptoConfig, error) {
	payloadConfig, err := webTTYWorkspaceRecordingConfig(*config.payloadConfig, credential, config.EndpointIdentity)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	config.PayloadCrypto, err = webtty.NewE2EClientPayloadCrypto(payloadConfig)
	if err != nil {
		return webTTYClientCryptoConfig{}, err
	}
	config.payloadConfig = &payloadConfig
	return config, nil
}

func webTTYWorkspaceRecordingConfig(payloadConfig webtty.E2EPayloadCryptoConfig, credential webTTYWorkspaceClientCredentialEnvelope, identity *webtty.WebTTYEndpointIdentity) (webtty.E2EPayloadCryptoConfig, error) {
	if identity == nil {
		return webtty.E2EPayloadCryptoConfig{}, fmt.Errorf("a WebTTY client credential requires its approved endpoint identity")
	}
	payload := credential.Payload
	if workspaceTrustString(payload, "webtty_key_id") != webtty.EncodeE2EKeyMaterial(identity.Encryption.KeyID) ||
		workspaceTrustString(payload, "webtty_public_key") != webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey) ||
		workspaceTrustString(payload, "client_signing_key_id") != webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID) ||
		workspaceTrustString(payload, "client_signing_public_key") != webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey) {
		return webtty.E2EPayloadCryptoConfig{}, fmt.Errorf("WebTTY client credential does not match the approved endpoint identity")
	}
	deviceID := workspaceTrustString(payload, "device_key_id")
	workspaceID := workspaceTrustString(payload, "workspace_id")
	projectID := workspaceTrustString(payload, "project_id")
	serverID := workspaceTrustString(payload, "server_id")
	if deviceID == "" || workspaceID == "" || projectID == "" || serverID == "" {
		return webtty.E2EPayloadCryptoConfig{}, fmt.Errorf("WebTTY client credential is missing its recording scope")
	}
	// This describes the encrypted recording recipient; it grants no authority.
	// Engine and the server still verify the signed credential on admission.
	if payloadConfig.WorkspaceID != "" && payloadConfig.WorkspaceID != workspaceID ||
		payloadConfig.ProjectID != "" && payloadConfig.ProjectID != projectID ||
		payloadConfig.ServerID != "" && payloadConfig.ServerID != serverID {
		return webtty.E2EPayloadCryptoConfig{}, fmt.Errorf("WebTTY client credential does not match the resolved recording scope")
	}
	payloadConfig.WorkspaceID, payloadConfig.ProjectID, payloadConfig.ServerID = workspaceID, projectID, serverID
	payloadConfig.Recipients = append([]webtty.E2ERecipient(nil), payloadConfig.Recipients...)
	var err error
	payloadConfig.Recipients, err = appendWebTTYE2EServerKey(payloadConfig.Recipients, webtty.E2ERecipient{
		ID: deviceID, Kind: webtty.E2ERecipientKindWorkspaceDevice,
		KeyEnvelopeSuite: identity.Encryption.KeyEnvelopeSuite,
		KeyID:            identity.Encryption.KeyID, PublicKey: identity.Encryption.PublicKey,
	})
	if err != nil {
		return webtty.E2EPayloadCryptoConfig{}, err
	}
	for index := range payloadConfig.Recipients {
		if bytes.Equal(payloadConfig.Recipients[index].KeyID, identity.Encryption.KeyID) {
			payloadConfig.Recipients[index].ID = deviceID
			payloadConfig.Recipients[index].Kind = webtty.E2ERecipientKindWorkspaceDevice
		}
	}
	return payloadConfig, nil
}

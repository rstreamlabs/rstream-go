// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	webTTYServerEnrollmentVersion                = 1
	webTTYServerEncryptionPolicyDisabled         = "disabled"
	webTTYServerEncryptionPolicyExplicitKey      = "explicit_key"
	webTTYServerEncryptionPolicyWorkspaceManaged = "workspace_managed"
	webTTYServerKeyAlgorithmX25519               = "webtty-x25519-hpke-v1"
	webTTYServerEnrollmentStatusOK               = "enrolled"
)

var webTTYServerIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type webTTYServerEnrollmentFile struct {
	Version                         int       `yaml:"version"`
	ServerID                        string    `yaml:"serverId"`
	ServerName                      string    `yaml:"serverName,omitempty"`
	WorkspaceID                     string    `yaml:"workspaceId,omitempty"`
	ProjectID                       string    `yaml:"projectId"`
	APIURL                          string    `yaml:"apiUrl,omitempty"`
	IdentityFile                    string    `yaml:"identityFile"`
	ServerPublicKey                 string    `yaml:"serverPublicKey"`
	ServerSigningKeyID              string    `yaml:"serverSigningKeyId"`
	ServerSigningPublicKey          string    `yaml:"serverSigningPublicKey"`
	ServerFingerprint               string    `yaml:"serverFingerprint"`
	ServerKeyAlgorithm              string    `yaml:"serverKeyAlgorithm"`
	WorkspaceTrustKeysetID          string    `yaml:"workspaceTrustKeysetId,omitempty"`
	WorkspaceTrustKeysetFingerprint string    `yaml:"workspaceTrustKeysetFingerprint,omitempty"`
	WorkspaceTrustPublicSigningKey  string    `yaml:"workspaceTrustPublicSigningKey,omitempty"`
	EncryptionPolicy                string    `yaml:"encryptionPolicy,omitempty"`
	EnrollmentStatus                string    `yaml:"enrollmentStatus"`
	EnrolledAt                      time.Time `yaml:"enrolledAt"`
}

type webTTYServerEnrollOptions struct {
	ProjectID        string
	ServerID         string
	IdentityPath     string
	EnrollmentPath   string
	EnrollmentStatus string
	WorkspaceTrust   *workspaceServerTrustMaterial
}

type workspaceServerTrustMaterial struct {
	device   workspaceDeviceFile
	envelope controlplane.WorkspaceKeyEnvelope
	bundle   *workspacePrivateBundle
}

var webttyServerEnrollCmd = &cobra.Command{
	Use:          "enroll <server-id>",
	Short:        "Enroll a registered WebTTY server",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYServerEnroll(cmd, args[0])
	},
}

func init() {
	webttyServerEnrollCmd.Flags().SortFlags = false
	webttyServerEnrollCmd.PersistentFlags().SortFlags = false
	webttyServerEnrollCmd.Flags().String("project-id", "", "tunnels project ID that owns the WebTTY server")
	webttyServerEnrollCmd.Flags().String("identity-file", "", "local WebTTY server identity file")
	webttyServerEnrollCmd.Flags().String("server-enrollment", "", "local registered WebTTY server enrollment file")
	webttyServerEnrollCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	webttyServerCmd.AddCommand(webttyServerEnrollCmd)
}

func runWebTTYServerEnroll(cmd *cobra.Command, serverID string) error {
	ctx := cmd.Context()
	identityPath, _ := cmd.Flags().GetString("identity-file")
	enrollmentPath, err := webTTYServerEnrollmentPathFromFlags(cmd)
	if err != nil {
		return err
	}
	serverID = strings.TrimSpace(serverID)
	if err := validateWebTTYServerID(serverID); err != nil {
		return err
	}
	runtime, client, project, err := webTTYRegisteredServerControlPlane(cmd)
	if err != nil {
		return err
	}
	enrollment, enrollmentPath, err := enrollWebTTYServer(ctx, runtime, client, webTTYServerEnrollOptions{
		ProjectID:      project.ID,
		ServerID:       serverID,
		IdentityPath:   identityPath,
		EnrollmentPath: enrollmentPath,
	})
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, map[string]any{
			"server_id":          enrollment.ServerID,
			"project_id":         enrollment.ProjectID,
			"workspace_id":       enrollment.WorkspaceID,
			"encryption_policy":  enrollment.EncryptionPolicy,
			"workspace_trust":    webTTYServerEnrollmentWorkspaceTrustStatus(&enrollment),
			"server_enrollment":  enrollmentPath,
			"identity_file":      enrollment.IdentityFile,
			"server_fingerprint": enrollment.ServerFingerprint,
		})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "WebTTY server enrolled\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Server ID: %s\n", enrollment.ServerID)
	if enrollment.EncryptionPolicy != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Encryption policy: %s\n", enrollment.EncryptionPolicy)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Fingerprint: %s\n", enrollment.ServerFingerprint)
	fmt.Fprintf(cmd.OutOrStdout(), "Enrollment: %s\n", enrollmentPath)
	printWebTTYServerEnrollmentWorkspaceTrust(cmd.OutOrStdout(), &enrollment)
	return nil
}

func enrollWebTTYServer(ctx context.Context, runtime *resolvedRuntime, client *controlplane.Client, options webTTYServerEnrollOptions) (webTTYServerEnrollmentFile, string, error) {
	projectID := strings.TrimSpace(options.ProjectID)
	serverID := strings.TrimSpace(options.ServerID)
	identityPath := strings.TrimSpace(options.IdentityPath)
	enrollmentPath := strings.TrimSpace(options.EnrollmentPath)
	if runtime == nil {
		return webTTYServerEnrollmentFile{}, "", fmt.Errorf("Control plane runtime is required")
	}
	if client == nil {
		return webTTYServerEnrollmentFile{}, "", fmt.Errorf("Control plane client is required")
	}
	if projectID == "" {
		return webTTYServerEnrollmentFile{}, "", fmt.Errorf("--project-id is required")
	}
	if err := validateWebTTYServerID(serverID); err != nil {
		return webTTYServerEnrollmentFile{}, "", err
	}
	if identityPath == "" {
		var err error
		identityPath, err = defaultWebTTYServerIdentityPath(serverID)
		if err != nil {
			return webTTYServerEnrollmentFile{}, "", err
		}
	}
	identity, err := webtty.LoadOrCreateWebTTYEndpointIdentityFile(identityPath)
	if err != nil {
		return webTTYServerEnrollmentFile{}, "", fmt.Errorf("failed to load WebTTY server identity: %w", err)
	}
	publicKey := webtty.EncodeE2EKeyMaterial(identity.Encryption.PublicKey)
	signingKeyID := webtty.EncodeE2EKeyMaterial(identity.Signing.KeyID)
	signingPublicKey := webtty.EncodeE2EKeyMaterial(identity.Signing.PublicKey)
	fingerprint := webTTYServerPublicKeyFingerprint(identity.Encryption.PublicKey)
	registeredServer, err := client.GetWebTTYServer(ctx, projectID, serverID)
	if err != nil {
		return webTTYServerEnrollmentFile{}, "", err
	}
	if strings.TrimSpace(registeredServer.ProjectID) != "" && registeredServer.ProjectID != projectID {
		return webTTYServerEnrollmentFile{}, "", fmt.Errorf("WebTTY server %s belongs to project %s, but the current command targets project %s", serverID, registeredServer.ProjectID, projectID)
	}
	workspaceTrust := options.WorkspaceTrust
	if registeredServer.EncryptionPolicy == webTTYServerEncryptionPolicyWorkspaceManaged {
		if workspaceTrust == nil {
			material, err := workspaceTrustedDeviceForServerTrust(ctx, client, registeredServer.WorkspaceID)
			if err != nil {
				return webTTYServerEnrollmentFile{}, "", fmt.Errorf("workspace-managed WebTTY servers require this machine to be a trusted workspace device before enrollment; %s: %w", workspaceDeviceEnrollmentHint(registeredServer.WorkspaceID), err)
			}
			workspaceTrust = &material
		}
	}
	server, err := client.EnrollWebTTYServer(ctx, projectID, serverID, controlplane.EnrollWebTTYServerRequest{
		ServerPublicKey:        publicKey,
		ServerSigningKeyID:     signingKeyID,
		ServerSigningPublicKey: signingPublicKey,
		ServerFingerprint:      fingerprint,
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		Capabilities:           defaultWebTTYServerControlPlaneCapabilities(),
	})
	if err != nil {
		return webTTYServerEnrollmentFile{}, "", err
	}
	if enrollmentPath == "" {
		enrollmentPath, err = defaultWebTTYServerEnrollmentPath(serverID)
		if err != nil {
			return webTTYServerEnrollmentFile{}, "", err
		}
	}
	enrollmentStatus := strings.TrimSpace(options.EnrollmentStatus)
	if enrollmentStatus == "" {
		enrollmentStatus = webTTYServerEnrollmentStatusOK
	}
	enrollment := webTTYServerEnrollmentFile{
		Version:                webTTYServerEnrollmentVersion,
		ServerID:               serverID,
		ServerName:             registeredServer.Name,
		WorkspaceID:            server.WorkspaceID,
		ProjectID:              projectID,
		APIURL:                 runtime.Resolved.APIURL,
		IdentityFile:           identityPath,
		ServerPublicKey:        publicKey,
		ServerSigningKeyID:     signingKeyID,
		ServerSigningPublicKey: signingPublicKey,
		ServerFingerprint:      fingerprint,
		ServerKeyAlgorithm:     webTTYServerKeyAlgorithmX25519,
		EncryptionPolicy:       server.EncryptionPolicy,
		EnrollmentStatus:       enrollmentStatus,
		EnrolledAt:             time.Now().UTC().Truncate(time.Second),
	}
	if err := writeWebTTYServerEnrollmentFile(enrollmentPath, enrollment); err != nil {
		return webTTYServerEnrollmentFile{}, "", err
	}
	if err := maybeApproveWorkspaceManagedWebTTYServerTrust(ctx, runtime, client, enrollmentPath, &enrollment, workspaceTrust); err != nil {
		return webTTYServerEnrollmentFile{}, "", err
	}
	return enrollment, enrollmentPath, nil
}

func maybeApproveWorkspaceManagedWebTTYServerTrust(ctx context.Context, runtime *resolvedRuntime, client *controlplane.Client, enrollmentPath string, enrollment *webTTYServerEnrollmentFile, trust *workspaceServerTrustMaterial) error {
	if enrollment == nil || enrollment.EncryptionPolicy != webTTYServerEncryptionPolicyWorkspaceManaged {
		return nil
	}
	if runtime == nil || strings.TrimSpace(runtime.Resolved.Token) == "" {
		return fmt.Errorf("workspace-managed WebTTY servers require an authenticated rstream login")
	}
	if trust == nil {
		material, err := workspaceTrustedDeviceForServerTrust(ctx, client, enrollment.WorkspaceID)
		if err != nil {
			return fmt.Errorf("workspace-managed WebTTY servers require this machine to be a trusted workspace device before enrollment; %s: %w", workspaceDeviceEnrollmentHint(enrollment.WorkspaceID), err)
		}
		trust = &material
	}
	deviceSigningKey, err := parseWorkspaceDeviceSigningKey(trust.device)
	if err != nil {
		return err
	}
	if trust.bundle == nil {
		return fmt.Errorf("workspace private bundle is required")
	}
	keysetSigningKey, err := workspaceP256SigningKeyFromJWK(trust.bundle.SigningPrivateKeyJWK)
	if err != nil {
		return err
	}
	if err := validateWorkspacePrivateBundle(trust.bundle, trust.envelope.KeysetID); err != nil {
		return err
	}
	signedAt := workspaceSignedAtNow()
	payload := workspaceWebTTYServerTrustApprovalPayload(enrollment, trust.envelope.KeysetID, trust.device.DeviceKeyID, signedAt)
	actorSignature, err := signWorkspacePayload(deviceSigningKey, payload)
	if err != nil {
		return err
	}
	keysetSignature, err := signWorkspacePayload(keysetSigningKey, payload)
	if err != nil {
		return err
	}
	_, err = client.ApproveWebTTYServerWorkspaceTrust(ctx, enrollment.ProjectID, enrollment.ServerID, controlplane.ApproveWebTTYServerWorkspaceTrustRequest{
		ActorDeviceKeyID: trust.device.DeviceKeyID,
		KeysetID:         trust.envelope.KeysetID,
		SignedAt:         signedAt,
		ActorSignature:   actorSignature,
		KeysetSignature:  keysetSignature,
	})
	if err != nil {
		return err
	}
	enrollment.WorkspaceTrustKeysetID = trust.envelope.KeysetID
	enrollment.WorkspaceTrustKeysetFingerprint = trust.bundle.Fingerprint
	enrollment.WorkspaceTrustPublicSigningKey = trust.bundle.PublicSigningKey
	return writeWebTTYServerEnrollmentFile(enrollmentPath, *enrollment)
}

func workspaceTrustedDeviceForServerTrust(ctx context.Context, client *controlplane.Client, workspaceID string) (workspaceServerTrustMaterial, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return workspaceServerTrustMaterial{}, fmt.Errorf("workspace ID is required")
	}
	devices, err := loadWorkspaceDeviceFiles(workspaceID)
	if err != nil {
		return workspaceServerTrustMaterial{}, err
	}
	var lastErr error
	for _, item := range devices {
		device, _, err := refreshWorkspaceDeviceStatusContext(ctx, client, item.path, item.device)
		if err != nil {
			lastErr = fmt.Errorf("refresh workspace device %s: %w", item.device.DeviceKeyID, err)
			continue
		}
		if device.Status != workspaceDeviceStatusActive || device.DeviceEnvelope == nil {
			lastErr = fmt.Errorf("workspace device %s is %s and has key envelope: %t", device.DeviceKeyID, device.Status, device.DeviceEnvelope != nil)
			continue
		}
		bundle, err := decryptWorkspaceKeyEnvelope(device, *device.DeviceEnvelope)
		if err != nil {
			lastErr = fmt.Errorf("decrypt workspace key envelope for device %s: %w", device.DeviceKeyID, err)
			continue
		}
		return workspaceServerTrustMaterial{
			device:   device,
			envelope: *device.DeviceEnvelope,
			bundle:   bundle,
		}, nil
	}
	if lastErr != nil {
		return workspaceServerTrustMaterial{}, lastErr
	}
	return workspaceServerTrustMaterial{}, fmt.Errorf("no active trusted workspace device can approve WebTTY server trust")
}

func validateWorkspacePrivateBundle(bundle *workspacePrivateBundle, keysetID string) error {
	if bundle == nil {
		return fmt.Errorf("workspace private bundle is required")
	}
	if strings.TrimSpace(keysetID) == "" {
		return fmt.Errorf("workspace keyset ID is required")
	}
	fingerprint, err := workspacePublicKeyFingerprint(bundle.PublicEncryptionKey, bundle.PublicSigningKey)
	if err != nil {
		return err
	}
	if fingerprint != bundle.Fingerprint {
		return fmt.Errorf("workspace private bundle fingerprint does not match public keys")
	}
	signingKey, err := workspaceP256SigningKeyFromJWK(bundle.SigningPrivateKeyJWK)
	if err != nil {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&signingKey.PublicKey)
	if err != nil {
		return err
	}
	if workspaceBase64URL(publicDER) != bundle.PublicSigningKey {
		return fmt.Errorf("workspace private bundle signing key does not match public signing key")
	}
	return nil
}

func workspaceWebTTYServerTrustApprovalPayload(enrollment *webTTYServerEnrollmentFile, keysetID string, actorDeviceKeyID string, signedAt string) map[string]any {
	return map[string]any{
		"v":                         1,
		"type":                      "workspace.webtty.server.trust",
		"workspace_id":              enrollment.WorkspaceID,
		"project_id":                enrollment.ProjectID,
		"server_id":                 enrollment.ServerID,
		"server_public_key":         enrollment.ServerPublicKey,
		"server_signing_key_id":     enrollment.ServerSigningKeyID,
		"server_signing_public_key": enrollment.ServerSigningPublicKey,
		"server_fingerprint":        enrollment.ServerFingerprint,
		"server_key_algorithm":      enrollment.ServerKeyAlgorithm,
		"encryption_policy":         webTTYServerEncryptionPolicyWorkspaceManaged,
		"keyset_id":                 keysetID,
		"actor_device_key_id":       actorDeviceKeyID,
		"signed_at":                 signedAt,
	}
}

func webTTYServerEnrollmentWorkspaceTrustStatus(enrollment *webTTYServerEnrollmentFile) string {
	if enrollment == nil || enrollment.EncryptionPolicy != webTTYServerEncryptionPolicyWorkspaceManaged {
		return ""
	}
	if strings.TrimSpace(enrollment.WorkspaceTrustKeysetID) != "" &&
		strings.TrimSpace(enrollment.WorkspaceTrustKeysetFingerprint) != "" &&
		strings.TrimSpace(enrollment.WorkspaceTrustPublicSigningKey) != "" {
		return "pinned"
	}
	return "pending"
}

func printWebTTYServerEnrollmentWorkspaceTrust(w io.Writer, enrollment *webTTYServerEnrollmentFile) {
	switch webTTYServerEnrollmentWorkspaceTrustStatus(enrollment) {
	case "pinned":
		fmt.Fprintln(w, "Workspace trust: pinned")
	case "pending":
		fmt.Fprintln(w, "Workspace trust: pending")
		fmt.Fprintln(w, "Next action: run rstream webtty server trust after restoring a trusted workspace device on this machine.")
	}
}

func defaultWebTTYServerControlPlaneCapabilities() *controlplane.WebTTYServerCapabilities {
	return &controlplane.WebTTYServerCapabilities{
		Transports:     []string{"plain", "websocket", "webtransport"},
		ExecutionModes: []string{"spawn", "login"},
		Encryption:     []string{"none", "explicit_key", "workspace_managed"},
	}
}

func validateWebTTYServerID(serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return fmt.Errorf("--server-id is required")
	}
	if serverID == "." || serverID == ".." || strings.ContainsAny(serverID, `/\`) || !webTTYServerIDPattern.MatchString(serverID) {
		return fmt.Errorf("--server-id contains unsupported characters")
	}
	return nil
}

func defaultRstreamHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rstream"), nil
}

func defaultWebTTYServerEnrollmentPath(serverID string) (string, error) {
	if err := validateWebTTYServerID(serverID); err != nil {
		return "", err
	}
	root, err := defaultRstreamHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "webtty", "enrollments", serverID+".yaml"), nil
}

func defaultWebTTYServerIdentityPath(serverID string) (string, error) {
	if err := validateWebTTYServerID(serverID); err != nil {
		return "", err
	}
	root, err := defaultRstreamHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "webtty", "identities", serverID+".identity.json"), nil
}

func readWebTTYServerEnrollmentFromFlags(cmd *cobra.Command) (*webTTYServerEnrollmentFile, string, error) {
	serverID, _ := cmd.Flags().GetString("server-id")
	enrollmentPath, err := webTTYServerEnrollmentPathFromFlags(cmd)
	if err != nil {
		return nil, "", err
	}
	serverID = strings.TrimSpace(serverID)
	enrollmentPath = strings.TrimSpace(enrollmentPath)
	if serverID == "" && enrollmentPath == "" {
		return nil, "", nil
	}
	if serverID != "" {
		if err := validateWebTTYServerID(serverID); err != nil {
			return nil, "", err
		}
	}
	if enrollmentPath == "" {
		var err error
		enrollmentPath, err = defaultWebTTYServerEnrollmentPath(serverID)
		if err != nil {
			return nil, "", err
		}
	}
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		return nil, "", err
	}
	if serverID != "" && enrollment.ServerID != serverID {
		return nil, "", fmt.Errorf("WebTTY server enrollment %s belongs to server %q", enrollmentPath, enrollment.ServerID)
	}
	return enrollment, enrollmentPath, nil
}

func loadWebTTYServerEnrollmentFile(path string) (*webTTYServerEnrollmentFile, error) {
	if err := checkWebTTYServerEnrollmentFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var enrollment webTTYServerEnrollmentFile
	if err := dec.Decode(&enrollment); err != nil {
		return nil, fmt.Errorf("invalid WebTTY server enrollment YAML: %w", err)
	}
	if enrollment.Version != webTTYServerEnrollmentVersion {
		return nil, fmt.Errorf("unsupported WebTTY server enrollment version %d", enrollment.Version)
	}
	if err := validateWebTTYServerID(enrollment.ServerID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(enrollment.ProjectID) == "" {
		return nil, fmt.Errorf("WebTTY server enrollment projectId is required")
	}
	if strings.TrimSpace(enrollment.IdentityFile) == "" {
		return nil, fmt.Errorf("WebTTY server enrollment identityFile is required")
	}
	if strings.TrimSpace(enrollment.ServerPublicKey) == "" ||
		strings.TrimSpace(enrollment.ServerSigningKeyID) == "" ||
		strings.TrimSpace(enrollment.ServerSigningPublicKey) == "" ||
		strings.TrimSpace(enrollment.ServerFingerprint) == "" {
		return nil, fmt.Errorf("WebTTY server enrollment endpoint identity and fingerprint are required")
	}
	if enrollment.ServerKeyAlgorithm != webTTYServerKeyAlgorithmX25519 {
		return nil, fmt.Errorf("unsupported WebTTY server key algorithm %q", enrollment.ServerKeyAlgorithm)
	}
	if err := validateWebTTYServerEnrollmentEncryptionPolicy(enrollment.EncryptionPolicy); err != nil {
		return nil, err
	}
	publicKey, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerPublicKey, webtty.E2EX25519PublicKeySize, "WebTTY server public key")
	if err != nil {
		return nil, err
	}
	if got := webTTYServerPublicKeyFingerprint(publicKey); got != enrollment.ServerFingerprint {
		return nil, fmt.Errorf("WebTTY server enrollment fingerprint does not match public key")
	}
	signingKeyID, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerSigningKeyID, webtty.WebTTYSigningKeyIDSize, "WebTTY server signing key id")
	if err != nil {
		return nil, err
	}
	signingPublicKey, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerSigningPublicKey, 0, "WebTTY server signing public key")
	if err != nil {
		return nil, err
	}
	if _, err := webtty.ParseWebTTYSigningPublicKey(signingPublicKey); err != nil {
		return nil, fmt.Errorf("WebTTY server signing public key is invalid: %w", err)
	}
	if !bytes.Equal(signingKeyID, webtty.WebTTYSigningKeyID(signingPublicKey)) {
		return nil, fmt.Errorf("WebTTY server signing key id does not match public key")
	}
	return &enrollment, nil
}

func writeWebTTYServerEnrollmentFile(path string, enrollment webTTYServerEnrollmentFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(enrollment); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".webtty-server-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func checkWebTTYServerEnrollmentFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("WebTTY server enrollment file %s must not be readable by group or others", path)
	}
	return nil
}

func webTTYServerPublicKeyFingerprint(publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return "sha256:" + webtty.EncodeE2EKeyMaterial(digest[:])
}

func webTTYServerHostKeyIDFromEnrollment(enrollment *webTTYServerEnrollmentFile) (string, error) {
	if enrollment == nil {
		return "", nil
	}
	publicKey, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerPublicKey, webtty.E2EX25519PublicKeySize, "WebTTY server public key")
	if err != nil {
		return "", err
	}
	return webtty.EncodeE2EKeyMaterial(webtty.E2EKeyID(publicKey)), nil
}

func validateWebTTYServerIdentityMatchesEnrollment(enrollment *webTTYServerEnrollmentFile, identity *webtty.E2EIdentity) error {
	if enrollment == nil || identity == nil {
		return nil
	}
	publicKey, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerPublicKey, webtty.E2EX25519PublicKeySize, "WebTTY server public key")
	if err != nil {
		return err
	}
	if !bytes.Equal(publicKey, identity.PublicKey) {
		return fmt.Errorf("WebTTY server identity does not match registered server enrollment")
	}
	if got := webTTYServerPublicKeyFingerprint(identity.PublicKey); got != enrollment.ServerFingerprint {
		return fmt.Errorf("WebTTY server identity fingerprint does not match registered server enrollment")
	}
	return nil
}

func validateWebTTYEndpointIdentityMatchesEnrollment(enrollment *webTTYServerEnrollmentFile, identity *webtty.WebTTYEndpointIdentity) error {
	if enrollment == nil || identity == nil {
		return nil
	}
	if err := validateWebTTYServerIdentityMatchesEnrollment(enrollment, &identity.Encryption); err != nil {
		return err
	}
	signingKeyID, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerSigningKeyID, webtty.WebTTYSigningKeyIDSize, "WebTTY server signing key id")
	if err != nil {
		return err
	}
	signingPublicKey, err := webtty.DecodeE2EKeyMaterial(enrollment.ServerSigningPublicKey, 0, "WebTTY server signing public key")
	if err != nil {
		return err
	}
	if !bytes.Equal(signingKeyID, identity.Signing.KeyID) {
		return fmt.Errorf("WebTTY server signing key id does not match registered server enrollment")
	}
	if !bytes.Equal(signingPublicKey, identity.Signing.PublicKey) {
		return fmt.Errorf("WebTTY server signing public key does not match registered server enrollment")
	}
	return nil
}

func registeredWebTTYServerRequested(cmd *cobra.Command) bool {
	serverID, _ := cmd.Flags().GetString("server-id")
	enrollmentPath, err := webTTYServerEnrollmentPathFromFlags(cmd)
	if err != nil {
		return true
	}
	return strings.TrimSpace(serverID) != "" || strings.TrimSpace(enrollmentPath) != ""
}

func webTTYServerEnrollmentPathFromFlags(cmd *cobra.Command) (string, error) {
	enrollmentPath := strings.TrimSpace(webTTYServerEnrollmentPathValue(cmd))
	if enrollmentPath == "" {
		return "", nil
	}
	return expandWebTTYPath(enrollmentPath)
}

func webTTYServerEnrollmentPathValue(cmd *cobra.Command) string {
	if cmd.Flags().Lookup("server-enrollment") == nil {
		return ""
	}
	value, _ := cmd.Flags().GetString("server-enrollment")
	return value
}

func webTTYServerEnrollmentIdentityPath(enrollment *webTTYServerEnrollmentFile) (string, bool) {
	if enrollment == nil || strings.TrimSpace(enrollment.IdentityFile) == "" {
		return "", false
	}
	return enrollment.IdentityFile, true
}

func validateWebTTYServerEnrollmentEncryptionPolicy(policy string) error {
	policy = strings.TrimSpace(policy)
	switch policy {
	case "", webTTYServerEncryptionPolicyDisabled, webTTYServerEncryptionPolicyExplicitKey, webTTYServerEncryptionPolicyWorkspaceManaged:
		return nil
	default:
		return fmt.Errorf("unsupported WebTTY server encryptionPolicy %q", policy)
	}
}

func webTTYServerEnrollmentRequiresE2E(enrollment *webTTYServerEnrollmentFile) bool {
	if enrollment == nil {
		return false
	}
	switch strings.TrimSpace(enrollment.EncryptionPolicy) {
	case webTTYServerEncryptionPolicyExplicitKey, webTTYServerEncryptionPolicyWorkspaceManaged:
		return true
	default:
		return false
	}
}

func webTTYServerEnrollmentWorkspaceManaged(enrollment *webTTYServerEnrollmentFile) bool {
	return enrollment != nil && strings.TrimSpace(enrollment.EncryptionPolicy) == webTTYServerEncryptionPolicyWorkspaceManaged
}

func webTTYServerRegisteredLabels(enrollment *webTTYServerEnrollmentFile, labels map[string]string) {
	if enrollment == nil || labels == nil {
		return
	}
	labels[webtty.WebTTYServerIDLabelKey] = enrollment.ServerID
	if name := strings.TrimSpace(enrollment.ServerName); name != "" {
		labels[webtty.WebTTYServerNameLabelKey] = name
	}
	if policy := strings.TrimSpace(enrollment.EncryptionPolicy); policy != "" {
		labels[webtty.WebTTYEncryptionPolicyLabelKey] = policy
	}
	if !webTTYServerEnrollmentRequiresE2E(enrollment) {
		labels[webtty.WebTTYE2ELabelKey] = webtty.WebTTYE2EDisabled
		labels[webtty.WebTTYClientProofLabelKey] = webtty.WebTTYClientProofNone
		return
	}
	labels[webtty.WebTTYE2ELabelKey] = webtty.WebTTYE2ERequired
	labels[webtty.WebTTYClientProofLabelKey] = webtty.WebTTYClientProofRequired
	if hostKeyID, err := webTTYServerHostKeyIDFromEnrollment(enrollment); err == nil && strings.TrimSpace(hostKeyID) != "" {
		labels[webtty.WebTTYHostKeyIDLabelKey] = hostKeyID
	}
}

func validateRegisteredWebTTYServerEnrollment(enrollment *webTTYServerEnrollmentFile) error {
	if enrollment == nil {
		return nil
	}
	if strings.TrimSpace(enrollment.ProjectID) == "" || strings.TrimSpace(enrollment.IdentityFile) == "" {
		return errors.New("WebTTY server enrollment is incomplete")
	}
	if err := validateWebTTYServerEnrollmentEncryptionPolicy(enrollment.EncryptionPolicy); err != nil {
		return err
	}
	return nil
}

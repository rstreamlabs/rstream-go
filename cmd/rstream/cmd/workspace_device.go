// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

const (
	workspaceDeviceFileVersion          = 1
	workspaceDeviceKindCLI              = "cli"
	workspaceDeviceKindAgent            = "agent"
	workspaceDeviceKindService          = "service"
	workspaceDeviceStatusPending        = "pending"
	workspaceDeviceStatusActive         = "active"
	workspaceDeviceStatusRevoked        = "revoked"
	workspaceDeviceStatusLost           = "lost"
	workspaceDeviceCryptoSuite          = "p256-hkdf-sha256-aes-256-gcm"
	workspaceDeviceEnvelopeInfo         = "rstream.workspace_key_envelope.v1"
	workspaceDeviceWebTTYCryptoSuite    = "webtty-x25519-hpke-v1"
	workspaceDeviceVerificationAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type workspaceDeviceFile struct {
	Version              int                                `json:"version"`
	Kind                 string                             `json:"kind"`
	WorkspaceID          string                             `json:"workspace_id"`
	DeviceKeyID          string                             `json:"device_key_id"`
	Status               string                             `json:"status"`
	Label                string                             `json:"label,omitempty"`
	PublicEncryptionKey  string                             `json:"public_encryption_key"`
	PrivateEncryptionKey string                             `json:"private_encryption_key"`
	PublicSigningKey     string                             `json:"public_signing_key"`
	PrivateSigningKey    string                             `json:"private_signing_key"`
	WebTTYPublicKey      string                             `json:"webtty_public_key,omitempty"`
	WebTTYKeyID          string                             `json:"webtty_key_id,omitempty"`
	WebTTYKeyAlgorithm   string                             `json:"webtty_key_algorithm,omitempty"`
	WebTTYIdentityPath   string                             `json:"webtty_identity_path,omitempty"`
	Fingerprint          string                             `json:"fingerprint"`
	DeviceEnvelope       *controlplane.WorkspaceKeyEnvelope `json:"device_envelope,omitempty"`
	RotatesDeviceKeyID   string                             `json:"rotates_device_key_id,omitempty"`
	RotationCompletedAt  *time.Time                         `json:"rotation_completed_at,omitempty"`
	CreatedAt            time.Time                          `json:"created_at"`
	UpdatedAt            time.Time                          `json:"updated_at"`
}

type workspacePrivateBundle struct {
	Version                 int               `json:"v"`
	CryptoSuite             string            `json:"cryptoSuite"`
	PublicEncryptionKey     string            `json:"publicEncryptionKey"`
	PublicSigningKey        string            `json:"publicSigningKey"`
	Fingerprint             string            `json:"fingerprint"`
	EncryptionPrivateKeyJWK workspaceECKeyJWK `json:"encryptionPrivateKeyJwk"`
	SigningPrivateKeyJWK    workspaceECKeyJWK `json:"signingPrivateKeyJwk"`
}

type workspaceECKeyJWK struct {
	Kty    string   `json:"kty"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y"`
	D      string   `json:"d"`
	Use    string   `json:"use,omitempty"`
	KeyOps []string `json:"key_ops,omitempty"`
	Alg    string   `json:"alg,omitempty"`
	Ext    *bool    `json:"ext,omitempty"`
}

type workspaceDeviceMaterial struct {
	file           workspaceDeviceFile
	signingPrivate *ecdsa.PrivateKey
	webttyIdentity *webtty.E2EIdentity
	proofSignature string
}

type workspaceDeviceAccessProofWithDevice struct {
	proof  controlplane.WorkspaceDeviceAccessProof
	device workspaceDeviceFile
}

type workspaceDeviceWorkspaceResolution struct {
	WorkspaceID     string `json:"workspace_id"`
	Source          string `json:"source"`
	ProjectEndpoint string `json:"project_endpoint,omitempty"`
}

var workspaceDeviceCmd = &cobra.Command{
	Use:          "device",
	Short:        "Manage trusted workspace devices",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
}

var workspaceDeviceEnrollCmd = &cobra.Command{
	Use:          "enroll",
	Short:        "Enroll this machine as a trusted workspace device",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkspaceDeviceEnroll(cmd)
	},
}

var workspaceDeviceStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show local workspace device trust status",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkspaceDeviceStatus(cmd)
	},
}

var workspaceDeviceRotateCmd = &cobra.Command{
	Use:          "rotate",
	Short:        "Replace the local trusted workspace device",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkspaceDeviceRotate(cmd)
	},
}

func init() {
	workspaceDeviceEnrollCmd.Flags().String("workspace", "", "workspace ID (defaults to the active project workspace)")
	workspaceDeviceEnrollCmd.Flags().String("label", "", "device label")
	workspaceDeviceEnrollCmd.Flags().String("kind", workspaceDeviceKindCLI, "device kind (cli, agent, service)")
	workspaceDeviceEnrollCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	workspaceDeviceStatusCmd.Flags().String("workspace", "", "workspace ID (defaults to the active project workspace)")
	workspaceDeviceStatusCmd.Flags().Bool("all", false, "include revoked and lost local devices")
	workspaceDeviceStatusCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	workspaceDeviceRotateCmd.Flags().String("workspace", "", "workspace ID (defaults to the active project workspace)")
	workspaceDeviceRotateCmd.Flags().String("label", "", "device label")
	workspaceDeviceRotateCmd.Flags().String("kind", workspaceDeviceKindCLI, "device kind (cli, agent, service)")
	workspaceDeviceRotateCmd.Flags().StringP("output", "o", "text", "output mode (text, json, yaml)")
	workspaceDeviceCmd.AddCommand(workspaceDeviceEnrollCmd)
	workspaceDeviceCmd.AddCommand(workspaceDeviceStatusCmd)
	workspaceDeviceCmd.AddCommand(workspaceDeviceRotateCmd)
	workspaceCmd.AddCommand(workspaceDeviceCmd)
}

func runWorkspaceDeviceEnroll(cmd *cobra.Command) error {
	kind, err := workspaceDeviceKind(cmd)
	if err != nil {
		return err
	}
	label, _ := cmd.Flags().GetString("label")
	label = strings.TrimSpace(label)
	if label == "" {
		label = defaultWorkspaceDeviceLabel(kind)
	}
	runtime, err := resolveWorkspaceDeviceRuntime(cmd)
	if err != nil {
		return err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	workspace, err := workspaceDeviceWorkspace(cmd, runtime, client)
	if err != nil {
		return err
	}
	existing, code, found, err := reusableWorkspaceDevice(cmd.Context(), client, workspace.WorkspaceID, kind)
	if err != nil {
		return err
	}
	if found {
		return printWorkspaceDeviceAlreadyEnrolled(cmd, workspace, existing, code)
	}
	createdDevice, err := createWorkspaceDevice(cmd.Context(), client, workspace, kind, label)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	result := workspaceDeviceOutput(createdDevice.path, createdDevice.device, createdDevice.code)
	addWorkspaceResolutionToOutput(result, workspace)
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Workspace device enrollment requested")
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", workspaceDescription(workspace))
	fmt.Fprintf(cmd.OutOrStdout(), "Device: %s\n", createdDevice.device.DeviceKeyID)
	fmt.Fprintf(cmd.OutOrStdout(), "Kind: %s\n", createdDevice.device.Kind)
	fmt.Fprintf(cmd.OutOrStdout(), "Label: %s\n", createdDevice.device.Label)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification code: %s\n", createdDevice.code)
	fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", createdDevice.device.Status)
	fmt.Fprintf(cmd.OutOrStdout(), "Device file: %s\n", createdDevice.path)
	return nil
}

func runWorkspaceDeviceRotate(cmd *cobra.Command) error {
	kind, err := workspaceDeviceKind(cmd)
	if err != nil {
		return err
	}
	label, _ := cmd.Flags().GetString("label")
	label = strings.TrimSpace(label)
	if label == "" {
		label = defaultWorkspaceDeviceLabel(kind)
	}
	runtime, err := resolveWorkspaceDeviceRuntime(cmd)
	if err != nil {
		return err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	workspace, err := workspaceDeviceWorkspace(cmd, runtime, client)
	if err != nil {
		return err
	}
	existing, _, found, err := reusableWorkspaceDevice(cmd.Context(), client, workspace.WorkspaceID, kind)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no active or pending local %s workspace device found for %s; run rstream workspace device enroll", kind, workspaceDescription(workspace))
	}
	created, err := createWorkspaceDevice(cmd.Context(), client, workspace, kind, label, existing.device.DeviceKeyID)
	if err != nil {
		return err
	}
	output, _ := cmd.Flags().GetString("output")
	result := workspaceDeviceOutput(created.path, created.device, created.code)
	result["rotated"] = found
	result["previous_device_id"] = existing.device.DeviceKeyID
	result["previous_device_status"] = existing.device.Status
	addWorkspaceResolutionToOutput(result, workspace)
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Workspace device rotation requested")
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", workspaceDescription(workspace))
	fmt.Fprintf(cmd.OutOrStdout(), "Device: %s\n", created.device.DeviceKeyID)
	fmt.Fprintf(cmd.OutOrStdout(), "Kind: %s\n", created.device.Kind)
	fmt.Fprintf(cmd.OutOrStdout(), "Label: %s\n", created.device.Label)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification code: %s\n", created.code)
	fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", created.device.Status)
	fmt.Fprintf(cmd.OutOrStdout(), "Device file: %s\n", created.path)
	if existing.device.Status == workspaceDeviceStatusActive {
		fmt.Fprintf(cmd.OutOrStdout(), "Previous device: %s remains active until the replacement is approved\n", existing.device.DeviceKeyID)
	}
	return nil
}

func runWorkspaceDeviceStatus(cmd *cobra.Command) error {
	runtime, err := resolveWorkspaceDeviceRuntime(cmd)
	if err != nil {
		return err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	workspace, err := workspaceDeviceWorkspace(cmd, runtime, client)
	if err != nil {
		return err
	}
	devices, err := loadWorkspaceDeviceFiles(workspace.WorkspaceID)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return fmt.Errorf("no local workspace devices found for %s; run rstream workspace device enroll", workspaceDescription(workspace))
	}
	includeAll, _ := cmd.Flags().GetBool("all")
	refreshed := make([]workspaceDeviceFileWithPath, 0, len(devices))
	for _, item := range devices {
		updated, _, err := refreshWorkspaceDeviceStatus(cmd, client, item.path, item.device)
		if err != nil {
			return err
		}
		item.device = updated
		refreshed = append(refreshed, item)
	}
	refreshed, err = completeWorkspaceDeviceRotations(cmd.Context(), client, workspace.WorkspaceID, refreshed)
	if err != nil {
		return err
	}
	outputs := make([]map[string]any, 0, len(refreshed))
	for _, item := range refreshed {
		if !includeAll && (item.device.Status == workspaceDeviceStatusRevoked || item.device.Status == workspaceDeviceStatusLost) {
			continue
		}
		code, err := workspaceDeviceVerificationCode(item.device)
		if err != nil {
			return err
		}
		itemOutput := workspaceDeviceOutput(item.path, item.device, code)
		addWorkspaceResolutionToOutput(itemOutput, workspace)
		outputs = append(outputs, itemOutput)
	}
	if len(outputs) == 0 && !includeAll {
		return fmt.Errorf("no active or pending local workspace devices found for %s; run rstream workspace device enroll, or pass --all to show revoked devices", workspaceDescription(workspace))
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, map[string]any{
			"workspace": workspace,
			"devices":   outputs,
		})
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", workspaceDescription(workspace))
	for _, out := range outputs {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n", out["device_id"], out["kind"], out["status"], out["verification_code"])
	}
	return nil
}

type workspaceDeviceCreation struct {
	path   string
	device workspaceDeviceFile
	code   string
}

func createWorkspaceDevice(ctx context.Context, client *controlplane.Client, workspace workspaceDeviceWorkspaceResolution, kind string, label string, rotatesDeviceKeyID ...string) (workspaceDeviceCreation, error) {
	material, err := generateWorkspaceDeviceMaterial(workspace.WorkspaceID, kind, label)
	if err != nil {
		return workspaceDeviceCreation{}, err
	}
	created, err := client.CreateWorkspaceDeviceKey(ctx, workspace.WorkspaceID, controlplane.CreateWorkspaceDeviceKeyRequest{
		Kind:                material.file.Kind,
		Label:               material.file.Label,
		PublicEncryptionKey: material.file.PublicEncryptionKey,
		PublicSigningKey:    material.file.PublicSigningKey,
		WebTTYPublicKey:     material.file.WebTTYPublicKey,
		WebTTYKeyID:         material.file.WebTTYKeyID,
		WebTTYKeyAlgorithm:  material.file.WebTTYKeyAlgorithm,
		Fingerprint:         material.file.Fingerprint,
		ProofSignature:      material.proofSignature,
	})
	if err != nil {
		return workspaceDeviceCreation{}, mapControlPlaneError(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	material.file.DeviceKeyID = created.DeviceKeyID
	material.file.Status = created.Status
	if len(rotatesDeviceKeyID) > 0 {
		material.file.RotatesDeviceKeyID = strings.TrimSpace(rotatesDeviceKeyID[0])
	}
	material.file.CreatedAt = now
	material.file.UpdatedAt = now
	identityPath, err := defaultWorkspaceWebTTYDeviceIdentityPath(material.file.WorkspaceID, material.file.DeviceKeyID)
	if err != nil {
		return workspaceDeviceCreation{}, err
	}
	material.file.WebTTYIdentityPath = identityPath
	if material.webttyIdentity != nil {
		if err := webtty.WriteE2EIdentityFile(identityPath, *material.webttyIdentity); err != nil {
			return workspaceDeviceCreation{}, err
		}
	}
	path, err := writeWorkspaceDeviceFile(material.file)
	if err != nil {
		return workspaceDeviceCreation{}, err
	}
	code, err := workspaceDeviceVerificationCode(material.file)
	if err != nil {
		return workspaceDeviceCreation{}, err
	}
	return workspaceDeviceCreation{path: path, device: material.file, code: code}, nil
}

func reusableWorkspaceDevice(ctx context.Context, client *controlplane.Client, workspaceID string, kind string) (workspaceDeviceFileWithPath, string, bool, error) {
	devices, err := loadWorkspaceDeviceFiles(workspaceID)
	if err != nil {
		return workspaceDeviceFileWithPath{}, "", false, err
	}
	for _, item := range devices {
		if item.device.Kind != kind {
			continue
		}
		updated, code, err := refreshWorkspaceDeviceStatusContext(ctx, client, item.path, item.device)
		if err != nil {
			return workspaceDeviceFileWithPath{}, "", false, err
		}
		item.device = updated
		if workspaceDeviceStatusBlocksDuplicateEnroll(updated.Status) {
			return item, code, true, nil
		}
	}
	return workspaceDeviceFileWithPath{}, "", false, nil
}

func workspaceDeviceStatusBlocksDuplicateEnroll(status string) bool {
	return status == workspaceDeviceStatusPending || status == workspaceDeviceStatusActive
}

func printWorkspaceDeviceAlreadyEnrolled(cmd *cobra.Command, workspace workspaceDeviceWorkspaceResolution, item workspaceDeviceFileWithPath, code string) error {
	output, _ := cmd.Flags().GetString("output")
	result := workspaceDeviceOutput(item.path, item.device, code)
	result["already_enrolled"] = true
	addWorkspaceResolutionToOutput(result, workspace)
	if output == "json" || output == "yaml" {
		return writeStructuredOutput(output, result)
	}
	if output != "text" {
		return validateOutputMode(output, "text", "json", "yaml")
	}
	if item.device.Status == workspaceDeviceStatusPending {
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace device enrollment is already pending")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace device is already enrolled")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", workspaceDescription(workspace))
	fmt.Fprintf(cmd.OutOrStdout(), "Device: %s\n", item.device.DeviceKeyID)
	fmt.Fprintf(cmd.OutOrStdout(), "Kind: %s\n", item.device.Kind)
	fmt.Fprintf(cmd.OutOrStdout(), "Label: %s\n", item.device.Label)
	fmt.Fprintf(cmd.OutOrStdout(), "Verification code: %s\n", code)
	fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", item.device.Status)
	fmt.Fprintf(cmd.OutOrStdout(), "Device file: %s\n", item.path)
	return nil
}

func revokeWorkspaceDeviceWithLocalProof(ctx context.Context, client *controlplane.Client, workspaceID string, device workspaceDeviceFile, reason string) error {
	request := controlplane.RevokeWorkspaceDeviceKeyRequest{
		Reason: reason,
	}
	if device.Status == workspaceDeviceStatusActive {
		return revokeWorkspaceDeviceWithActorProof(ctx, client, workspaceID, device, device, reason)
	}
	if err := client.RevokeWorkspaceDeviceKey(ctx, workspaceID, device.DeviceKeyID, request); err != nil {
		return mapControlPlaneError(err)
	}
	return nil
}

func revokeWorkspaceDeviceWithActorProof(ctx context.Context, client *controlplane.Client, workspaceID string, actor workspaceDeviceFile, target workspaceDeviceFile, reason string) error {
	if actor.Status != workspaceDeviceStatusActive {
		return fmt.Errorf("workspace device %s cannot revoke %s because it is %s", actor.DeviceKeyID, target.DeviceKeyID, actor.Status)
	}
	signingKey, err := parseWorkspaceDeviceSigningKey(actor)
	if err != nil {
		return err
	}
	signature, err := signWorkspacePayload(signingKey, workspaceDeviceRevokePayload(
		workspaceID,
		actor.DeviceKeyID,
		target.DeviceKeyID,
		workspaceDeviceStatusRevoked,
		reason,
	))
	if err != nil {
		return err
	}
	request := controlplane.RevokeWorkspaceDeviceKeyRequest{
		Reason:           reason,
		ActorDeviceKeyID: actor.DeviceKeyID,
		Signature:        signature,
	}
	if err := client.RevokeWorkspaceDeviceKey(ctx, workspaceID, target.DeviceKeyID, request); err != nil {
		return mapControlPlaneError(err)
	}
	return nil
}

func refreshWorkspaceDeviceStatus(cmd *cobra.Command, client *controlplane.Client, path string, device workspaceDeviceFile) (workspaceDeviceFile, string, error) {
	return refreshWorkspaceDeviceStatusContext(cmd.Context(), client, path, device)
}

func refreshWorkspaceDeviceStatusContext(ctx context.Context, client *controlplane.Client, path string, device workspaceDeviceFile) (workspaceDeviceFile, string, error) {
	signingKey, err := parseWorkspaceDeviceSigningKey(device)
	if err != nil {
		return device, "", err
	}
	challenge, err := workspaceRandomBase64URL(32)
	if err != nil {
		return device, "", err
	}
	signedAt := workspaceSignedAtNow()
	signature, err := signWorkspacePayload(signingKey, workspaceDeviceLookupPayload(device.WorkspaceID, device.Fingerprint, challenge, signedAt))
	if err != nil {
		return device, "", err
	}
	lookup, err := client.LookupWorkspaceDeviceKeys(ctx, device.WorkspaceID, controlplane.LookupWorkspaceDeviceKeysRequest{
		Proofs: []controlplane.WorkspaceDeviceAccessProof{{
			DeviceFingerprint: device.Fingerprint,
			Challenge:         challenge,
			SignedAt:          signedAt,
			Signature:         signature,
		}},
	})
	if err != nil {
		return device, "", mapControlPlaneError(err)
	}
	for _, remote := range lookup.Devices {
		if remote.ID != device.DeviceKeyID {
			continue
		}
		device.Status = remote.Status
		device.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	}
	for _, envelope := range lookup.DeviceEnvelopes {
		if envelope.RecipientID == device.DeviceKeyID && envelope.RecipientKind == "device" {
			next := envelope
			device.DeviceEnvelope = &next
			device.UpdatedAt = time.Now().UTC().Truncate(time.Second)
			break
		}
	}
	if err := writeWorkspaceDeviceFileAt(path, device); err != nil {
		return device, "", err
	}
	code, err := workspaceDeviceVerificationCode(device)
	if err != nil {
		return device, "", err
	}
	return device, code, nil
}

func completeWorkspaceDeviceRotations(ctx context.Context, client *controlplane.Client, workspaceID string, devices []workspaceDeviceFileWithPath) ([]workspaceDeviceFileWithPath, error) {
	byDeviceID := make(map[string]int, len(devices))
	for index, item := range devices {
		byDeviceID[item.device.DeviceKeyID] = index
	}
	for index, item := range devices {
		replacement := item.device
		rotatesDeviceKeyID := strings.TrimSpace(replacement.RotatesDeviceKeyID)
		if rotatesDeviceKeyID == "" || replacement.RotationCompletedAt != nil {
			continue
		}
		if replacement.Status != workspaceDeviceStatusActive {
			continue
		}
		oldIndex, ok := byDeviceID[rotatesDeviceKeyID]
		if !ok {
			return nil, fmt.Errorf("workspace device %s replaces %s, but the replaced local device file is missing; restore the old device file or remove the incomplete replacement", replacement.DeviceKeyID, rotatesDeviceKeyID)
		}
		oldDevice := devices[oldIndex].device
		switch oldDevice.Status {
		case workspaceDeviceStatusActive, workspaceDeviceStatusPending:
			if err := revokeWorkspaceDeviceWithActorProof(ctx, client, workspaceID, replacement, oldDevice, "Replaced by a rotated local workspace device."); err != nil {
				return nil, err
			}
			oldDevice.Status = workspaceDeviceStatusRevoked
			oldDevice.UpdatedAt = time.Now().UTC().Truncate(time.Second)
			if err := writeWorkspaceDeviceFileAt(devices[oldIndex].path, oldDevice); err != nil {
				return nil, err
			}
			devices[oldIndex].device = oldDevice
		case workspaceDeviceStatusRevoked, workspaceDeviceStatusLost:
		default:
			return nil, fmt.Errorf("workspace device %s has unsupported rotation predecessor status %q", oldDevice.DeviceKeyID, oldDevice.Status)
		}
		now := time.Now().UTC().Truncate(time.Second)
		replacement.RotationCompletedAt = &now
		replacement.UpdatedAt = now
		if err := writeWorkspaceDeviceFileAt(item.path, replacement); err != nil {
			return nil, err
		}
		devices[index].device = replacement
	}
	return devices, nil
}

func workspaceDeviceAccessProofs(workspaceID string, limit int) ([]controlplane.WorkspaceDeviceAccessProof, error) {
	items, err := workspaceDeviceAccessProofsWithDevices(workspaceID, limit)
	if err != nil {
		return nil, err
	}
	proofs := make([]controlplane.WorkspaceDeviceAccessProof, 0, len(items))
	for _, item := range items {
		proofs = append(proofs, item.proof)
	}
	return proofs, nil
}

func workspaceDeviceAccessProofsWithDevices(workspaceID string, limit int) ([]workspaceDeviceAccessProofWithDevice, error) {
	if limit <= 0 {
		return nil, nil
	}
	devices, err := loadWorkspaceDeviceFiles(workspaceID)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, nil
	}
	proofs := make([]workspaceDeviceAccessProofWithDevice, 0, min(limit, len(devices)))
	for _, item := range devices {
		if len(proofs) >= limit {
			break
		}
		if !workspaceDeviceIsActive(item.device) {
			continue
		}
		signingKey, err := parseWorkspaceDeviceSigningKey(item.device)
		if err != nil {
			return nil, err
		}
		challenge, err := workspaceRandomBase64URL(32)
		if err != nil {
			return nil, err
		}
		signedAt := workspaceSignedAtNow()
		signature, err := signWorkspacePayload(signingKey, workspaceDeviceLookupPayload(item.device.WorkspaceID, item.device.Fingerprint, challenge, signedAt))
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, workspaceDeviceAccessProofWithDevice{
			proof: controlplane.WorkspaceDeviceAccessProof{
				DeviceFingerprint: item.device.Fingerprint,
				Challenge:         challenge,
				SignedAt:          signedAt,
				Signature:         signature,
			},
			device: item.device,
		})
	}
	return proofs, nil
}

func workspaceDeviceWorkspace(cmd *cobra.Command, runtime *resolvedRuntime, client *controlplane.Client) (workspaceDeviceWorkspaceResolution, error) {
	workspaceID, _ := cmd.Flags().GetString("workspace")
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		if err := validateWorkspaceID(workspaceID); err != nil {
			return workspaceDeviceWorkspaceResolution{}, err
		}
		return workspaceDeviceWorkspaceResolution{WorkspaceID: workspaceID, Source: "flag"}, nil
	}
	if runtime == nil || runtime.Resolved.Context == nil || strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint) == "" {
		return workspaceDeviceWorkspaceResolution{}, workspaceDeviceWorkspaceRequiredError()
	}
	projectEndpoint := strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint)
	project, err := client.ResolveProjectByEndpoint(cmd.Context(), projectEndpoint)
	if err != nil {
		return workspaceDeviceWorkspaceResolution{}, mapControlPlaneError(err)
	}
	workspaceID = strings.TrimSpace(project.WorkspaceID)
	if workspaceID == "" {
		return workspaceDeviceWorkspaceResolution{}, fmt.Errorf("active project %s has no workspace ID; run rstream workspace list and pass --workspace", projectEndpoint)
	}
	if err := validateWorkspaceID(workspaceID); err != nil {
		return workspaceDeviceWorkspaceResolution{}, err
	}
	return workspaceDeviceWorkspaceResolution{
		WorkspaceID:     workspaceID,
		Source:          "active_project",
		ProjectEndpoint: projectEndpoint,
	}, nil
}

func resolveWorkspaceDeviceRuntime(cmd *cobra.Command) (*resolvedRuntime, error) {
	workspaceID, _ := cmd.Flags().GetString("workspace")
	if strings.TrimSpace(workspaceID) != "" {
		apiURL, _ := cmd.Flags().GetString("api-url")
		env := config.ReadEnv()
		if strings.TrimSpace(apiURL) != "" || strings.TrimSpace(env.APIURL) != "" {
			return resolveControlPlane(cmd, true)
		}
	}
	return resolveRuntime(cmd, false, true)
}

func validateWorkspaceID(workspaceID string) error {
	if workspaceID == "." || workspaceID == ".." || strings.ContainsAny(workspaceID, `/\`) {
		return fmt.Errorf("--workspace contains unsupported characters")
	}
	return nil
}

func workspaceDeviceWorkspaceRequiredError() error {
	return fmt.Errorf("--workspace is required when no active project context is configured; run rstream workspace list to copy a workspace ID, or run rstream project use <project-endpoint> and retry")
}

func workspaceDeviceEnrollmentHint(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "run rstream workspace device enroll from an active project context, approve the verification code from a trusted browser, then run rstream workspace device status"
	}
	return fmt.Sprintf("run rstream workspace device enroll from an active project context, or run rstream workspace device enroll --workspace %s; approve the verification code from a trusted browser, then run rstream workspace device status", workspaceID)
}

func workspaceDescription(workspace workspaceDeviceWorkspaceResolution) string {
	if workspace.Source == "active_project" && workspace.ProjectEndpoint != "" {
		return fmt.Sprintf("%s (from active project %s)", workspace.WorkspaceID, workspace.ProjectEndpoint)
	}
	return workspace.WorkspaceID
}

func addWorkspaceResolutionToOutput(output map[string]any, workspace workspaceDeviceWorkspaceResolution) {
	output["workspace_source"] = workspace.Source
	if workspace.ProjectEndpoint != "" {
		output["project_endpoint"] = workspace.ProjectEndpoint
	}
}

func workspaceDeviceKind(cmd *cobra.Command) (string, error) {
	if cmd.Flags().Lookup("kind") == nil {
		return workspaceDeviceKindCLI, nil
	}
	kind, _ := cmd.Flags().GetString("kind")
	kind, err := normalizeWorkspaceDeviceKind(kind)
	if err != nil {
		return "", fmt.Errorf("--kind must be one of: cli, agent, service")
	}
	return kind, nil
}

func normalizeWorkspaceDeviceKind(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case workspaceDeviceKindCLI, workspaceDeviceKindAgent, workspaceDeviceKindService:
		return kind, nil
	default:
		return "", fmt.Errorf("unsupported workspace device kind %q", kind)
	}
}

func generateWorkspaceDeviceMaterial(workspaceID string, kind string, label string) (*workspaceDeviceMaterial, error) {
	kind, err := normalizeWorkspaceDeviceKind(kind)
	if err != nil {
		return nil, err
	}
	encryptionPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate workspace device encryption key: %w", err)
	}
	signingPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate workspace device signing key: %w", err)
	}
	publicEncryptionKey, privateEncryptionKey, err := marshalWorkspaceDeviceEncryptionKeyPair(encryptionPrivate)
	if err != nil {
		return nil, err
	}
	publicSigningKey, privateSigningKey, err := marshalWorkspaceDeviceSigningKeyPair(signingPrivate)
	if err != nil {
		return nil, err
	}
	webttyIdentity, err := webtty.GenerateE2EIdentity()
	if err != nil {
		return nil, fmt.Errorf("generate WebTTY device identity: %w", err)
	}
	webttyPublicKey := webtty.EncodeE2EKeyMaterial(webttyIdentity.PublicKey)
	webttyKeyID := webtty.EncodeE2EKeyMaterial(webttyIdentity.KeyID)
	fingerprint, err := workspacePublicKeyFingerprint(publicEncryptionKey, publicSigningKey)
	if err != nil {
		return nil, err
	}
	payload := workspaceDeviceProofPayload(workspaceID, kind, label, publicEncryptionKey, publicSigningKey, webttyPublicKey, webttyKeyID, workspaceDeviceWebTTYCryptoSuite, fingerprint)
	signature, err := signWorkspacePayload(signingPrivate, payload)
	if err != nil {
		return nil, err
	}
	return &workspaceDeviceMaterial{
		file: workspaceDeviceFile{
			Version:              workspaceDeviceFileVersion,
			Kind:                 kind,
			WorkspaceID:          workspaceID,
			Label:                label,
			PublicEncryptionKey:  publicEncryptionKey,
			PrivateEncryptionKey: privateEncryptionKey,
			PublicSigningKey:     publicSigningKey,
			PrivateSigningKey:    privateSigningKey,
			WebTTYPublicKey:      webttyPublicKey,
			WebTTYKeyID:          webttyKeyID,
			WebTTYKeyAlgorithm:   workspaceDeviceWebTTYCryptoSuite,
			Fingerprint:          fingerprint,
		},
		signingPrivate: signingPrivate,
		webttyIdentity: webttyIdentity,
		proofSignature: signature,
	}, nil
}

func marshalWorkspaceDeviceEncryptionKeyPair(privateKey *ecdh.PrivateKey) (string, string, error) {
	publicDER, err := x509.MarshalPKIXPublicKey(privateKey.PublicKey())
	if err != nil {
		return "", "", fmt.Errorf("marshal workspace device public encryption key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal workspace device private encryption key: %w", err)
	}
	return workspaceBase64URL(publicDER), workspaceBase64URL(privateDER), nil
}

func marshalWorkspaceDeviceSigningKeyPair(privateKey *ecdsa.PrivateKey) (string, string, error) {
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal workspace device public signing key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal workspace device private signing key: %w", err)
	}
	return workspaceBase64URL(publicDER), workspaceBase64URL(privateDER), nil
}

func parseWorkspaceDeviceEncryptionKey(device workspaceDeviceFile) (*ecdh.PrivateKey, error) {
	der, err := base64.RawURLEncoding.DecodeString(device.PrivateEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decode workspace device encryption key: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse workspace device encryption key: %w", err)
	}
	switch privateKey := key.(type) {
	case *ecdh.PrivateKey:
		if privateKey.Curve() != ecdh.P256() {
			return nil, fmt.Errorf("workspace device encryption key is not P-256 ECDH")
		}
		return privateKey, nil
	case *ecdsa.PrivateKey:
		if privateKey.Curve != elliptic.P256() {
			return nil, fmt.Errorf("workspace device encryption key is not P-256 ECDH")
		}
		scalar := privateKey.D.Bytes()
		if len(scalar) > 32 {
			return nil, fmt.Errorf("workspace device encryption key scalar is invalid")
		}
		padded := make([]byte, 32)
		copy(padded[32-len(scalar):], scalar)
		ecdhKey, err := ecdh.P256().NewPrivateKey(padded)
		if err != nil {
			return nil, fmt.Errorf("parse workspace device encryption key scalar: %w", err)
		}
		return ecdhKey, nil
	default:
		return nil, fmt.Errorf("workspace device encryption key is not P-256 ECDH")
	}
}

func parseWorkspaceDeviceSigningKey(device workspaceDeviceFile) (*ecdsa.PrivateKey, error) {
	der, err := base64.RawURLEncoding.DecodeString(device.PrivateSigningKey)
	if err != nil {
		return nil, fmt.Errorf("decode workspace device signing key: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse workspace device signing key: %w", err)
	}
	privateKey, ok := key.(*ecdsa.PrivateKey)
	if !ok || privateKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("workspace device signing key is not P-256 ECDSA")
	}
	return privateKey, nil
}

func decryptWorkspaceKeyEnvelope(device workspaceDeviceFile, envelope controlplane.WorkspaceKeyEnvelope) (*workspacePrivateBundle, error) {
	if device.Status != workspaceDeviceStatusActive {
		return nil, fmt.Errorf("workspace device %s is not active", device.DeviceKeyID)
	}
	if envelope.RecipientKind != "device" || envelope.RecipientID != device.DeviceKeyID {
		return nil, fmt.Errorf("workspace key envelope does not target this device")
	}
	if envelope.Crypto.Suite != workspaceDeviceCryptoSuite {
		return nil, fmt.Errorf("unsupported workspace key envelope suite %q", envelope.Crypto.Suite)
	}
	if envelope.RevokedAt != nil {
		return nil, fmt.Errorf("workspace key envelope is revoked")
	}
	privateKey, err := parseWorkspaceDeviceEncryptionKey(device)
	if err != nil {
		return nil, err
	}
	ephemeralPublicKey, err := workspaceP256ECDHPublicKeyFromSPKI(envelope.Crypto.EncapsulatedKey)
	if err != nil {
		return nil, fmt.Errorf("parse workspace key envelope encapsulated key: %w", err)
	}
	sharedSecret, err := privateKey.ECDH(ephemeralPublicKey)
	if err != nil {
		return nil, fmt.Errorf("derive workspace key envelope secret: %w", err)
	}
	context := envelope.Crypto.Context
	salt, err := workspaceContextBase64URL(context, "salt")
	if err != nil {
		return nil, err
	}
	nonce, err := workspaceContextBase64URL(context, "nonce")
	if err != nil {
		return nil, err
	}
	info := workspaceDeviceEnvelopeInfo
	if raw, ok := context["info"].(string); ok && strings.TrimSpace(raw) != "" {
		info = raw
	}
	key, err := hkdf.Key(sha256.New, sharedSecret, salt, info, 32)
	if err != nil {
		return nil, fmt.Errorf("derive workspace key envelope key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create workspace key envelope cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create workspace key envelope AEAD: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode workspace key envelope ciphertext: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt workspace key envelope: %w", err)
	}
	var bundle workspacePrivateBundle
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("decode workspace private bundle: %w", err)
	}
	if bundle.Version != 1 || bundle.CryptoSuite != workspaceDeviceCryptoSuite {
		return nil, fmt.Errorf("unsupported workspace private bundle")
	}
	if strings.TrimSpace(bundle.PublicSigningKey) == "" || strings.TrimSpace(bundle.Fingerprint) == "" {
		return nil, fmt.Errorf("workspace private bundle is incomplete")
	}
	return &bundle, nil
}

func workspaceContextBase64URL(context map[string]any, key string) ([]byte, error) {
	value, ok := context[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("workspace key envelope context %s is required", key)
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode workspace key envelope context %s: %w", key, err)
	}
	return data, nil
}

func workspaceP256ECDHPublicKeyFromSPKI(encoded string) (*ecdh.PublicKey, error) {
	der, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	switch publicKey := key.(type) {
	case *ecdh.PublicKey:
		if publicKey.Curve() != ecdh.P256() {
			return nil, fmt.Errorf("public key is not P-256 ECDH")
		}
		return publicKey, nil
	case *ecdsa.PublicKey:
		if publicKey.Curve != elliptic.P256() || !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
			return nil, fmt.Errorf("public key is not P-256 ECDH")
		}
		raw := elliptic.Marshal(elliptic.P256(), publicKey.X, publicKey.Y)
		ecdhPublicKey, err := ecdh.P256().NewPublicKey(raw)
		if err != nil {
			return nil, err
		}
		return ecdhPublicKey, nil
	default:
		return nil, fmt.Errorf("public key is not P-256 ECDH")
	}
}

func workspaceP256SigningKeyFromJWK(jwk workspaceECKeyJWK) (*ecdsa.PrivateKey, error) {
	if jwk.Kty != "EC" || jwk.Crv != "P-256" {
		return nil, fmt.Errorf("workspace private bundle signing key is not P-256")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("decode workspace signing key x coordinate: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("decode workspace signing key y coordinate: %w", err)
	}
	dBytes, err := base64.RawURLEncoding.DecodeString(jwk.D)
	if err != nil {
		return nil, fmt.Errorf("decode workspace signing key scalar: %w", err)
	}
	curve := elliptic.P256()
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	d := new(big.Int).SetBytes(dBytes)
	if !curve.IsOnCurve(x, y) || d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 {
		return nil, fmt.Errorf("workspace signing key JWK is invalid")
	}
	scalar := d.Bytes()
	padded := make([]byte, 32)
	if len(scalar) > len(padded) {
		return nil, fmt.Errorf("workspace signing key scalar is invalid")
	}
	copy(padded[len(padded)-len(scalar):], scalar)
	checkX, checkY := curve.ScalarBaseMult(padded)
	if checkX.Cmp(x) != 0 || checkY.Cmp(y) != 0 {
		return nil, fmt.Errorf("workspace signing key public coordinates do not match private scalar")
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
		D:         d,
	}, nil
}

func signWorkspacePayload(privateKey *ecdsa.PrivateKey, payload any) (string, error) {
	canonical, err := workspaceCanonicalJSON(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign workspace payload: %w", err)
	}
	return workspaceBase64URL(signature), nil
}

func workspacePublicKeyFingerprint(publicEncryptionKey string, publicSigningKey string) (string, error) {
	hash, err := workspaceSHA256Base64URL(map[string]any{
		"v":                     1,
		"type":                  "workspace.public_keys",
		"public_encryption_key": publicEncryptionKey,
		"public_signing_key":    publicSigningKey,
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hash, nil
}

func workspaceDeviceProofPayload(workspaceID string, kind string, label string, publicEncryptionKey string, publicSigningKey string, webttyPublicKey string, webttyKeyID string, webttyKeyAlgorithm string, fingerprint string) map[string]any {
	payload := map[string]any{
		"v":                     1,
		"type":                  "workspace.device.proof",
		"workspace_id":          workspaceID,
		"device_kind":           kind,
		"public_encryption_key": publicEncryptionKey,
		"public_signing_key":    publicSigningKey,
		"fingerprint":           fingerprint,
	}
	if strings.TrimSpace(label) != "" {
		payload["label"] = label
	}
	if strings.TrimSpace(webttyPublicKey) != "" || strings.TrimSpace(webttyKeyID) != "" || strings.TrimSpace(webttyKeyAlgorithm) != "" {
		payload["webtty_public_key"] = webttyPublicKey
		payload["webtty_key_id"] = webttyKeyID
		payload["webtty_key_algorithm"] = webttyKeyAlgorithm
	}
	return payload
}

func workspaceDeviceLookupPayload(workspaceID string, fingerprint string, challenge string, signedAt string) map[string]any {
	return map[string]any{
		"v":                  1,
		"type":               "workspace.device.lookup",
		"workspace_id":       workspaceID,
		"device_fingerprint": fingerprint,
		"challenge":          challenge,
		"signed_at":          signedAt,
	}
}

func workspaceDeviceRevokePayload(workspaceID string, actorDeviceKeyID string, targetDeviceKeyID string, nextStatus string, reason string) map[string]any {
	payload := map[string]any{
		"v":                    1,
		"type":                 "workspace.device.revoke",
		"workspace_id":         workspaceID,
		"actor_device_key_id":  actorDeviceKeyID,
		"target_device_key_id": targetDeviceKeyID,
		"next_status":          nextStatus,
	}
	if strings.TrimSpace(reason) != "" {
		payload["reason"] = reason
	}
	return payload
}

func workspaceSignedAtNow() string {
	return time.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func workspaceDeviceVerificationPayload(device workspaceDeviceFile) map[string]any {
	payload := map[string]any{
		"v":                     1,
		"type":                  "workspace.device.verification",
		"workspace_id":          device.WorkspaceID,
		"device_key_id":         device.DeviceKeyID,
		"device_kind":           device.Kind,
		"public_encryption_key": device.PublicEncryptionKey,
		"public_signing_key":    device.PublicSigningKey,
		"fingerprint":           device.Fingerprint,
	}
	if strings.TrimSpace(device.WebTTYPublicKey) != "" || strings.TrimSpace(device.WebTTYKeyID) != "" || strings.TrimSpace(device.WebTTYKeyAlgorithm) != "" {
		payload["webtty_public_key"] = device.WebTTYPublicKey
		payload["webtty_key_id"] = device.WebTTYKeyID
		payload["webtty_key_algorithm"] = device.WebTTYKeyAlgorithm
	}
	return payload
}

func workspaceDeviceVerificationCode(device workspaceDeviceFile) (string, error) {
	canonical, err := workspaceCanonicalJSON(workspaceDeviceVerificationPayload(device))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	code := make([]byte, 12)
	for i := range code {
		code[i] = workspaceDeviceVerificationAlphabet[int(digest[i])%len(workspaceDeviceVerificationAlphabet)]
	}
	return string(code[0:4]) + "-" + string(code[4:8]) + "-" + string(code[8:12]), nil
}

func workspaceSHA256Base64URL(value any) (string, error) {
	canonical, err := workspaceCanonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	return workspaceBase64URL(digest[:]), nil
}

func workspaceBase64URL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func workspaceRandomBase64URL(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate workspace challenge: %w", err)
	}
	return workspaceBase64URL(data), nil
}

func workspaceCanonicalJSON(value any) (string, error) {
	switch item := value.(type) {
	case nil:
		return "null", nil
	case string:
		data, err := json.Marshal(item)
		return string(data), err
	case bool:
		if item {
			return "true", nil
		}
		return "false", nil
	case int:
		return fmt.Sprintf("%d", item), nil
	case int64:
		return fmt.Sprintf("%d", item), nil
	case json.Number:
		value := item.String()
		if strings.ContainsAny(value, ".eE") {
			return "", fmt.Errorf("unsupported canonical JSON number %q", value)
		}
		return value, nil
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key, value := range item {
			if value != nil {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyJSON, err := json.Marshal(key)
			if err != nil {
				return "", err
			}
			valueJSON, err := workspaceCanonicalJSON(item[key])
			if err != nil {
				return "", err
			}
			buf.Write(keyJSON)
			buf.WriteByte(':')
			buf.WriteString(valueJSON)
		}
		buf.WriteByte('}')
		return buf.String(), nil
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, value := range item {
			if i > 0 {
				buf.WriteByte(',')
			}
			valueJSON, err := workspaceCanonicalJSON(value)
			if err != nil {
				return "", err
			}
			buf.WriteString(valueJSON)
		}
		buf.WriteByte(']')
		return buf.String(), nil
	default:
		return "", fmt.Errorf("unsupported canonical JSON value %T", value)
	}
}

type workspaceDeviceFileWithPath struct {
	path   string
	device workspaceDeviceFile
}

func writeWorkspaceDeviceFile(device workspaceDeviceFile) (string, error) {
	dir, err := defaultWorkspaceDevicesDir(device.WorkspaceID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, device.DeviceKeyID+".json")
	return path, writeWorkspaceDeviceFileAt(path, device)
}

func writeWorkspaceDeviceFileAt(path string, device workspaceDeviceFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	data, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace device file: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".workspace-device-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
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

func loadWorkspaceDeviceFiles(workspaceID string) ([]workspaceDeviceFileWithPath, error) {
	dir, err := defaultWorkspaceDevicesDir(workspaceID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	devices := make([]workspaceDeviceFileWithPath, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		device, err := loadWorkspaceDeviceFile(path)
		if err != nil {
			return nil, err
		}
		devices = append(devices, workspaceDeviceFileWithPath{path: path, device: device})
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].device.CreatedAt.Before(devices[j].device.CreatedAt)
	})
	return devices, nil
}

func loadWorkspaceDeviceFile(path string) (workspaceDeviceFile, error) {
	if err := checkWorkspaceDeviceFileMode(path); err != nil {
		return workspaceDeviceFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return workspaceDeviceFile{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var device workspaceDeviceFile
	if err := decoder.Decode(&device); err != nil {
		return workspaceDeviceFile{}, fmt.Errorf("decode workspace device file %s: %w", path, err)
	}
	if device.Version != workspaceDeviceFileVersion {
		return workspaceDeviceFile{}, fmt.Errorf("unsupported workspace device file version %d", device.Version)
	}
	if _, err := normalizeWorkspaceDeviceKind(device.Kind); err != nil {
		return workspaceDeviceFile{}, err
	}
	return device, nil
}

func checkWorkspaceDeviceFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("workspace device file %s must not be readable by group or others", path)
	}
	return nil
}

func loadWorkspaceDeviceWebTTYIdentity(device workspaceDeviceFile) (*webtty.E2EIdentity, error) {
	if strings.TrimSpace(device.WebTTYPublicKey) == "" || strings.TrimSpace(device.WebTTYKeyID) == "" {
		return nil, fmt.Errorf("workspace device %s has no WebTTY E2E identity; re-enroll this device", device.DeviceKeyID)
	}
	if device.WebTTYKeyAlgorithm != workspaceDeviceWebTTYCryptoSuite {
		return nil, fmt.Errorf("workspace device %s uses unsupported WebTTY key algorithm %q", device.DeviceKeyID, device.WebTTYKeyAlgorithm)
	}
	path := strings.TrimSpace(device.WebTTYIdentityPath)
	if path == "" {
		var err error
		path, err = defaultWorkspaceWebTTYDeviceIdentityPath(device.WorkspaceID, device.DeviceKeyID)
		if err != nil {
			return nil, err
		}
	}
	identity, err := webtty.LoadE2EIdentityFile(path)
	if err != nil {
		return nil, err
	}
	publicKey := webtty.EncodeE2EKeyMaterial(identity.PublicKey)
	keyID := webtty.EncodeE2EKeyMaterial(identity.KeyID)
	if publicKey != device.WebTTYPublicKey || keyID != device.WebTTYKeyID {
		return nil, fmt.Errorf("workspace device %s WebTTY identity does not match device metadata", device.DeviceKeyID)
	}
	return identity, nil
}

func workspaceDeviceWebTTYEndpointIdentity(device workspaceDeviceFile) (*webtty.WebTTYEndpointIdentity, error) {
	encryption, err := loadWorkspaceDeviceWebTTYIdentity(device)
	if err != nil {
		return nil, err
	}
	signingPrivateKeyDER, err := base64.RawURLEncoding.DecodeString(device.PrivateSigningKey)
	if err != nil {
		return nil, fmt.Errorf("decode workspace device signing key: %w", err)
	}
	signingPrivateKey, err := webtty.ParseWebTTYSigningPrivateKey(signingPrivateKeyDER)
	if err != nil {
		return nil, fmt.Errorf("parse workspace device signing key for WebTTY: %w", err)
	}
	signingPublicKeyDER, err := webtty.MarshalWebTTYSigningPublicKey(&signingPrivateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace device signing key for WebTTY: %w", err)
	}
	expectedSigningPublicKeyDER, err := base64.RawURLEncoding.DecodeString(device.PublicSigningKey)
	if err != nil {
		return nil, fmt.Errorf("decode workspace device public signing key: %w", err)
	}
	if !bytes.Equal(signingPublicKeyDER, expectedSigningPublicKeyDER) {
		return nil, fmt.Errorf("workspace device %s signing key does not match device metadata", device.DeviceKeyID)
	}
	return &webtty.WebTTYEndpointIdentity{
		Encryption: *encryption,
		Signing: webtty.WebTTYSigningIdentity{
			KeyID:      webtty.WebTTYSigningKeyID(signingPublicKeyDER),
			PublicKey:  signingPublicKeyDER,
			PrivateKey: signingPrivateKeyDER,
		},
	}, nil
}

func defaultWorkspaceDevicesDir(workspaceID string) (string, error) {
	if workspaceID == "." || workspaceID == ".." || strings.ContainsAny(workspaceID, `/\`) || strings.TrimSpace(workspaceID) == "" {
		return "", fmt.Errorf("workspace ID contains unsupported characters")
	}
	root, err := defaultRstreamHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "workspaces", workspaceID, "devices"), nil
}

func defaultWorkspaceWebTTYDeviceIdentityPath(workspaceID string, deviceKeyID string) (string, error) {
	if workspaceID == "." || workspaceID == ".." || strings.ContainsAny(workspaceID, `/\`) || strings.TrimSpace(workspaceID) == "" {
		return "", fmt.Errorf("workspace ID contains unsupported characters")
	}
	if deviceKeyID == "." || deviceKeyID == ".." || strings.ContainsAny(deviceKeyID, `/\`) || strings.TrimSpace(deviceKeyID) == "" {
		return "", fmt.Errorf("workspace device key ID contains unsupported characters")
	}
	root, err := defaultRstreamHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "workspaces", workspaceID, "webtty", "identities", deviceKeyID+".identity.json"), nil
}

func defaultWorkspaceDeviceLabel(kind string) string {
	hostname, err := os.Hostname()
	suffix := strings.ToUpper(kind)
	if err != nil || strings.TrimSpace(hostname) == "" {
		return suffix
	}
	return hostname + " " + suffix
}

func workspaceDeviceOutput(path string, device workspaceDeviceFile, code string) map[string]any {
	output := map[string]any{
		"workspace_id":      device.WorkspaceID,
		"device_id":         device.DeviceKeyID,
		"kind":              device.Kind,
		"label":             device.Label,
		"status":            device.Status,
		"fingerprint":       device.Fingerprint,
		"verification_code": code,
		"device_file":       path,
		"webtty_key_id":     device.WebTTYKeyID,
		"webtty_identity":   device.WebTTYIdentityPath,
		"has_key_envelope":  device.DeviceEnvelope != nil,
		"rotates_device_id": device.RotatesDeviceKeyID,
	}
	if device.RotationCompletedAt != nil {
		output["rotation_completed_at"] = device.RotationCompletedAt.Format(time.RFC3339)
	}
	return output
}

func workspaceDeviceIsActive(device workspaceDeviceFile) bool {
	return device.Status == workspaceDeviceStatusActive && device.DeviceEnvelope != nil
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
	webttypb "github.com/rstreamlabs/rstream-go/webtty/pb"
	"github.com/spf13/cobra"
)

func TestRunWebTTYServerCreateInfersActiveProject(t *testing.T) {
	clearRstreamTestEnv(t)
	responsePayload := testCreateWebTTYServerResponse()
	var seen controlplane.CreateWebTTYServerRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/resolve/demo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.Project{ID: "project-1", WorkspaceID: "workspace-1", Endpoint: "demo", Routing: "regional"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			if r.URL.Query().Get("q") != "Prod Shell" || r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected existing-server query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			if got := r.Header.Get("Authorization"); got != "Bearer default-token" {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responsePayload)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	writeWorkspaceDeviceTestConfig(t, server.URL, "demo")
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	if err := cmd.Flags().Set("encryption-policy", webTTYServerEncryptionPolicyExplicitKey); err != nil {
		t.Fatalf("set encryption-policy: %v", err)
	}
	if err := cmd.Flags().Set("label", "role=ops"); err != nil {
		t.Fatalf("set label: %v", err)
	}
	if err := runWebTTYServerCreate(cmd, "Prod Shell"); err != nil {
		t.Fatalf("runWebTTYServerCreate() error = %v", err)
	}
	if seen.Name != "Prod Shell" || seen.EncryptionPolicy != webTTYServerEncryptionPolicyExplicitKey || seen.Labels["role"] != "ops" {
		t.Fatalf("unexpected create payload: %#v", seen)
	}
	text := out.String()
	for _, want := range []string{
		"Registered WebTTY server created",
		"Project: project-1 (from active project demo)",
		"Enroll command:",
		"Run command:",
		"rstream --api-url " + server.URL + " webtty server enroll prod-shell --project-id project-1",
		"rstream webtty server -v --server-id prod-shell --login-user <os-user>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("create output missing %q: %s", want, text)
		}
	}
}

func TestRunWebTTYServerCreateJSONIncludesActionableCommands(t *testing.T) {
	clearRstreamTestEnv(t)
	responsePayload := testCreateWebTTYServerResponse()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --project-id is set", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			if r.URL.Query().Get("q") != "Prod Shell" || r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected existing-server query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responsePayload)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	configPath := writeTestGlobalProjectConfig(t)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "project-id", "project-1")
	mustSetFlag(t, cmd, "output", "json")
	stdout, err := captureStdout(t, func() error {
		return runWebTTYServerCreate(cmd, "Prod Shell")
	})
	if err != nil {
		t.Fatalf("runWebTTYServerCreate() error = %v", err)
	}
	var decoded struct {
		APIURL        string `json:"api_url"`
		EnrollCommand string `json:"enroll_command"`
		RunCommand    string `json:"run_command"`
		Server        struct {
			ID string `json:"id"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("decode create json output: %v\n%s", err, stdout)
	}
	if decoded.APIURL != server.URL {
		t.Fatalf("json api_url = %q, want %q", decoded.APIURL, server.URL)
	}
	if decoded.Server.ID != "prod-shell" {
		t.Fatalf("json server id = %q", decoded.Server.ID)
	}
	wantEnroll := "rstream --api-url " + server.URL + " webtty server enroll prod-shell --project-id project-1"
	if decoded.EnrollCommand != wantEnroll {
		t.Fatalf("json enroll_command = %q, want %q", decoded.EnrollCommand, wantEnroll)
	}
	if decoded.RunCommand != "rstream webtty server -v --server-id prod-shell --login-user <os-user>" {
		t.Fatalf("json run_command = %q", decoded.RunCommand)
	}
}

func TestRunWebTTYServerRegisteredCommandsWithProjectID(t *testing.T) {
	clearRstreamTestEnv(t)
	t.Setenv("RSTREAM_API_URL", "")
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	configPath := writeTestGlobalProjectConfig(t)
	t.Setenv("RSTREAM_CONFIG", configPath)
	responsePayload := testCreateWebTTYServerResponse()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --project-id is set", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["list"] = true
			if r.URL.Query().Get("status") != "active" || r.URL.Query().Get("pageSize") != "5" {
				http.Error(w, "unexpected list query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{responsePayload.Server}, Page: 1, PageSize: 5, Total: 1, TotalPages: 1})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell":
			seen["show"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(responsePayload.Server)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell":
			seen["delete"] = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	listCmd := newTestWebTTYServerListAdminCommand(&out)
	listCmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, listCmd, server.URL, configPath)
	mustSetFlag(t, listCmd, "project-id", "project-1")
	mustSetFlag(t, listCmd, "status", "active")
	mustSetFlag(t, listCmd, "page-size", "5")
	if err := runWebTTYServerRegisteredList(listCmd); err != nil {
		t.Fatalf("runWebTTYServerRegisteredList() error = %v", err)
	}
	if !strings.Contains(out.String(), "Prod Shell") || !strings.Contains(out.String(), "prod-shell") {
		t.Fatalf("unexpected list output: %q", out.String())
	}
	out.Reset()
	showCmd := newTestWebTTYServerShowAdminCommand(&out)
	showCmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, showCmd, server.URL, configPath)
	mustSetFlag(t, showCmd, "project-id", "project-1")
	if err := runWebTTYServerShow(showCmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYServerShow() error = %v", err)
	}
	if !strings.Contains(out.String(), "Encryption") || !strings.Contains(out.String(), webTTYServerEncryptionPolicyExplicitKey) {
		t.Fatalf("unexpected show output: %q", out.String())
	}
	deleteCmd := newTestWebTTYServerDeleteAdminCommand(&out)
	deleteCmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, deleteCmd, server.URL, configPath)
	mustSetFlag(t, deleteCmd, "project-id", "project-1")
	if err := runWebTTYServerDelete(deleteCmd, "prod-shell"); err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("delete should require --yes, got %v", err)
	}
	mustSetFlag(t, deleteCmd, "yes", "true")
	if err := runWebTTYServerDelete(deleteCmd, "prod-shell"); err != nil {
		t.Fatalf("runWebTTYServerDelete() error = %v", err)
	}
	for _, key := range []string{"list", "show", "delete"} {
		if !seen[key] {
			t.Fatalf("command %q did not call the control-plane API", key)
		}
	}
}

func TestRunWebTTYServerCreateWithProjectIDIgnoresGlobalProjectContext(t *testing.T) {
	clearRstreamTestEnv(t)
	responsePayload := testCreateWebTTYServerResponse()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --project-id is set", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["list"] = true
			if r.URL.Query().Get("q") != "Prod Shell" {
				http.Error(w, "unexpected query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["create"] = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responsePayload)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	configPath := writeTestGlobalProjectConfig(t)
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "runtime-token")
	mustSetFlag(t, cmd, "project-id", "project-1")
	if err := runWebTTYServerCreate(cmd, "Prod Shell"); err != nil {
		t.Fatalf("runWebTTYServerCreate() error = %v", err)
	}
	for _, key := range []string{"list", "create"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	if !strings.Contains(out.String(), "Project: project-1") {
		t.Fatalf("create output should use explicit project id: %q", out.String())
	}
}

func TestRunWebTTYServerRegisteredListRequiresProjectContext(t *testing.T) {
	clearRstreamTestEnv(t)
	t.Setenv("RSTREAM_API_URL", "https://api.example.com")
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWebTTYServerListAdminCommand(&out)
	cmd.SetContext(t.Context())
	err := runWebTTYServerRegisteredList(cmd)
	if err == nil || !strings.Contains(err.Error(), "rstream project list") || !strings.Contains(err.Error(), "rstream project use") {
		t.Fatalf("expected actionable project discovery error, got %v", err)
	}
}

func TestRunWebTTYServerCreateCanEnrollLocally(t *testing.T) {
	clearRstreamTestEnv(t)
	responsePayload := testCreateWebTTYServerResponse()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			if r.URL.Query().Get("q") != "Prod Shell" || r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected existing-server query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["create"] = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responsePayload)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell":
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(responsePayload.Server)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell/enroll":
			seen["enroll"] = true
			var req controlplane.EnrollWebTTYServerRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if req.ServerPublicKey == "" || req.ServerSigningPublicKey == "" {
				http.Error(w, "invalid enrollment", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               responsePayload.Server.ID,
				WorkspaceID:      responsePayload.Server.WorkspaceID,
				ProjectID:        responsePayload.Server.ProjectID,
				Name:             responsePayload.Server.Name,
				Status:           webTTYServerStatusActive,
				RecordingPolicy:  responsePayload.Server.RecordingPolicy,
				EncryptionPolicy: responsePayload.Server.EncryptionPolicy,
				AccessPolicy:     responsePayload.Server.AccessPolicy,
				CreatedAt:        responsePayload.Server.CreatedAt,
				UpdatedAt:        responsePayload.Server.UpdatedAt,
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")
	enrollmentPath := filepath.Join(dir, "enrollment.yaml")
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	mustSetFlag(t, cmd, "project-id", "project-1")
	mustSetFlag(t, cmd, "enroll", "true")
	mustSetFlag(t, cmd, "identity-file", identityPath)
	mustSetFlag(t, cmd, "server-enrollment", enrollmentPath)
	if err := runWebTTYServerCreate(cmd, "Prod Shell"); err != nil {
		t.Fatalf("runWebTTYServerCreate() error = %v", err)
	}
	for _, key := range []string{"create", "get", "enroll"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		t.Fatalf("loadWebTTYServerEnrollmentFile() error = %v", err)
	}
	if enrollment.ServerID != "prod-shell" || enrollment.ProjectID != "project-1" {
		t.Fatalf("unexpected enrollment: %#v", enrollment)
	}
	if !strings.Contains(out.String(), "Local enrollment: "+enrollmentPath) {
		t.Fatalf("create --enroll output missing local enrollment: %q", out.String())
	}
}

func TestRunWebTTYServerCreateWorkspaceManagedEnrollRequiresTrustedDeviceBeforeCreate(t *testing.T) {
	clearRstreamTestEnv(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	seenCreate := false
	seenList := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{
				Projects: []controlplane.Project{{
					ID:          "project-1",
					WorkspaceID: "workspace-1",
					Endpoint:    "demo",
					Routing:     "regional",
				}},
				Page:       1,
				PageSize:   100,
				Total:      1,
				TotalPages: 1,
			})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seenList = true
			http.Error(w, "server list should not be called before trusted-device preflight", http.StatusBadRequest)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seenCreate = true
			http.Error(w, "create should not be called before trusted-device preflight", http.StatusBadRequest)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	mustSetFlag(t, cmd, "project-id", "project-1")
	mustSetFlag(t, cmd, "enroll", "true")
	mustSetFlag(t, cmd, "encryption-policy", webTTYServerEncryptionPolicyWorkspaceManaged)
	err := runWebTTYServerCreate(cmd, "Workspace Shell")
	if err == nil {
		t.Fatal("expected trusted-device preflight error")
	}
	for _, want := range []string{
		"workspace-managed WebTTY servers require this machine to be a trusted workspace device before creation",
		"rstream workspace device enroll --workspace workspace-1",
		"no active trusted workspace device",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("trusted-device preflight error missing %q: %s", want, err)
		}
	}
	if seenList || seenCreate {
		t.Fatalf("server APIs called before trusted-device preflight: list=%v create=%v", seenList, seenCreate)
	}
}

func TestRunWebTTYServerCreateEnrollAutoPinsWorkspaceManagedTrust(t *testing.T) {
	clearRstreamTestEnv(t)
	projectID := "project-1"
	serverID := "workspace-shell"
	workspaceID := "workspace-1"
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	material, err := generateWorkspaceDeviceMaterial(workspaceID, workspaceDeviceKindService, "WebTTY Server")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	material.file.DeviceKeyID = "device-1"
	material.file.Status = workspaceDeviceStatusActive
	material.file.CreatedAt = now
	material.file.UpdatedAt = now
	if _, err := writeWorkspaceDeviceFile(material.file); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile() error = %v", err)
	}
	keysetPrivate, keysetPublic, bundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, material.file, "keyset-1")
	material.file.DeviceEnvelope = &envelope
	pending := controlplane.WebTTYServer{
		ID:               serverID,
		WorkspaceID:      workspaceID,
		ProjectID:        projectID,
		Name:             "Workspace Shell",
		Status:           webTTYServerStatusPendingEnrollment,
		RecordingPolicy:  webTTYServerRecordingPolicyRecorded,
		EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
		AccessPolicy:     webTTYServerAccessPolicyProjectMembers,
		CreatedAt:        "2026-06-06T12:00:00.000Z",
		UpdatedAt:        "2026-06-06T12:00:00.000Z",
	}
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --project-id is set", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels":
			if r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected project list query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{
				Projects: []controlplane.Project{{
					ID:          projectID,
					WorkspaceID: workspaceID,
					Endpoint:    "runtime-project",
					Routing:     "regional",
				}},
				Page:       1,
				PageSize:   100,
				Total:      1,
				TotalPages: 1,
			})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["list"] = true
			if r.URL.Query().Get("q") != "Workspace Shell" || r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected existing-server query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["create"] = true
			var req controlplane.CreateWebTTYServerRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if req.Name != pending.Name || req.EncryptionPolicy != webTTYServerEncryptionPolicyWorkspaceManaged {
				http.Error(w, "unexpected create payload", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(controlplane.CreateWebTTYServerResponse{Server: pending})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/workspace-shell":
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pending)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			seen["lookup"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  material.file.DeviceKeyID,
					Kind:                material.file.Kind,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: material.file.PublicEncryptionKey,
					PublicSigningKey:    &material.file.PublicSigningKey,
					Fingerprint:         material.file.Fingerprint,
					CreatedAt:           "2026-06-06T12:00:00.000Z",
				}},
				DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{envelope},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/workspace-shell/enroll":
			seen["enroll"] = true
			var req controlplane.EnrollWebTTYServerRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if req.ServerPublicKey == "" || req.ServerSigningKeyID == "" || req.ServerSigningPublicKey == "" {
				http.Error(w, "invalid enrollment payload", http.StatusBadRequest)
				return
			}
			active := pending
			active.Status = webTTYServerStatusActive
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(active)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/workspace-shell/workspace-trust":
			seen["trust"] = true
			var req controlplane.ApproveWebTTYServerWorkspaceTrustRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			enrollmentPath, _ := defaultWebTTYServerEnrollmentPath(serverID)
			enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload := workspaceWebTTYServerTrustApprovalPayload(enrollment, envelope.KeysetID, material.file.DeviceKeyID, req.SignedAt)
			if req.ActorDeviceKeyID != material.file.DeviceKeyID ||
				req.KeysetID != envelope.KeysetID ||
				verifyWorkspaceP256Signature(material.file.PublicSigningKey, payload, req.ActorSignature, "actor") != nil ||
				verifyWorkspaceP256Signature(keysetPublic, payload, req.KeysetSignature, "keyset") != nil {
				http.Error(w, "invalid trust payload", http.StatusBadRequest)
				return
			}
			active := pending
			active.Status = webTTYServerStatusActive
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(active)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", writeTestGlobalProjectConfig(t))
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	mustSetFlag(t, cmd, "project-id", projectID)
	mustSetFlag(t, cmd, "enroll", "true")
	mustSetFlag(t, cmd, "encryption-policy", webTTYServerEncryptionPolicyWorkspaceManaged)
	if err := runWebTTYServerCreate(cmd, pending.Name); err != nil {
		t.Fatalf("runWebTTYServerCreate() error = %v", err)
	}
	for _, key := range []string{"list", "lookup", "create", "get", "enroll", "trust"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	enrollmentPath, _ := defaultWebTTYServerEnrollmentPath(serverID)
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		t.Fatalf("loadWebTTYServerEnrollmentFile() error = %v", err)
	}
	if enrollment.WorkspaceTrustKeysetID != envelope.KeysetID ||
		enrollment.WorkspaceTrustKeysetFingerprint != bundle.Fingerprint ||
		enrollment.WorkspaceTrustPublicSigningKey != bundle.PublicSigningKey {
		t.Fatalf("workspace trust pins were not persisted: %#v", enrollment)
	}
	writeTestWorkspaceDeviceWithWebTTYIdentity(t, material.file, material.webttyIdentity)
	currentDevice := testWebTTYCurrentDeviceResolution(t, material.file, keysetPrivate, bundle)
	credential, err := webTTYWorkspaceClientCredential(material.file, controlplane.ResolveWebTTYServerClientResponse{
		ServerID:         enrollment.ServerID,
		WorkspaceID:      enrollment.WorkspaceID,
		ProjectID:        enrollment.ProjectID,
		EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
		E2ERequired:      true,
		CurrentDevice:    currentDevice,
	})
	if err != nil {
		t.Fatalf("webTTYWorkspaceClientCredential() error = %v", err)
	}
	endpointIdentity, err := workspaceDeviceWebTTYEndpointIdentity(material.file)
	if err != nil {
		t.Fatalf("workspaceDeviceWebTTYEndpointIdentity() error = %v", err)
	}
	verifiedKey, err := verifyWebTTYWorkspaceClientCredential(enrollment, webtty.ClientProofVerification{
		Proof: &webttypb.ClientProof{
			SigningKeyId:     endpointIdentity.Signing.KeyID,
			SigningPublicKey: endpointIdentity.Signing.PublicKey,
		},
		Credential: credential,
	})
	if err != nil {
		t.Fatalf("workspace-managed verifier rejected credential from create --enroll enrollment: %v", err)
	}
	if !bytes.Equal(verifiedKey, endpointIdentity.Signing.PublicKey) {
		t.Fatalf("verified client signing key mismatch")
	}
	if !strings.Contains(out.String(), "Workspace trust: pinned") {
		t.Fatalf("create --enroll output missing workspace trust status: %q", out.String())
	}
}

func TestRunWebTTYServerCreateSurfacesExistingNameWithCommands(t *testing.T) {
	clearRstreamTestEnv(t)
	responsePayload := testCreateWebTTYServerResponse()
	seenPost := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			if r.URL.Query().Get("q") != "Prod Shell" || r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected existing-server query", http.StatusBadRequest)
				return
			}
			active := responsePayload.Server
			active.Status = webTTYServerStatusActive
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{active}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seenPost = true
			http.Error(w, "create should not be called", http.StatusBadRequest)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	mustSetFlag(t, cmd, "project-id", "project-1")
	err := runWebTTYServerCreate(cmd, "Prod Shell")
	if err == nil {
		t.Fatal("expected existing-name error")
	}
	text := err.Error()
	for _, want := range []string{
		`registered WebTTY server "Prod Shell" already exists`,
		"rstream webtty server show prod-shell",
		"rstream webtty server delete prod-shell --yes",
		"choose another name",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("existing-name error missing %q: %s", want, text)
		}
	}
	if seenPost {
		t.Fatal("create API was called despite existing server preflight")
	}
}

func TestRunWebTTYServerCreateEnrollReusesCompatiblePendingServer(t *testing.T) {
	clearRstreamTestEnv(t)
	responsePayload := testCreateWebTTYServerResponse()
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			seen["list"] = true
			if r.URL.Query().Get("q") != "Prod Shell" || r.URL.Query().Get("pageSize") != "100" {
				http.Error(w, "unexpected existing-server query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{responsePayload.Server}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			http.Error(w, "create should not be called", http.StatusBadRequest)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell":
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(responsePayload.Server)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell/enroll":
			seen["enroll"] = true
			w.Header().Set("Content-Type", "application/json")
			active := responsePayload.Server
			active.Status = webTTYServerStatusActive
			_ = json.NewEncoder(w).Encode(active)
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	dir := t.TempDir()
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	mustSetFlag(t, cmd, "project-id", "project-1")
	mustSetFlag(t, cmd, "enroll", "true")
	mustSetFlag(t, cmd, "encryption-policy", webTTYServerEncryptionPolicyExplicitKey)
	mustSetFlag(t, cmd, "identity-file", filepath.Join(dir, "identity.json"))
	mustSetFlag(t, cmd, "server-enrollment", filepath.Join(dir, "enrollment.yaml"))
	if err := runWebTTYServerCreate(cmd, "Prod Shell"); err != nil {
		t.Fatalf("runWebTTYServerCreate() error = %v", err)
	}
	for _, key := range []string{"list", "get", "enroll"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	if !strings.Contains(out.String(), "Registered WebTTY server resumed and enrolled") {
		t.Fatalf("expected resumed output, got %q", out.String())
	}
}

func TestRunWebTTYServerCreateMapsAPIRaceConflictWithCommands(t *testing.T) {
	clearRstreamTestEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.ListWebTTYServersResponse{Servers: []controlplane.WebTTYServer{}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "A WebTTY server with this name already exists in this project."})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	t.Setenv("RSTREAM_API_URL", server.URL)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out bytes.Buffer
	cmd := newTestWebTTYServerCreateAdminCommand(&out)
	cmd.SetContext(t.Context())
	mustSetFlag(t, cmd, "project-id", "project-1")
	err := runWebTTYServerCreate(cmd, "Prod Shell")
	if err == nil {
		t.Fatal("expected conflict error")
	}
	text := err.Error()
	for _, want := range []string{
		`registered WebTTY server "Prod Shell" already exists`,
		"rstream webtty server list --project-id project-1 --q 'Prod Shell'",
		"rstream webtty server delete <server-id> --project-id project-1 --yes",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("conflict error missing %q: %s", want, text)
		}
	}
}

func TestMapWebTTYServerPermissionErrorsAreActionable(t *testing.T) {
	readErr := mapWebTTYServerReadError(controlplane.ErrForbidden)
	if readErr == nil ||
		!strings.Contains(readErr.Error(), "network.webtty-servers.read-only") ||
		!strings.Contains(readErr.Error(), "rstream login") {
		t.Fatalf("unexpected read error: %v", readErr)
	}
	writeErr := mapWebTTYServerWriteError(controlplane.ErrForbidden)
	if writeErr == nil ||
		!strings.Contains(writeErr.Error(), "network.webtty-servers.read-write") ||
		!strings.Contains(writeErr.Error(), "rstream login") {
		t.Fatalf("unexpected write error: %v", writeErr)
	}
	authErr := mapWebTTYServerWriteError(controlplane.ErrUnauthorized)
	if authErr == nil || !strings.Contains(authErr.Error(), "not authenticated") {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
}

func TestRunWebTTYServerTrustPinsWorkspaceKeysetFromTrustedDevice(t *testing.T) {
	clearRstreamTestEnv(t)
	enrollment, _ := testWebTTYWorkspaceTrustEnrollment(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	enrollmentPath := filepath.Join(home, "prod-shell.yaml")
	if err := writeWebTTYServerEnrollmentFile(enrollmentPath, *enrollment); err != nil {
		t.Fatalf("writeWebTTYServerEnrollmentFile() error = %v", err)
	}
	material, err := generateWorkspaceDeviceMaterial(enrollment.WorkspaceID, workspaceDeviceKindCLI, "Local CLI")
	if err != nil {
		t.Fatalf("generateWorkspaceDeviceMaterial() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	material.file.DeviceKeyID = "device-1"
	material.file.Status = workspaceDeviceStatusActive
	material.file.CreatedAt = now
	material.file.UpdatedAt = now
	if _, err := writeWorkspaceDeviceFile(material.file); err != nil {
		t.Fatalf("writeWorkspaceDeviceFile() error = %v", err)
	}
	_, keysetPublic, bundle, envelope := testWorkspaceKeyEnvelopeForDevice(t, material.file, "keyset-1")
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.EscapedPath(), "/resolve/global-project") {
			http.Error(w, "global project context must not be resolved when --project-id is set", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/workspaces/workspace-1/enterprise/devices/lookup":
			seen["lookup"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.LookupWorkspaceDeviceKeysResponse{
				Devices: []controlplane.WorkspaceDeviceKey{{
					ID:                  material.file.DeviceKeyID,
					Kind:                material.file.Kind,
					Status:              workspaceDeviceStatusActive,
					PublicEncryptionKey: material.file.PublicEncryptionKey,
					PublicSigningKey:    &material.file.PublicSigningKey,
					Fingerprint:         material.file.Fingerprint,
					CreatedAt:           "2026-06-06T12:00:00.000Z",
				}},
				DeviceEnvelopes: []controlplane.WorkspaceKeyEnvelope{envelope},
			})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/projects/tunnels/project-1/webtty/servers/prod-shell/workspace-trust":
			seen["trust"] = true
			var req controlplane.ApproveWebTTYServerWorkspaceTrustRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			currentEnrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload := workspaceWebTTYServerTrustApprovalPayload(currentEnrollment, envelope.KeysetID, material.file.DeviceKeyID, req.SignedAt)
			if req.ActorDeviceKeyID != material.file.DeviceKeyID ||
				req.KeysetID != envelope.KeysetID ||
				verifyWorkspaceP256Signature(material.file.PublicSigningKey, payload, req.ActorSignature, "actor") != nil ||
				verifyWorkspaceP256Signature(keysetPublic, payload, req.KeysetSignature, "keyset") != nil {
				http.Error(w, "invalid trust payload", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(controlplane.WebTTYServer{
				ID:               enrollment.ServerID,
				WorkspaceID:      enrollment.WorkspaceID,
				ProjectID:        enrollment.ProjectID,
				Name:             "Shell",
				Status:           "active",
				RecordingPolicy:  "recorded",
				EncryptionPolicy: webTTYServerEncryptionPolicyWorkspaceManaged,
				AccessPolicy:     "project_members",
				CreatedAt:        "2026-06-06T12:00:00.000Z",
				UpdatedAt:        "2026-06-06T12:00:00.000Z",
			})
		default:
			http.Error(w, "unexpected request "+r.URL.EscapedPath(), http.StatusBadRequest)
		}
	}))
	defer server.Close()
	configPath := writeTestGlobalProjectConfig(t)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", configPath)
	var out bytes.Buffer
	cmd := newTestWebTTYServerTrustAdminCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "server-enrollment", enrollmentPath)
	mustSetFlag(t, cmd, "project-id", enrollment.ProjectID)
	if err := runWebTTYServerTrust(cmd, enrollment.ServerID); err != nil {
		t.Fatalf("runWebTTYServerTrust() error = %v", err)
	}
	for _, key := range []string{"lookup", "trust"} {
		if !seen[key] {
			t.Fatalf("missing %s API call", key)
		}
	}
	updated, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		t.Fatalf("loadWebTTYServerEnrollmentFile() error = %v", err)
	}
	if updated.WorkspaceTrustKeysetID != envelope.KeysetID ||
		updated.WorkspaceTrustKeysetFingerprint != bundle.Fingerprint ||
		updated.WorkspaceTrustPublicSigningKey != bundle.PublicSigningKey {
		t.Fatalf("workspace trust pins were not persisted: %#v", updated)
	}
	if !strings.Contains(out.String(), "Workspace trust pinned") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunWebTTYServerTrustRejectsLocalEnrollmentMismatchBeforeControlPlane(t *testing.T) {
	clearRstreamTestEnv(t)
	cases := []struct {
		name       string
		serverID   string
		projectID  string
		mutate     func(*webTTYServerEnrollmentFile)
		wantError  string
		wantNoHTTP bool
	}{
		{
			name:       "server id mismatch",
			serverID:   "other-shell",
			wantError:  "belongs to server \"prod-shell\"",
			wantNoHTTP: true,
		},
		{
			name:      "project id mismatch",
			serverID:  "prod-shell",
			projectID: "other-project",
			wantError: "belongs to project project-1, but --project-id targets other-project",
		},
		{
			name:     "non workspace managed enrollment",
			serverID: "prod-shell",
			mutate: func(enrollment *webTTYServerEnrollmentFile) {
				enrollment.EncryptionPolicy = webTTYServerEncryptionPolicyExplicitKey
			},
			wantError:  "workspace trust pins require a workspace-managed WebTTY server enrollment",
			wantNoHTTP: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enrollment, _ := testWebTTYWorkspaceTrustEnrollment(t)
			if tc.mutate != nil {
				tc.mutate(enrollment)
			}
			home := t.TempDir()
			setWorkspaceDeviceTestHome(t, home)
			enrollmentPath := filepath.Join(home, "prod-shell.yaml")
			if err := writeWebTTYServerEnrollmentFile(enrollmentPath, *enrollment); err != nil {
				t.Fatalf("writeWebTTYServerEnrollmentFile() error = %v", err)
			}
			calledHTTP := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calledHTTP = true
				http.Error(w, "control plane must not be called for invalid local enrollment", http.StatusBadRequest)
			}))
			defer server.Close()
			configPath := writeTestGlobalProjectConfig(t)
			t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
			t.Setenv("RSTREAM_CONFIG", configPath)
			var out bytes.Buffer
			cmd := newTestWebTTYServerTrustAdminCommand(&out)
			cmd.SetContext(t.Context())
			addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
			mustSetFlag(t, cmd, "server-enrollment", enrollmentPath)
			if tc.projectID != "" {
				mustSetFlag(t, cmd, "project-id", tc.projectID)
			}
			err := runWebTTYServerTrust(cmd, tc.serverID)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("runWebTTYServerTrust() error = %v, want containing %q", err, tc.wantError)
			}
			if tc.wantNoHTTP && calledHTTP {
				t.Fatalf("control plane was called for invalid local enrollment")
			}
		})
	}
}

func TestRunWebTTYServerTrustRequiresTrustedDeviceBeforeApproval(t *testing.T) {
	clearRstreamTestEnv(t)
	enrollment, _ := testWebTTYWorkspaceTrustEnrollment(t)
	home := t.TempDir()
	setWorkspaceDeviceTestHome(t, home)
	enrollmentPath := filepath.Join(home, "prod-shell.yaml")
	if err := writeWebTTYServerEnrollmentFile(enrollmentPath, *enrollment); err != nil {
		t.Fatalf("writeWebTTYServerEnrollmentFile() error = %v", err)
	}
	calledHTTP := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledHTTP = true
		http.Error(w, "control plane must not be called before a trusted workspace device exists", http.StatusBadRequest)
	}))
	defer server.Close()
	configPath := writeTestGlobalProjectConfig(t)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "token")
	t.Setenv("RSTREAM_CONFIG", configPath)
	var out bytes.Buffer
	cmd := newTestWebTTYServerTrustAdminCommand(&out)
	cmd.SetContext(t.Context())
	addTestControlPlaneOverrideFlags(t, cmd, server.URL, configPath)
	mustSetFlag(t, cmd, "server-enrollment", enrollmentPath)
	mustSetFlag(t, cmd, "project-id", enrollment.ProjectID)
	err := runWebTTYServerTrust(cmd, enrollment.ServerID)
	if err == nil {
		t.Fatalf("expected trusted workspace device error")
	}
	for _, want := range []string{
		"workspace-managed WebTTY servers require this machine to be a trusted workspace device before enrollment",
		"rstream workspace device enroll --workspace workspace-1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if calledHTTP {
		t.Fatalf("control plane was called before a trusted workspace device existed")
	}
}

func TestWebTTYServerRegisteredHelpDoesNotExposeInternalAliases(t *testing.T) {
	var out bytes.Buffer
	webttyServerCmd.SetOut(&out)
	defer webttyServerCmd.SetOut(nil)
	if err := webttyServerCmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"create",
		"list",
		"show",
		"delete",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("server help missing command %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{"server-binding", "e2e-policy", "no-heartbeat"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("server help exposes noisy flag %q: %s", forbidden, text)
		}
	}
}

func testCreateWebTTYServerResponse() controlplane.CreateWebTTYServerResponse {
	publicKey := "public-key"
	fingerprint := "sha256:fingerprint"
	keyAlgorithm := "webtty-x25519-hpke-v1"
	return controlplane.CreateWebTTYServerResponse{
		Server: controlplane.WebTTYServer{
			ID:                 "prod-shell",
			WorkspaceID:        "workspace-1",
			ProjectID:          "project-1",
			Name:               "Prod Shell",
			Status:             "pending_enrollment",
			RecordingPolicy:    webTTYServerRecordingPolicyRecorded,
			EncryptionPolicy:   webTTYServerEncryptionPolicyExplicitKey,
			AccessPolicy:       webTTYServerAccessPolicyProjectMembers,
			ServerPublicKey:    &publicKey,
			ServerFingerprint:  &fingerprint,
			ServerKeyAlgorithm: &keyAlgorithm,
			CreatedAt:          "2026-06-06T12:00:00.000Z",
			UpdatedAt:          "2026-06-06T12:00:00.000Z",
		},
	}
}

func newTestWebTTYServerCreateAdminCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "create"}
	cmd.SetOut(out)
	addWebTTYRegisteredServerProjectFlag(cmd)
	cmd.Flags().String("description", "", "")
	cmd.Flags().String("recording-policy", webTTYServerRecordingPolicyRecorded, "")
	cmd.Flags().String("encryption-policy", webTTYServerEncryptionPolicyDisabled, "")
	cmd.Flags().String("access-policy", webTTYServerAccessPolicyProjectMembers, "")
	cmd.Flags().StringArray("label", nil, "")
	cmd.Flags().Bool("enroll", false, "")
	cmd.Flags().String("identity-file", "", "")
	cmd.Flags().String("server-enrollment", "", "")
	cmd.Flags().StringP("output", "o", webTTYServerAdminOutputText, "")
	return cmd
}

func newTestWebTTYServerListAdminCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.SetOut(out)
	addWebTTYRegisteredServerProjectFlag(cmd)
	cmd.Flags().String("q", "", "")
	cmd.Flags().String("status", "", "")
	cmd.Flags().Int("page", 0, "")
	cmd.Flags().Int("page-size", 0, "")
	cmd.Flags().StringP("output", "o", webTTYServerAdminOutputTable, "")
	return cmd
}

func newTestWebTTYServerShowAdminCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "show"}
	cmd.SetOut(out)
	addWebTTYRegisteredServerProjectFlag(cmd)
	cmd.Flags().StringP("output", "o", webTTYServerAdminOutputTable, "")
	return cmd
}

func newTestWebTTYServerDeleteAdminCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "delete"}
	cmd.SetOut(out)
	addWebTTYRegisteredServerProjectFlag(cmd)
	cmd.Flags().Bool("yes", false, "")
	cmd.Flags().StringP("output", "o", webTTYServerAdminOutputText, "")
	return cmd
}

func newTestWebTTYServerTrustAdminCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "trust"}
	cmd.SetOut(out)
	addWebTTYRegisteredServerProjectFlag(cmd)
	cmd.Flags().String("server-enrollment", "", "")
	cmd.Flags().StringP("output", "o", webTTYServerAdminOutputText, "")
	return cmd
}

func addTestControlPlaneOverrideFlags(t *testing.T, cmd *cobra.Command, apiURL string, configPath string) {
	t.Helper()
	cmd.Flags().String("api-url", "", "")
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("context", "", "")
	mustSetFlag(t, cmd, "api-url", apiURL)
	mustSetFlag(t, cmd, "config", configPath)
}

func writeTestGlobalProjectConfig(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := `version: 1
defaults:
  context:
    name: global
contexts:
  - name: global
    apiUrl: https://wrong.example.com
    projectEndpoint: global-project
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

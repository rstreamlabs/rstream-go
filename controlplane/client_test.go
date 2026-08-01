// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClientOptionsAndRequireToken(t *testing.T) {
	httpClient := &http.Client{Timeout: time.Second}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient(" https://api.example.com/ ", " token ", WithHTTPClient(httpClient), WithLogger(logger))
	if client.apiURL != "https://api.example.com" || client.token != "token" {
		t.Fatalf("client fields not normalized: apiURL=%q token=%q", client.apiURL, client.token)
	}
	if client.httpClient == httpClient || client.httpClient.Timeout != httpClient.Timeout || client.logger != logger {
		t.Fatalf("client options not applied")
	}
	if client.httpClient.CheckRedirect == nil {
		t.Fatal("control plane client must reject redirects")
	}
	if err := client.RequireToken(); err != nil {
		t.Fatalf("RequireToken() error = %v", err)
	}
	if err := NewClient("https://api.example.com", " ").RequireToken(); err == nil {
		t.Fatalf("expected missing token error")
	}
}

func TestControlPlaneHeadersAndRedirectIsolation(t *testing.T) {
	var redirected atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer redirectTarget.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Deployment-Bypass"); got != "secret" {
			http.Error(w, "missing deployment header", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewClient(server.URL, "token", WithHeaders(map[string]string{"x-deployment-bypass": "secret"}))
	if _, err := client.Whoami(context.Background()); err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("Whoami() error = %v, want redirect response", err)
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target received %d requests", redirected.Load())
	}
	if _, err := NewClient(server.URL, "token", WithHeaders(map[string]string{"Authorization": "secret"})).Whoami(context.Background()); err == nil || !strings.Contains(err.Error(), "reserved control plane header") {
		t.Fatalf("Whoami() error = %v, want reserved header error", err)
	}
}

func TestControlPlaneHTMLResponsesAreTyped(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{name: "content type", contentType: "text/html; charset=utf-8", body: "<!doctype html><title>Protected</title>", status: http.StatusOK},
		{name: "body prefix", contentType: "text/plain", body: " \n<html><title>Protected</title>", status: http.StatusOK},
		{name: "forbidden", contentType: "text/html", body: "<html><title>Protected</title>", status: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "token").Whoami(t.Context()); !errors.Is(err, ErrAccessProtection) {
				t.Fatalf("Whoami() error = %v, want ErrAccessProtection", err)
			}
		})
	}
}

func TestWhoamiAuthorizationAndAuthErrorWrapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/whoami":
			if got := r.Header.Get("Authorization"); got != "Bearer token" {
				http.Error(w, "unexpected authorization", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Whoami{ID: "user-1", Role: "admin"})
		case "/api/projects/tunnels":
			if got := r.Header.Get("Authorization"); got != "" {
				http.Error(w, "authorization should be empty", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, " token ")
	whoami, err := client.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami() error = %v", err)
	}
	if whoami.ID != "user-1" || whoami.Role != "admin" {
		t.Fatalf("unexpected whoami: %+v", whoami)
	}
	_, err = NewClient(server.URL, "").ListProjects(context.Background(), ListProjectsParams{})
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected forbidden wrapped error, got %v", err)
	}
	_, err = NewClient(server.URL, "bad").Whoami(context.Background())
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("expected unauthorized wrapped error, got %v", err)
	}
}

func TestResolveProjectByEndpointEscapesPath(t *testing.T) {
	endpoint := "e7e8a732.aws-eu-west-3-1.c.rstream.io:8443"
	expectedPath := "/api/projects/tunnels/resolve/" + url.PathEscape(endpoint)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != expectedPath {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Project{ID: "p1", Endpoint: endpoint, Name: "Project", Routing: "regional"})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	project, err := client.ResolveProjectByEndpoint(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if project.ID != "p1" || project.Endpoint != endpoint {
		t.Fatalf("unexpected project: %+v", project)
	}
}

func TestResolveProjectByIDEscapesPath(t *testing.T) {
	projectID := "project/with space"
	expectedPath := "/api/projects/tunnels/" + url.PathEscape(projectID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != expectedPath {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("x-deployment-bypass"); got != "secret" {
			http.Error(w, "missing deployment bypass", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Project{ID: projectID, Endpoint: "endpoint", Name: "Project", Routing: "regional"})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token", WithHeaders(map[string]string{"x-deployment-bypass": "secret"}))
	project, err := client.ResolveProjectByID(context.Background(), projectID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if project.ID != projectID || project.Endpoint != "endpoint" {
		t.Fatalf("unexpected project: %+v", project)
	}
}

func TestProjectResponsesRequireRouting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/projects/tunnels":
			_ = json.NewEncoder(w).Encode(ListProjectsResponse{Projects: []Project{{ID: "p1"}}})
		case "/api/projects/tunnels/p1":
			_ = json.NewEncoder(w).Encode(Project{ID: "p1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	if _, err := client.ListProjects(t.Context(), ListProjectsParams{}); err == nil || !strings.Contains(err.Error(), "routing") {
		t.Fatalf("ListProjects() error = %v, want routing error", err)
	}
	if _, err := client.ResolveProjectByID(t.Context(), "p1"); err == nil || !strings.Contains(err.Error(), "routing") {
		t.Fatalf("ResolveProjectByID() error = %v, want routing error", err)
	}
}

func TestWorkspaceProjectCreationEndpoints(t *testing.T) {
	workspaceID := "workspace/with space"
	expectedPrefix := "/api/workspaces/" + url.PathEscape(workspaceID) + "/projects/tunnels"
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/workspaces":
			seen["workspaces"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ListWorkspacesResponse{Workspaces: []Workspace{{ID: "ws1", Name: "Workspace", Type: "personal"}}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == expectedPrefix:
			seen["workspace_projects"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ListProjectsResponse{Projects: []Project{{ID: "p1", Name: "Project", Routing: "regional"}}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == expectedPrefix+"/plan/config":
			seen["options"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectCreationOptionsResponse{Plans: []ProjectCreationOption{{Plan: "basic", Available: true, CreationFingerprint: "abc"}}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/projects/tunnels/p1/plan":
			seen["project_plan"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectPlan{"id": "basic", "features": []string{"HTTPS tunnels"}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == expectedPrefix:
			seen["create"] = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(Project{ID: "p2", Name: "Created", Routing: "regional"})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == expectedPrefix+"/payment-checkout":
			seen["checkout"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(CreateProjectCheckoutResponse{URL: "https://checkout.example", ProjectID: "p3"})
		case r.Method == http.MethodPut && r.URL.EscapedPath() == "/api/projects/tunnels/p2":
			var request UpdateProjectRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Name != "Renamed" {
				http.Error(w, "unexpected update request", http.StatusBadRequest)
				return
			}
			seen["update"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Project{ID: "p2", Name: request.Name, Routing: "regional"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/api/projects/tunnels/p2":
			seen["delete"] = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	if _, err := client.ListWorkspaces(context.Background()); err != nil {
		t.Fatalf("ListWorkspaces returned error: %v", err)
	}
	if _, err := client.ListWorkspaceProjects(context.Background(), workspaceID, ListProjectsParams{}); err != nil {
		t.Fatalf("ListWorkspaceProjects returned error: %v", err)
	}
	if _, err := client.ProjectCreationOptions(context.Background(), workspaceID); err != nil {
		t.Fatalf("ProjectCreationOptions returned error: %v", err)
	}
	if _, err := client.GetProjectPlan(context.Background(), "p1"); err != nil {
		t.Fatalf("GetProjectPlan returned error: %v", err)
	}
	request := CreateProjectRequest{Name: "Created", Routing: "regional", Provider: "aws", Region: "eu-west-3", Plan: "basic", CreationFingerprint: "abc", IdempotencyKey: "idem"}
	if _, err := client.CreateProject(context.Background(), workspaceID, request); err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}
	if _, err := client.CreateProjectCheckout(context.Background(), workspaceID, request); err != nil {
		t.Fatalf("CreateProjectCheckout returned error: %v", err)
	}
	updated, err := client.UpdateProject(context.Background(), "p2", UpdateProjectRequest{Name: "Renamed"})
	if err != nil || updated.Name != "Renamed" {
		t.Fatalf("UpdateProject returned %+v, %v", updated, err)
	}
	if err := client.DeleteProject(context.Background(), "p2"); err != nil {
		t.Fatalf("DeleteProject returned error: %v", err)
	}
	for _, key := range []string{"workspaces", "workspace_projects", "options", "project_plan", "create", "checkout", "update", "delete"} {
		if !seen[key] {
			t.Fatalf("endpoint %q was not called", key)
		}
	}
}

func TestCreateProjectTURNCredentialsEscapesProjectID(t *testing.T) {
	projectID := "workspace/project id"
	expectedPath := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/turn-server/credentials"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		if got := r.URL.EscapedPath(); got != expectedPath {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		var payload CreateTURNCredentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.TTLSeconds == nil || *payload.TTLSeconds != 60 {
			http.Error(w, "unexpected credential request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TURNCredentials{Username: "u", Credential: "c", URLs: []string{"turn:example.com"}, TTL: 60})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	ttl := 60
	res, err := client.CreateProjectTURNCredentialsWithOptions(context.Background(), projectID, CreateTURNCredentialsRequest{TTLSeconds: &ttl})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" || res.TTL != 60 {
		t.Fatalf("unexpected credentials: %+v", res)
	}
}

func TestCreateProjectTURNCredentialsByEndpointEscapesPath(t *testing.T) {
	endpoint := "abc12345.aws-eu-west-3-1.c.rstream.io"
	expectedPath := "/api/projects/tunnels/resolve/" + url.PathEscape(endpoint) + "/turn-server/credentials"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != expectedPath {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TURNCredentials{
			Username:   "u",
			Credential: "c",
			URLs:       []string{"turn:example.com:3478?transport=udp"},
			TTL:        86400,
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	res, err := client.CreateProjectTURNCredentialsByEndpoint(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" || res.TTL != 86400 {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestCreateTokenPostsPermissionsAndResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tokens" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusBadRequest)
			return
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if string(payload["permissions"]) != `["tunnels.resources.read-only"]` {
			http.Error(w, "unexpected permissions", http.StatusBadRequest)
			return
		}
		if !strings.Contains(string(payload["resources"]), `"tunnels"`) {
			http.Error(w, "unexpected resources", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CreateTokenResponse{Token: "minted"})
	}))
	defer server.Close()
	perms := []string{"tunnels.resources.read-only"}
	resources := json.RawMessage(`{"tunnels":{"projects":["p1"],"scopes":{"tunnels":{"connect":true}}}}`)
	response, err := NewClient(server.URL, "token").CreateToken(context.Background(), CreateTokenRequest{Permissions: &perms, Resources: &resources})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}
	if response.Token != "minted" {
		t.Fatalf("unexpected token response: %#v", response)
	}
}

func TestWebTTYServerManagementEndpoints(t *testing.T) {
	projectID := "project/1"
	serverID := "server/1"
	publicKey := "pub"
	fingerprint := "sha256:fingerprint"
	keyAlgorithm := "webtty-x25519-hpke-v1"
	serverPayload := WebTTYServer{
		ID:                 serverID,
		WorkspaceID:        "workspace-1",
		ProjectID:          projectID,
		Name:               "Shell",
		Status:             "pending_enrollment",
		RecordingPolicy:    "recorded",
		EncryptionPolicy:   "explicit_key",
		AccessPolicy:       "project_members",
		ServerPublicKey:    &publicKey,
		ServerFingerprint:  &fingerprint,
		ServerKeyAlgorithm: &keyAlgorithm,
		CreatedAt:          "2026-06-06T12:00:00.000Z",
		UpdatedAt:          "2026-06-06T12:00:00.000Z",
	}
	responsePayload := CreateWebTTYServerResponse{
		Server: serverPayload,
	}
	projectPrefix := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers"
	serverPath := projectPrefix + "/" + url.PathEscape(serverID)
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix:
			seen["list"] = true
			if r.URL.Query().Get("q") != "shell" || r.URL.Query().Get("status") != "active" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pageSize") != "10" {
				http.Error(w, "unexpected list query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ListWebTTYServersResponse{Servers: []WebTTYServer{serverPayload}, Page: 2, PageSize: 10, Total: 1, TotalPages: 1})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == projectPrefix:
			seen["create"] = true
			var payload CreateWebTTYServerRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if payload.Name != "Shell" || payload.RecordingPolicy != "recorded" || payload.EncryptionPolicy != "explicit_key" || payload.AccessPolicy != "project_members" || payload.Labels["role"] != "ops" {
				http.Error(w, "unexpected create payload", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(responsePayload)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == serverPath:
			seen["get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(serverPayload)
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == serverPath:
			seen["update"] = true
			var payload UpdateWebTTYServerRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if payload.Status == nil || *payload.Status != "suspended" || payload.AccessPolicy == nil || *payload.AccessPolicy != "restricted" {
				http.Error(w, "unexpected update payload", http.StatusBadRequest)
				return
			}
			updated := serverPayload
			updated.Status = "suspended"
			updated.AccessPolicy = "restricted"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(updated)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == serverPath:
			seen["delete"] = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	page := 2
	pageSize := 10
	client := NewClient(server.URL, "token")
	list, err := client.ListWebTTYServers(context.Background(), projectID, ListWebTTYServersParams{Query: "shell", Status: "active", Page: &page, PageSize: &pageSize})
	if err != nil {
		t.Fatalf("ListWebTTYServers() error = %v", err)
	}
	if len(list.Servers) != 1 || list.Servers[0].ID != serverID {
		t.Fatalf("unexpected list response: %#v", list)
	}
	created, err := client.CreateWebTTYServer(context.Background(), projectID, CreateWebTTYServerRequest{
		Name:             "Shell",
		RecordingPolicy:  "recorded",
		EncryptionPolicy: "explicit_key",
		AccessPolicy:     "project_members",
		Labels:           map[string]string{"role": "ops"},
	})
	if err != nil {
		t.Fatalf("CreateWebTTYServer() error = %v", err)
	}
	if created.Server.ID != serverID {
		t.Fatalf("unexpected create response: %#v", created)
	}
	got, err := client.GetWebTTYServer(context.Background(), projectID, serverID)
	if err != nil {
		t.Fatalf("GetWebTTYServer() error = %v", err)
	}
	if got.ID != serverID || got.ProjectID != projectID {
		t.Fatalf("unexpected get response: %#v", got)
	}
	status := "suspended"
	accessPolicy := "restricted"
	updated, err := client.UpdateWebTTYServer(context.Background(), projectID, serverID, UpdateWebTTYServerRequest{Status: &status, AccessPolicy: &accessPolicy})
	if err != nil {
		t.Fatalf("UpdateWebTTYServer() error = %v", err)
	}
	if updated.Status != "suspended" || updated.AccessPolicy != "restricted" {
		t.Fatalf("unexpected update response: %#v", updated)
	}
	if err := client.DeleteWebTTYServer(context.Background(), projectID, serverID); err != nil {
		t.Fatalf("DeleteWebTTYServer() error = %v", err)
	}
	for _, key := range []string{"list", "create", "get", "update", "delete"} {
		if !seen[key] {
			t.Fatalf("endpoint %q was not called", key)
		}
	}
}

func TestEnrollWebTTYServer(t *testing.T) {
	projectID := "project/1"
	serverID := "server/1"
	publicKey := "pub"
	fingerprint := "sha256:fingerprint"
	keyAlgorithm := "webtty-x25519-hpke-v1"
	responsePayload := WebTTYServer{
		ID:                 serverID,
		WorkspaceID:        "workspace-1",
		ProjectID:          projectID,
		Name:               "Shell",
		Status:             "active",
		RecordingPolicy:    "recorded",
		EncryptionPolicy:   "explicit_key",
		AccessPolicy:       "project_members",
		ServerPublicKey:    &publicKey,
		ServerFingerprint:  &fingerprint,
		ServerKeyAlgorithm: &keyAlgorithm,
		CreatedAt:          "2026-06-06T12:00:00.000Z",
		UpdatedAt:          "2026-06-06T12:00:00.000Z",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project%2F1/webtty/servers/server%2F1/enroll" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		var payload EnrollWebTTYServerRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if payload.ServerPublicKey != publicKey || payload.ServerFingerprint != fingerprint || payload.ServerKeyAlgorithm != keyAlgorithm {
			http.Error(w, "unexpected payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responsePayload)
	}))
	defer server.Close()
	response, err := NewClient(server.URL, "token").EnrollWebTTYServer(context.Background(), projectID, serverID, EnrollWebTTYServerRequest{
		ServerPublicKey:    publicKey,
		ServerFingerprint:  fingerprint,
		ServerKeyAlgorithm: keyAlgorithm,
	})
	if err != nil {
		t.Fatalf("EnrollWebTTYServer() error = %v", err)
	}
	if response.ID != serverID || response.ProjectID != projectID || response.EncryptionPolicy != "explicit_key" || response.ServerFingerprint == nil || *response.ServerFingerprint != fingerprint {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestApproveWebTTYServerWorkspaceTrust(t *testing.T) {
	projectID := "project/1"
	serverID := "server/1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project%2F1/webtty/servers/server%2F1/workspace-trust" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		var payload ApproveWebTTYServerWorkspaceTrustRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if payload.ActorDeviceKeyID != "device-1" || payload.KeysetID != "keyset-1" || payload.ActorSignature == "" || payload.KeysetSignature == "" {
			http.Error(w, "unexpected payload", http.StatusBadRequest)
			return
		}
		status := "workspace_trusted"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(WebTTYServer{
			ID:               serverID,
			WorkspaceID:      "workspace-1",
			ProjectID:        projectID,
			Name:             "Shell",
			Status:           "active",
			RecordingPolicy:  "recorded",
			EncryptionPolicy: "workspace_managed",
			AccessPolicy:     "project_members",
			ServerKeyStatus:  &status,
			CreatedAt:        "2026-06-06T12:00:00.000Z",
			UpdatedAt:        "2026-06-06T12:00:00.000Z",
		})
	}))
	defer server.Close()
	response, err := NewClient(server.URL, "token").ApproveWebTTYServerWorkspaceTrust(context.Background(), projectID, serverID, ApproveWebTTYServerWorkspaceTrustRequest{
		ActorDeviceKeyID: "device-1",
		KeysetID:         "keyset-1",
		SignedAt:         "2026-06-08T10:00:00.000Z",
		ActorSignature:   "actor-signature",
		KeysetSignature:  "keyset-signature",
	})
	if err != nil {
		t.Fatalf("ApproveWebTTYServerWorkspaceTrust() error = %v", err)
	}
	if response.ID != serverID || response.ServerKeyStatus == nil || *response.ServerKeyStatus != "workspace_trusted" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestResolveWebTTYServerClient(t *testing.T) {
	projectID := "project/1"
	serverID := "server/1"
	publicKey := "public-key"
	fingerprint := "sha256:fingerprint"
	keyAlgorithm := "webtty-x25519-hpke-v1"
	keyStatus := "workspace_trusted"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/projects/tunnels/project%2F1/webtty/servers/server%2F1/client-resolution" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		var payload ResolveWebTTYServerClientRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(payload.DeviceProofs) != 1 || payload.DeviceProofs[0].DeviceFingerprint != "sha256:device" {
			http.Error(w, "unexpected device proof", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ResolveWebTTYServerClientResponse{
			ServerID:           serverID,
			WorkspaceID:        "workspace-1",
			ProjectID:          projectID,
			EncryptionPolicy:   "workspace_managed",
			E2ERequired:        true,
			ServerPublicKey:    &publicKey,
			ServerFingerprint:  &fingerprint,
			ServerKeyAlgorithm: &keyAlgorithm,
			ServerKeyStatus:    &keyStatus,
		})
	}))
	defer server.Close()
	response, err := NewClient(server.URL, "token").ResolveWebTTYServerClient(context.Background(), projectID, serverID, ResolveWebTTYServerClientRequest{
		DeviceProofs: []WorkspaceDeviceAccessProof{{
			DeviceFingerprint: "sha256:device",
			Challenge:         "challenge",
			SignedAt:          "2026-06-08T10:00:00Z",
			Signature:         "signature",
		}},
	})
	if err != nil {
		t.Fatalf("ResolveWebTTYServerClient() error = %v", err)
	}
	if !response.E2ERequired || response.ServerPublicKey == nil || *response.ServerPublicKey != publicKey || response.EncryptionPolicy != "workspace_managed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestProjectOperationsAndWorkspaceMembersEndpoints(t *testing.T) {
	projectID := "workspace/project id"
	domainID := "domain/with space"
	addressID := "address/with space"
	workspaceID := "workspace/with space"
	projectPrefix := "/api/projects/tunnels/" + url.PathEscape(projectID)
	domainPath := projectPrefix + "/domains/" + url.PathEscape(domainID)
	addressPath := projectPrefix + "/addresses/" + url.PathEscape(addressID)
	workspaceMembersPath := "/api/workspaces/" + url.PathEscape(workspaceID) + "/members"
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/logs":
			seen["logs"] = true
			if r.URL.Query().Get("timeline") != "1h" || r.URL.Query().Get("eventType") != "connection.closed" || r.URL.Query().Get("pageSize") != "5" {
				http.Error(w, "unexpected logs query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectLogsResponse{Events: []ProjectLogEvent{{"eventType": "connection.closed"}}, Page: 1, PageSize: 5, Total: 1, TotalPages: 1})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/usage":
			seen["usage"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectUsage{"metrics": map[string]any{"bandwidthTunnels": map[string]any{"value": 1}}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/turn/usage":
			seen["turn_usage"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectTURNUsage{"totals30d": map[string]any{"relayBytesTotal": 42}})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/domains":
			seen["domains_list"] = true
			if r.URL.Query().Get("q") != "codex" || r.URL.Query().Get("pageSize") != "10" {
				http.Error(w, "unexpected domains query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ListProjectDomainsResponse{Domains: []ProjectDomain{{"id": domainID, "hostname": "codex.example.com"}}, Page: 1, PageSize: 10, Total: 1, TotalPages: 1})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == projectPrefix+"/domains":
			seen["domain_create"] = true
			var payload CreateProjectDomainRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Hostname != "*.codex.example.com" || payload.Kind != ProjectDomainKindWildcard || payload.CertificateValidation != ProjectDomainCertificateValidationDNS01 {
				http.Error(w, "unexpected domain create", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectDomain{"id": domainID, "hostname": payload.Hostname})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == domainPath:
			seen["domain_get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectDomain{"id": domainID, "hostname": "codex.example.com"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == domainPath:
			seen["domain_delete"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectDomain{"id": domainID})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == domainPath+"/verify":
			seen["domain_verify"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectDomain{"id": domainID, "status": "active"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == domainPath+"/domain-connect":
			seen["domain_connect"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(DomainConnectResponse{"supported": true, "applyUrl": "https://dns.example/apply"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/addresses":
			seen["addresses_list"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ListProjectTCPAddressesResponse{Addresses: []ProjectTCPAddress{{ID: addressID, ProjectID: projectID, Hostname: "tcp.example.com", Port: 10042, Address: "tcp.example.com:10042"}}, ReservationEnabled: true, PublishedTCPEnabled: true, Limit: 50, QuarantineSeconds: 86400})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == projectPrefix+"/addresses":
			seen["address_reserve"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectTCPAddress{ID: addressID, ProjectID: projectID, Hostname: "tcp.example.com", Port: 10042, Address: "tcp.example.com:10042"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == addressPath:
			seen["address_release"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ReleaseProjectTCPAddressResponse{ID: addressID, ReusableAt: "2026-07-18T12:00:00Z"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/settings":
			seen["settings_get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectSettings{"publicAccessPolicy": "allowed"})
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == projectPrefix+"/settings":
			seen["settings_patch"] = true
			var payload ProjectSettings
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["publicAccessPolicy"] != "forbidden" {
				http.Error(w, "unexpected settings patch", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectSettings{"publicAccessPolicy": "forbidden"})
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == projectPrefix+"/settings":
			seen["settings_reset"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectSettings{"publicAccessPolicy": "allowed"})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == workspaceMembersPath:
			seen["members"] = true
			if r.URL.Query().Get("q") != "admin" || r.URL.Query().Get("pageSize") != "10" {
				http.Error(w, "unexpected members query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(WorkspaceMembersResponse{Members: []WorkspaceMember{{ID: "m1", Email: "admin@example.test", Role: "admin", Status: "active"}}, Page: 1, PageSize: 10, Total: 1, TotalPages: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	pageSize := 5
	memberPageSize := 10
	client := NewClient(server.URL, "token")
	if _, err := client.ListProjectLogs(context.Background(), projectID, ProjectLogsParams{Timeline: "1h", EventType: "connection.closed", PageSize: &pageSize}); err != nil {
		t.Fatalf("ListProjectLogs returned error: %v", err)
	}
	if _, err := client.GetProjectUsage(context.Background(), projectID); err != nil {
		t.Fatalf("GetProjectUsage returned error: %v", err)
	}
	if _, err := client.GetProjectTURNUsage(context.Background(), projectID); err != nil {
		t.Fatalf("GetProjectTURNUsage returned error: %v", err)
	}
	if _, err := client.ListProjectDomains(context.Background(), projectID, ListProjectDomainsParams{Query: "codex", PageSize: &memberPageSize}); err != nil {
		t.Fatalf("ListProjectDomains returned error: %v", err)
	}
	if _, err := client.CreateProjectDomain(t.Context(), projectID, CreateProjectDomainRequest{Hostname: "*.codex.example.com", Kind: ProjectDomainKindWildcard, CertificateValidation: ProjectDomainCertificateValidationDNS01}); err != nil {
		t.Fatalf("CreateProjectDomain returned error: %v", err)
	}
	if _, err := client.GetProjectDomain(context.Background(), projectID, domainID); err != nil {
		t.Fatalf("GetProjectDomain returned error: %v", err)
	}
	if _, err := client.DeleteProjectDomain(context.Background(), projectID, domainID); err != nil {
		t.Fatalf("DeleteProjectDomain returned error: %v", err)
	}
	if _, err := client.VerifyProjectDomain(context.Background(), projectID, domainID); err != nil {
		t.Fatalf("VerifyProjectDomain returned error: %v", err)
	}
	if _, err := client.GetProjectDomainConnect(context.Background(), projectID, domainID); err != nil {
		t.Fatalf("GetProjectDomainConnect returned error: %v", err)
	}
	addresses, err := client.ListProjectTCPAddresses(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ListProjectTCPAddresses returned error: %v", err)
	}
	if !addresses.PublishedTCPEnabled {
		t.Fatal("ListProjectTCPAddresses did not decode the published TCP policy")
	}
	if _, err := client.ReserveProjectTCPAddress(context.Background(), projectID); err != nil {
		t.Fatalf("ReserveProjectTCPAddress returned error: %v", err)
	}
	if _, err := client.ReleaseProjectTCPAddress(context.Background(), projectID, addressID); err != nil {
		t.Fatalf("ReleaseProjectTCPAddress returned error: %v", err)
	}
	if _, err := client.GetProjectSettings(context.Background(), projectID); err != nil {
		t.Fatalf("GetProjectSettings returned error: %v", err)
	}
	if _, err := client.PatchProjectSettings(context.Background(), projectID, ProjectSettings{"publicAccessPolicy": "forbidden"}); err != nil {
		t.Fatalf("PatchProjectSettings returned error: %v", err)
	}
	if _, err := client.ResetProjectSettings(context.Background(), projectID); err != nil {
		t.Fatalf("ResetProjectSettings returned error: %v", err)
	}
	if _, err := client.ListWorkspaceMembers(context.Background(), workspaceID, WorkspaceMembersParams{Query: "admin", PageSize: &memberPageSize}); err != nil {
		t.Fatalf("ListWorkspaceMembers returned error: %v", err)
	}
	for _, key := range []string{"logs", "usage", "turn_usage", "domains_list", "domain_create", "domain_get", "domain_delete", "domain_verify", "domain_connect", "addresses_list", "address_reserve", "address_release", "settings_get", "settings_patch", "settings_reset", "members"} {
		if !seen[key] {
			t.Fatalf("endpoint %q was not called", key)
		}
	}
}

func TestProjectEventsAndWebhookEndpoints(t *testing.T) {
	projectID := "workspace/project id"
	webhookID := "webhook/with space"
	deliveryID := "delivery/with space"
	projectPrefix := "/api/projects/tunnels/" + url.PathEscape(projectID)
	webhookPath := projectPrefix + "/webhooks/" + url.PathEscape(webhookID)
	deliveryPath := webhookPath + "/deliveries/" + url.PathEscape(deliveryID)
	seen := map[string]bool{}
	description := "Lifecycle sink"
	createdBy := "user-1"
	rotatedAt := "2026-06-02T12:00:00.000Z"
	httpStatus := 200
	responseTime := 42
	responseHeaders := ProjectWebhookResponseHeaders{"content-type": []string{"application/json"}}
	responseBody := `{"accepted":true}`
	completedAt := rotatedAt
	webhook := ProjectWebhook{ID: webhookID, WorkspaceID: "workspace-id", ProjectID: projectID, Name: "Lifecycle sink", Description: &description, DestinationType: ProjectWebhookDestinationEndpoint, Status: ProjectWebhookEndpointEnabled, Events: []string{"tunnel.created", "tunnel.deleted"}, Config: json.RawMessage(`{"url":"https://example.com/rstream/webhook"}`), SecretLastRotatedAt: &rotatedAt, CreatedByUserID: &createdBy, CreatedAt: rotatedAt, UpdatedAt: rotatedAt}
	delivery := ProjectWebhookDelivery{ID: deliveryID, WorkspaceID: "workspace-id", ProjectID: projectID, WebhookEndpointID: webhookID, EventID: "evt-1", EventType: "tunnel.created", Status: ProjectWebhookDeliverySucceeded, AttemptCount: 1, LastAttemptAt: &rotatedAt, SucceededAt: &rotatedAt, LastHTTPStatus: &httpStatus, LastResponseTimeMs: &responseTime, RequestBody: json.RawMessage(`{"id":"evt-1","type":"tunnel.created"}`), CreatedAt: rotatedAt, UpdatedAt: rotatedAt, Attempts: []ProjectWebhookDeliveryAttempt{{ID: "attempt-1", DeliveryID: deliveryID, AttemptNumber: 1, Status: ProjectWebhookDeliveryAttemptSucceeded, HTTPStatus: &httpStatus, ResponseTimeMs: &responseTime, ResponseHeaders: &responseHeaders, ResponseBody: &responseBody, StartedAt: rotatedAt, CompletedAt: &completedAt, CreatedAt: rotatedAt}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/events":
			seen["events"] = true
			if r.URL.Query().Get("timeline") != "24h" || r.URL.Query().Get("eventType") != "tunnel.created" || r.URL.Query().Get("afterEventId") != "evt-0" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pageSize") != "10" || r.URL.Query().Get("order") != "desc" {
				http.Error(w, "unexpected events query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectEventsResponse{Events: []ProjectEvent{{ID: "row-1", EventID: "evt-1", EventType: "tunnel.created", EventCategory: "lifecycle", CreatedAt: rotatedAt, UpdatedAt: rotatedAt, ProjectID: projectID, WorkspaceID: "workspace-id", ClusterID: "cluster-id", Payload: json.RawMessage(`{"id":"evt-1","type":"tunnel.created"}`)}}, Page: 2, PageSize: 10, Total: 1, TotalPages: 1})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == projectPrefix+"/webhooks":
			seen["webhooks_list"] = true
			if r.URL.Query().Get("q") != "lifecycle" || r.URL.Query().Get("status") != string(ProjectWebhookEndpointEnabled) || r.URL.Query().Get("destinationType") != string(ProjectWebhookDestinationEndpoint) || r.URL.Query().Get("pageSize") != "10" {
				http.Error(w, "unexpected webhooks query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectWebhooksResponse{Webhooks: []ProjectWebhook{webhook}, Page: 1, PageSize: 10, Total: 1, TotalPages: 1})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == projectPrefix+"/webhooks":
			seen["webhook_create"] = true
			var payload CreateProjectWebhookRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Name != "Lifecycle sink" || payload.Config.URL != "https://example.com/rstream/webhook" || payload.DestinationType != ProjectWebhookDestinationEndpoint {
				http.Error(w, "unexpected webhook create", http.StatusBadRequest)
				return
			}
			out := webhook
			out.SigningSecret = "whsec_clear"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == webhookPath:
			seen["webhook_get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(webhook)
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == webhookPath:
			seen["webhook_update"] = true
			var payload UpdateProjectWebhookRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Config == nil || payload.Config.URL != "https://example.com/rstream/updated" {
				http.Error(w, "unexpected webhook update", http.StatusBadRequest)
				return
			}
			out := webhook
			configPayload, err := json.Marshal(payload.Config)
			if err != nil {
				http.Error(w, "failed to encode config", http.StatusInternalServerError)
				return
			}
			out.Config = configPayload
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == webhookPath+"/secret/rotate":
			seen["webhook_rotate"] = true
			out := webhook
			out.SigningSecret = "whsec_rotated"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == webhookPath+"/deliveries":
			seen["deliveries_list"] = true
			if r.URL.Query().Get("status") != string(ProjectWebhookDeliverySucceeded) || r.URL.Query().Get("eventType") != "tunnel.created" || r.URL.Query().Get("pageSize") != "10" {
				http.Error(w, "unexpected deliveries query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ProjectWebhookDeliveriesResponse{Deliveries: []ProjectWebhookDelivery{delivery}, Page: 1, PageSize: 10, Total: 1, TotalPages: 1})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == deliveryPath:
			seen["delivery_get"] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(delivery)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == webhookPath:
			seen["webhook_delete"] = true
			out := webhook
			out.Status = ProjectWebhookEndpointDisabled
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	page := 2
	pageSize := 10
	client := NewClient(server.URL, "token")
	events, err := client.ListProjectEvents(context.Background(), projectID, ProjectEventsParams{Timeline: "24h", EventType: "tunnel.created", AfterEventID: "evt-0", Page: &page, PageSize: &pageSize, Order: "desc"})
	if err != nil || len(events.Events) != 1 || events.Events[0].EventType != "tunnel.created" || string(events.Events[0].Payload) == "" {
		t.Fatalf("ListProjectEvents returned %+v, %v", events, err)
	}
	webhooks, err := client.ListProjectWebhooks(context.Background(), projectID, ProjectWebhooksParams{Query: "lifecycle", Status: ProjectWebhookEndpointEnabled, DestinationType: ProjectWebhookDestinationEndpoint, PageSize: &pageSize})
	if err != nil || len(webhooks.Webhooks) != 1 {
		t.Fatalf("ListProjectWebhooks returned %+v, %v", webhooks, err)
	}
	config, err := webhooks.Webhooks[0].DecodeEndpointConfig()
	if err != nil || config.URL != "https://example.com/rstream/webhook" {
		t.Fatalf("DecodeEndpointConfig returned %+v, %v", config, err)
	}
	created, err := client.CreateProjectWebhook(context.Background(), projectID, CreateProjectWebhookRequest{Name: "Lifecycle sink", DestinationType: ProjectWebhookDestinationEndpoint, Events: []string{"tunnel.created"}, Config: ProjectWebhookEndpointConfig{URL: "https://example.com/rstream/webhook"}})
	if err != nil || created.SigningSecret != "whsec_clear" {
		t.Fatalf("CreateProjectWebhook returned %+v, %v", created, err)
	}
	if _, err := client.GetProjectWebhook(context.Background(), projectID, webhookID); err != nil {
		t.Fatalf("GetProjectWebhook returned error: %v", err)
	}
	updateConfig := ProjectWebhookEndpointConfig{URL: "https://example.com/rstream/updated"}
	updated, err := client.UpdateProjectWebhook(context.Background(), projectID, webhookID, UpdateProjectWebhookRequest{Config: &updateConfig})
	updatedConfig, decodeErr := updated.DecodeEndpointConfig()
	if err != nil || decodeErr != nil || updatedConfig.URL != updateConfig.URL {
		t.Fatalf("UpdateProjectWebhook returned %+v, %v", updated, err)
	}
	rotated, err := client.RotateProjectWebhookSecret(context.Background(), projectID, webhookID)
	if err != nil || rotated.SigningSecret != "whsec_rotated" {
		t.Fatalf("RotateProjectWebhookSecret returned %+v, %v", rotated, err)
	}
	deliveries, err := client.ListProjectWebhookDeliveries(context.Background(), projectID, webhookID, ProjectWebhookDeliveriesParams{Status: ProjectWebhookDeliverySucceeded, EventType: "tunnel.created", PageSize: &pageSize})
	if err != nil || len(deliveries.Deliveries) != 1 || deliveries.Deliveries[0].Attempts[0].HTTPStatus == nil || *deliveries.Deliveries[0].Attempts[0].HTTPStatus != 200 {
		t.Fatalf("ListProjectWebhookDeliveries returned %+v, %v", deliveries, err)
	}
	gotDelivery, err := client.GetProjectWebhookDelivery(context.Background(), projectID, webhookID, deliveryID)
	if err != nil || string(gotDelivery.RequestBody) == "" {
		t.Fatalf("GetProjectWebhookDelivery returned %+v, %v", gotDelivery, err)
	}
	deleted, err := client.DeleteProjectWebhook(context.Background(), projectID, webhookID)
	if err != nil || deleted.Status != ProjectWebhookEndpointDisabled {
		t.Fatalf("DeleteProjectWebhook returned %+v, %v", deleted, err)
	}
	for _, key := range []string{"events", "webhooks_list", "webhook_create", "webhook_get", "webhook_update", "webhook_rotate", "deliveries_list", "delivery_get", "webhook_delete"} {
		if !seen[key] {
			t.Fatalf("endpoint %q was not called", key)
		}
	}
}

func TestProjectWebhookRequestsValidateDestinationBeforeIO(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	_, err := client.CreateProjectWebhook(context.Background(), "project-id", CreateProjectWebhookRequest{
		DestinationType: ProjectWebhookDestinationAmazonEventBridge,
		Events:          []string{"tunnel.created"},
		Config:          ProjectWebhookEndpointConfig{URL: "https://example.com/rstream/webhook"},
		Name:            "Lifecycle sink",
	})
	if err == nil {
		t.Fatal("expected unsupported destination error")
	}
	_, err = client.CreateProjectWebhook(context.Background(), "project-id", CreateProjectWebhookRequest{
		Events: []string{"tunnel.created"},
		Config: ProjectWebhookEndpointConfig{URL: "http://example.com/rstream/webhook"},
		Name:   "Lifecycle sink",
	})
	if err == nil {
		t.Fatal("expected insecure endpoint error")
	}
	config := ProjectWebhookEndpointConfig{URL: "https://token@example.com/rstream/webhook"}
	_, err = client.UpdateProjectWebhook(context.Background(), "project-id", "webhook-id", UpdateProjectWebhookRequest{Config: &config})
	if err == nil {
		t.Fatal("expected endpoint credentials error")
	}
}

func TestProjectWebhookDecodeEndpointConfigRejectsOtherDestinations(t *testing.T) {
	webhook := ProjectWebhook{
		DestinationType: ProjectWebhookDestinationAmazonEventBridge,
		Config:          json.RawMessage(`{"eventBusArn":"arn:aws:events:eu-west-3:123:event-bus/default"}`),
	}
	if _, err := webhook.DecodeEndpointConfig(); err == nil {
		t.Fatal("expected destination mismatch error")
	}
}

func TestListProjectsQueryParamsAndParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("q") != "acme" {
			http.Error(w, "missing q param", http.StatusBadRequest)
			return
		}
		if query.Get("page") != "2" {
			http.Error(w, "missing page param", http.StatusBadRequest)
			return
		}
		if _, ok := query["pageSize"]; ok {
			http.Error(w, "unexpected pageSize param", http.StatusBadRequest)
			return
		}
		if _, ok := query["sort"]; ok {
			http.Error(w, "unexpected sort param", http.StatusBadRequest)
			return
		}
		if _, ok := query["order"]; ok {
			http.Error(w, "unexpected order param", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListProjectsResponse{
			Projects:   []Project{{ID: "p1", Name: "Acme", Endpoint: "acme:8443", Status: "active", Plan: "pro", Deployment: "shared", Provider: "aws", Routing: "regional"}},
			Page:       2,
			PageSize:   10,
			Total:      1,
			TotalPages: 1,
		})
	}))
	defer server.Close()
	page := 2
	client := NewClient(server.URL, "token")
	resp, err := client.ListProjects(context.Background(), ListProjectsParams{
		Query: "acme",
		Page:  &page,
	})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if resp.Page != 2 || resp.PageSize != 10 || resp.Total != 1 || len(resp.Projects) != 1 {
		t.Fatalf("unexpected list response: %+v", resp)
	}
	if resp.Projects[0].ID != "p1" || resp.Projects[0].Endpoint != "acme:8443" {
		t.Fatalf("unexpected project: %+v", resp.Projects[0])
	}
}

func TestDoJSONBodyPropagatesAPIErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Credential \"rstream Login\" already exists and cannot be replaced automatically.",
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	_, err := client.CreateRstreamLogin(context.Background(), RstreamLoginRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "Credential \"rstream Login\" already exists and cannot be replaced automatically." {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestDoJSONBodyFallsBackToGenericMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "plain failure", http.StatusBadGateway)
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	_, err := client.CreateRstreamLogin(context.Background(), RstreamLoginRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "control-plane request failed: 502 Bad Gateway" {
		t.Fatalf("unexpected fallback error message: %q", got)
	}
}

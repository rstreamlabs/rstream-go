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
	if client.httpClient != httpClient || client.logger != logger {
		t.Fatalf("client options not applied")
	}
	if err := client.RequireToken(); err != nil {
		t.Fatalf("RequireToken() error = %v", err)
	}
	if err := NewClient("https://api.example.com", " ").RequireToken(); err == nil {
		t.Fatalf("expected missing token error")
	}
}

func TestWhoamiAuthorizationAndUnauthorizedWrapping(t *testing.T) {
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
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "forbidden") {
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
		_ = json.NewEncoder(w).Encode(Project{ID: "p1", Endpoint: endpoint, Name: "Project"})
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TURNCredentials{Username: "u", Credential: "c", URLs: []string{"turn:example.com"}, TTL: 60})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	res, err := client.CreateProjectTURNCredentials(context.Background(), projectID)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" || res.TTL != 60 {
		t.Fatalf("unexpected credentials: %+v", res)
	}
}

func TestCreateProjectTURNCredentialsByEndpointEscapesPath(t *testing.T) {
	endpoint := "bbc44f81.aws-eu-west-3-1.c.rstream.io"
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
			Projects:   []Project{{ID: "p1", Name: "Acme", Endpoint: "acme:8443", Status: "active", Plan: "pro", Deployment: "shared", Provider: "aws"}},
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

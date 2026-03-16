// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

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

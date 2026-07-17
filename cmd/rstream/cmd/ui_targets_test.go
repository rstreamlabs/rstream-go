// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestUIRuntimeResolverKeepsLocalContextsWithoutControlPlane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "device", Engine: "device.example:443", Auth: testUIInlineAuth("device-token")}
	cfg := config.Config{Contexts: []config.Context{contextValue}, Defaults: config.Defaults{Context: &config.DefaultContext{Name: "device"}}}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	runtime := &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: config.Resolved{ContextName: "device", Context: &contextValue, Engine: contextValue.Engine, Token: "device-token"}}
	resolver := newUIRuntimeResolver(path, uiRuntimeOptions{})
	discovery := resolver.discoverTargets(t.Context(), runtime)
	if len(discovery.Targets) != 1 || discovery.Targets[0].Context.Name != "device" {
		t.Fatalf("local targets = %#v, want device context", discovery.Targets)
	}
	if discovery.ProjectError == nil || !strings.Contains(discovery.ProjectError.Error(), "not configured") {
		t.Fatalf("ProjectError = %v, want non-blocking Control Plane error", discovery.ProjectError)
	}
}

func TestUIRuntimeResolverKeepsLastLocalContextsWhenConfigReloadFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("contexts: ["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	contextValue := config.Context{Name: "last-known", Engine: "last.example:443"}
	runtime := &resolvedRuntime{ConfigPath: path, Config: config.Config{Contexts: []config.Context{contextValue}}, Resolved: config.Resolved{Context: &contextValue}}
	discovery := newUIRuntimeResolver(path, uiRuntimeOptions{}).discoverTargets(t.Context(), runtime)
	if len(discovery.Targets) != 1 || discovery.Targets[0].Context.Name != "last-known" {
		t.Fatalf("fallback targets = %#v, want last-known", discovery.Targets)
	}
	if discovery.ProjectError == nil || !strings.Contains(discovery.ProjectError.Error(), "reload config") {
		t.Fatalf("ProjectError = %v, want reload error", discovery.ProjectError)
	}
}

func TestUIRuntimeResolverFiltersOnlyLinkedContextsByAPIScope(t *testing.T) {
	cfg := config.Config{Contexts: []config.Context{
		{Name: "first", APIURL: "https://first.example", Engine: "first.example:443"},
		{Name: "second", APIURL: "https://second.example", Engine: "second.example:443"},
		{Name: "device", Engine: "device.example:443"},
	}}
	resolver := newUIRuntimeResolver("unused", uiRuntimeOptions{apiURLScope: "https://first.example"})
	targets := resolver.initialTargets(cfg, nil)
	if len(targets) != 2 || targets[0].Context.Name != "device" || targets[1].Context.Name != "first" {
		t.Fatalf("scoped contexts = %#v, want device and first", targets)
	}
}

func TestUIRuntimeResolverDiscoversPaginatedProjectsAndWorkspaceNames(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer account-token" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspaces":
			_ = json.NewEncoder(w).Encode(controlplane.ListWorkspacesResponse{Workspaces: []controlplane.Workspace{{ID: "workspace-1", Name: "Operations"}}})
		case "/api/projects/tunnels":
			requests++
			page := r.URL.Query().Get("page")
			project := controlplane.Project{ID: "project-" + page, WorkspaceID: "workspace-1", Name: "Project " + page, Endpoint: "project-" + page, Domain: "engine.example", EnginePort: 443, Status: "active"}
			_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{Projects: []controlplane.Project{project}, Page: requests, PageSize: 100, Total: 2, TotalPages: 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "current", APIURL: server.URL, Engine: "current.example:443", ProjectEndpoint: "current"}
	cfg := config.Config{
		Environments: []config.Environment{{APIURL: server.URL, Auth: testUIInlineAuth("account-token")}},
		Contexts:     []config.Context{contextValue},
		Defaults:     config.Defaults{Context: &config.DefaultContext{Name: "current"}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	runtime := &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: config.Resolved{APIURL: server.URL, ContextName: "current", Context: &contextValue, Token: "account-token"}}
	discovery := newUIRuntimeResolver(path, uiRuntimeOptions{}).discoverTargets(t.Context(), runtime)
	if discovery.ProjectError != nil || discovery.WorkspaceWarning != nil {
		t.Fatalf("discoverTargets() errors = %v / %v", discovery.ProjectError, discovery.WorkspaceWarning)
	}
	if requests != 2 || len(discovery.Targets) != 3 {
		t.Fatalf("requests=%d targets=%#v, want two pages and three targets", requests, discovery.Targets)
	}
	for _, target := range discovery.Targets {
		if target.Kind == uiTargetProject && target.WorkspaceName != "Operations" {
			t.Fatalf("workspace name = %q, want Operations", target.WorkspaceName)
		}
	}
}

func TestUIRuntimeResolverToleratesWorkspaceLookupFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspaces" {
			http.Error(w, "workspace denied", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/api/projects/tunnels" {
			_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{Projects: []controlplane.Project{{ID: "project-1", Name: "Project", Endpoint: "project", Domain: "engine.example"}}, TotalPages: 1})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "current", APIURL: server.URL, Engine: "current.example:443"}
	cfg := config.Config{Environments: []config.Environment{{APIURL: server.URL, Auth: testUIInlineAuth("account-token")}}, Contexts: []config.Context{contextValue}}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	runtime := &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: config.Resolved{APIURL: server.URL, Context: &contextValue, Token: "account-token"}}
	discovery := newUIRuntimeResolver(path, uiRuntimeOptions{}).discoverTargets(t.Context(), runtime)
	if discovery.ProjectError != nil || discovery.WorkspaceWarning == nil || len(discovery.Targets) != 2 {
		t.Fatalf("discovery = %#v, want projects with workspace warning", discovery)
	}
}

func TestUIRuntimeResolverKeepsContextsWhenProjectTokenIsForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspaces" {
			_ = json.NewEncoder(w).Encode(controlplane.ListWorkspacesResponse{})
			return
		}
		http.Error(w, "project listing denied", http.StatusForbidden)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "limited", APIURL: server.URL, Engine: "limited.example:443", Auth: testUIInlineAuth("limited-token")}
	cfg := config.Config{Contexts: []config.Context{contextValue}}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	runtime := &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: config.Resolved{APIURL: server.URL, Context: &contextValue, Token: "limited-token"}}
	discovery := newUIRuntimeResolver(path, uiRuntimeOptions{}).discoverTargets(t.Context(), runtime)
	if len(discovery.Targets) != 1 || discovery.Targets[0].Context.Name != "limited" {
		t.Fatalf("local targets = %#v, want limited context", discovery.Targets)
	}
	if discovery.ProjectError == nil || !strings.Contains(discovery.ProjectError.Error(), "not authorized") {
		t.Fatalf("ProjectError = %v, want non-blocking forbidden error", discovery.ProjectError)
	}
}

func TestUIRuntimeResolverTemporaryAndPersistentContextSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	first := config.Context{Name: "first", Engine: "first.example:443", Auth: testUIInlineAuth("first-token")}
	second := config.Context{Name: "second", Engine: "second.example:443", Auth: testUIInlineAuth("second-token")}
	cfg := config.Config{Contexts: []config.Context{first, second}, Defaults: config.Defaults{Context: &config.DefaultContext{Name: "first"}}}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	resolver := newUIRuntimeResolver(path, uiRuntimeOptions{contextOverride: "first"})
	target := uiTarget{Kind: uiTargetContext, Context: second}
	runtime, connection, warning, persisted, err := resolver.prepareTarget(target, false)
	if err != nil {
		t.Fatalf("temporary prepareTarget() error = %v", err)
	}
	if persisted || warning != "" || runtime.Resolved.Engine != second.Engine || !connection.SessionOnly {
		t.Fatalf("temporary result = %#v %#v warning=%q persisted=%v", runtime.Resolved, connection, warning, persisted)
	}
	loaded, _ := config.Load(path)
	if loaded.Defaults.Context == nil || loaded.Defaults.Context.Name != "first" {
		t.Fatalf("temporary selection changed default: %#v", loaded.Defaults.Context)
	}
	_, connection, warning, persisted, err = resolver.prepareTarget(target, true)
	if err != nil {
		t.Fatalf("persistent prepareTarget() error = %v", err)
	}
	if !persisted || connection.SessionOnly || !strings.Contains(warning, "still selects first") {
		t.Fatalf("persistent result = %#v warning=%q persisted=%v", connection, warning, persisted)
	}
	loaded, _ = config.Load(path)
	if loaded.Defaults.Context == nil || loaded.Defaults.Context.Name != "second" {
		t.Fatalf("persistent selection default = %#v, want second", loaded.Defaults.Context)
	}
}

func TestUIRuntimeResolverTemporaryAndPersistentProjectSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	apiURL := "https://api.example"
	first := config.Context{Name: "first", APIURL: apiURL, Engine: "first.example:443"}
	cfg := config.Config{
		Environments: []config.Environment{{APIURL: apiURL, Auth: testUIInlineAuth("account-token")}},
		Contexts:     []config.Context{first},
		Defaults:     config.Defaults{Context: &config.DefaultContext{Name: "first"}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	project := controlplane.Project{ID: "project-1", WorkspaceID: "workspace-1", Name: "New Project", Endpoint: "new-project", Domain: "engines.example", EnginePort: 443}
	target := uiTarget{Kind: uiTargetProject, APIURL: apiURL, Project: project}
	resolver := newUIRuntimeResolver(path, uiRuntimeOptions{})
	runtime, connection, _, persisted, err := resolver.prepareTarget(target, false)
	if err != nil {
		t.Fatalf("temporary project prepareTarget() error = %v", err)
	}
	if persisted || runtime.Resolved.Engine != "new-project.engines.example:443" || connection.ProjectName != "New Project" || !connection.SessionOnly {
		t.Fatalf("temporary project result = %#v %#v persisted=%v", runtime.Resolved, connection, persisted)
	}
	loaded, _ := config.Load(path)
	if len(loaded.Contexts) != 1 || loaded.Defaults.Context.Name != "first" {
		t.Fatalf("temporary project changed config: %#v", loaded)
	}
	_, connection, _, persisted, err = resolver.prepareTarget(target, true)
	if err != nil {
		t.Fatalf("persistent project prepareTarget() error = %v", err)
	}
	loaded, _ = config.Load(path)
	if !persisted || connection.SessionOnly || len(loaded.Contexts) != 2 || loaded.Defaults.Context == nil || loaded.Defaults.Context.Name != "new-project" {
		t.Fatalf("persistent project result = %#v config=%#v persisted=%v", connection, loaded, persisted)
	}
}

func TestUIRuntimeResolverRejectsConflictingEngineOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contextValue := config.Context{Name: "target", Engine: "target.example:443", Auth: testUIInlineAuth("token")}
	if err := config.WriteAtomic(path, config.Config{Contexts: []config.Context{contextValue}}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	resolver := newUIRuntimeResolver(path, uiRuntimeOptions{environment: config.EnvSettings{Engine: "override.example:443"}})
	_, _, _, _, err := resolver.prepareTarget(uiTarget{Kind: uiTargetContext, Context: contextValue}, false)
	if err == nil || !strings.Contains(err.Error(), "RSTREAM_ENGINE") {
		t.Fatalf("prepareTarget() error = %v, want engine override error", err)
	}
}

func TestUIRuntimeSwitchWaitsForInventoryAndReportsEngineErrors(t *testing.T) {
	readyServer := newUIInventoryServer(t, http.StatusOK)
	defer readyServer.Close()
	path, target := writeUIRuntimeTarget(t, readyServer.URL)
	resolver := newUIRuntimeResolver(path, uiRuntimeOptions{})
	app := &uiApp{resolver: resolver}
	ctx, cancel := context.WithCancel(t.Context())
	result, err := app.prepareRuntimeSwitch(ctx, cancel, target, false, "sse", 2*time.Second)
	if err != nil {
		t.Fatalf("prepareRuntimeSwitch() error = %v", err)
	}
	if result.store == nil || !result.store.snapshot().Connected {
		t.Fatalf("prepared store = %#v, want connected snapshot", result.store)
	}
	result.cancel()
	failingServer := newUIInventoryServer(t, http.StatusForbidden)
	defer failingServer.Close()
	path, target = writeUIRuntimeTarget(t, failingServer.URL)
	app.resolver = newUIRuntimeResolver(path, uiRuntimeOptions{})
	ctx, cancel = context.WithCancel(t.Context())
	result, err = app.prepareRuntimeSwitch(ctx, cancel, target, false, "sse", 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "connect Engine inventory") {
		t.Fatalf("prepareRuntimeSwitch() error = %v, want Engine inventory error", err)
	}
	if result == nil || result.persisted {
		t.Fatalf("failed result = %#v, want non-persisted result", result)
	}
}

func TestUIRuntimeSwitchStopsWaitingAfterReadinessTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	path, target := writeUIRuntimeTarget(t, server.URL)
	app := &uiApp{resolver: newUIRuntimeResolver(path, uiRuntimeOptions{})}
	ctx, cancel := context.WithCancel(t.Context())
	started := time.Now()
	_, err := app.prepareRuntimeSwitch(ctx, cancel, target, false, "sse", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("prepareRuntimeSwitch() error = %v, want readiness timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("readiness timeout took %s, want less than one second", elapsed)
	}
}

func TestUIActivateRuntimeAtomicallyReplacesBundle(t *testing.T) {
	oldCtx, oldCancel := context.WithCancel(t.Context())
	newCtx, newCancel := context.WithCancel(t.Context())
	oldRuntime := &resolvedRuntime{Resolved: config.Resolved{ContextName: "old"}}
	newRuntime := &resolvedRuntime{Resolved: config.Resolved{ContextName: "new"}}
	oldClient := &rstream.Client{}
	newClient := &rstream.Client{}
	newStore := newUIStore("sse")
	newStore.connected = true
	app := &uiApp{
		ctx:           t.Context(),
		app:           tview.NewApplication(),
		runtime:       oldRuntime,
		client:        oldClient,
		store:         newUIStore("sse"),
		runtimeCancel: oldCancel,
		runtimeGen:    4,
		state:         uiState{ClientID: "old-client", TunnelID: "old-tunnel"},
	}
	newCancel()
	app.activateRuntime(&uiRuntimeSwitchResult{ctx: newCtx, cancel: newCancel, runtime: newRuntime, client: newClient, store: newStore, connection: uiConnectionInfo{ContextName: "new"}})
	if app.runtime != newRuntime || app.client != newClient || app.store != newStore || app.runtimeGen != 5 {
		t.Fatalf("runtime bundle was not replaced atomically: %#v", app)
	}
	if app.state.ClientID != "" || app.state.TunnelID != "" {
		t.Fatalf("resource selections were not reset: %#v", app.state)
	}
	if app.state.Message != "" {
		t.Fatalf("successful activation message = %q, want header-only confirmation", app.state.Message)
	}
	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("previous runtime was not canceled")
	}
	plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{Target: "shell", RstreamURL: "rstrm://shell"}, slog.Default(), "", false)
	if err != nil || selection != nil || plan == nil || plan.config == nil || plan.config.DialContext == nil {
		t.Fatalf("WebTTY did not use activated runtime bundle: plan=%#v selection=%#v err=%v", plan, selection, err)
	}
}

func TestUIRuntimeSwitchPresentationHidesAndRestoresPreviousInventory(t *testing.T) {
	oldServer := webtty.ServerInfo{Target: "old-shell", TunnelID: "old-tunnel", Status: "online"}
	app := &uiApp{
		table:       tview.NewTable().SetFixed(1, 0).SetSelectable(true, false),
		detail:      tview.NewTextView(),
		state:       uiState{View: uiViewWebTTY, TunnelID: oldServer.TunnelID},
		snapshot:    uiSnapshot{Connected: true, WebTTY: []webtty.ServerInfo{oldServer}},
		connection:  uiConnectionInfo{ContextName: "old-context", Engine: "old.example:443"},
		activePage:  uiPageInventory,
		switchingTo: "new-context",
	}
	app.renderInventory()
	if cell := app.table.GetCell(1, 0); cell == nil || cell.Text != "Switching to new-context..." {
		t.Fatalf("pending inventory cell = %#v", cell)
	}
	if len(app.webttyRows) != 0 || app.detail.GetText(false) != " " {
		t.Fatalf("previous resources remained visible: rows=%#v detail=%q", app.webttyRows, app.detail.GetText(false))
	}
	meta := app.inventoryMetaText()
	if !strings.Contains(meta, "switching to new-context") || strings.Contains(meta, "old-context") || strings.Contains(meta, "old.example") {
		t.Fatalf("pending metadata = %q", meta)
	}
	app.finishRuntimeSwitchPresentation("switch failed")
	if app.switchingTo != "" || app.state.Message != "switch failed" {
		t.Fatalf("restored switch state = target %q message %q", app.switchingTo, app.state.Message)
	}
	if cell := app.table.GetCell(1, 0); cell == nil || cell.Text != oldServer.Target {
		t.Fatalf("restored inventory cell = %#v", cell)
	}
	if !strings.Contains(app.detail.GetText(false), oldServer.Target) {
		t.Fatalf("restored detail = %q", app.detail.GetText(false))
	}
}

func TestUITargetPickerFilteringAndStatus(t *testing.T) {
	picker := &uiTargetPicker{
		filter:      tview.NewInputField(),
		tabs:        tview.NewTextView(),
		table:       tview.NewTable().SetFixed(1, 0).SetSelectable(true, false),
		details:     tview.NewTextView(),
		status:      tview.NewTextView(),
		actions:     tview.NewTextView(),
		mode:        uiTargetContext,
		selectedIDs: make(map[uiTargetKind]string),
	}
	picker.targets = []uiTarget{
		{Kind: uiTargetContext, Current: true, Default: true, Context: config.Context{Name: "device", Engine: "device.example:443"}},
		{Kind: uiTargetProject, Project: controlplane.Project{ID: "project-1", Name: "Production", Endpoint: "prod"}, WorkspaceName: "Operations"},
	}
	app := &uiApp{}
	app.renderTargetPicker(picker)
	if len(picker.filtered) != 1 || picker.filtered[0].Context.Name != "device" {
		t.Fatalf("context targets = %#v, want device only", picker.filtered)
	}
	if current := picker.table.GetCell(1, 0).Text; current != "*" {
		t.Fatalf("CURRENT column = %q, want *", current)
	}
	if defaultValue := picker.table.GetCell(1, 1).Text; defaultValue != "*" {
		t.Fatalf("DEFAULT column = %q, want *", defaultValue)
	}
	if scope := picker.table.GetCell(1, 5).Text; scope != "local" {
		t.Fatalf("SCOPE column = %q, want local", scope)
	}
	if detail := picker.details.GetText(false); !strings.Contains(detail, "Name       device") || strings.Contains(detail, "Current") || strings.Contains(detail, "Default") {
		t.Fatalf("context detail = %q", detail)
	}
	picker.filter.SetText("prod")
	picker.mode = uiTargetProject
	app.renderTargetPicker(picker)
	if len(picker.filtered) != 1 || picker.filtered[0].Project.Name != "Production" {
		t.Fatalf("filtered targets = %#v, want Production", picker.filtered)
	}
	if project := picker.table.GetCell(1, 2).Text; project != "Production" {
		t.Fatalf("PROJECT column = %q, want Production", project)
	}
	if workspace := picker.table.GetCell(1, 3).Text; workspace != "Operations" {
		t.Fatalf("WORKSPACE column = %q, want Operations", workspace)
	}
	if current, defaultValue := picker.table.GetCell(1, 0).Text, picker.table.GetCell(1, 1).Text; current != "" || defaultValue != "" {
		t.Fatalf("project markers = %q/%q, want empty cells", current, defaultValue)
	}
	if status := formatUITargetPickerStatus(picker); status != " " {
		t.Fatalf("normal status = %q, want blank", status)
	}
	picker.loading = false
	picker.projectError = fmt.Errorf("Control Plane unavailable")
	if status := formatUITargetPickerStatus(picker); !strings.Contains(status, "Control Plane unavailable") {
		t.Fatalf("status = %q, want Control Plane error", status)
	}
	if actions := formatUITargetPickerActions(); !strings.Contains(actions, "Use") || !strings.Contains(actions, "Make default") || strings.Contains(actions, "this UI only") {
		t.Fatalf("actions = %q, want compact actions", actions)
	}
	if tabs := formatUITargetPickerTabs(picker); !strings.Contains(tabs, "Contexts") || !strings.Contains(tabs, "Projects") || strings.Contains(tabs, "(1)") {
		t.Fatalf("tabs = %q, want count-free labels", tabs)
	}
}

func testUIInlineAuth(token string) *config.Auth {
	return &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{Kind: config.TokenStorageInline, Value: token}}}
}

func newUIInventoryServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sse" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			http.Error(w, "inventory denied", status)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"state.initial\",\"object\":{}}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
}

func writeUIRuntimeTarget(t *testing.T, serverURL string) (string, uiTarget) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	insecure := true
	contextValue := config.Context{
		Name:      "target",
		Engine:    parsed.Host,
		Auth:      testUIInlineAuth("engine-token"),
		Transport: &config.TransportConfig{Mode: "tls", TLS: &config.TLSConfig{InsecureSkipVerify: &insecure}},
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.WriteAtomic(path, config.Config{Contexts: []config.Context{contextValue}}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	return path, uiTarget{Kind: uiTargetContext, Context: contextValue}
}

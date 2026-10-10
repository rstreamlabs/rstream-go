// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/doctor"
)

func TestRunDoctorWithoutContextStaysLocalAndReportsActionableChecks(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "missing.yaml")
	command := runtimeFlagsCommand(t)
	mustSetFlag(t, command, "config", path)
	report, diagnosticErr := runDoctor(command)
	if !errors.Is(diagnosticErr, doctor.ErrChecksFailed) {
		t.Fatalf("diagnostic error = %v", diagnosticErr)
	}
	if report.ConfigPath != path {
		t.Fatalf("ConfigPath = %q, want %q", report.ConfigPath, path)
	}
	if report.Summary.Pass == 0 || report.Summary.Fail == 0 || report.Summary.Skip == 0 {
		t.Fatalf("summary should include pass/fail/skip checks: %#v", report.Summary)
	}
	got := map[string]doctor.Status{}
	for _, check := range report.Checks {
		got[check.Name] = check.Status
	}
	if got["config"] != doctor.StatusPass || got["context"] != doctor.StatusWarn || got["token"] != doctor.StatusFail || got["engine_address"] != doctor.StatusSkip || got["engine"] != doctor.StatusSkip {
		t.Fatalf("unexpected doctor checks: %#v", got)
	}
	file, err := os.CreateTemp(t.TempDir(), "doctor-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()
	if err := printDoctorTable(file, report); err != nil {
		t.Fatalf("printDoctorTable() error = %v", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "Summary") || !strings.Contains(string(data), "config") {
		t.Fatalf("doctor table output = %q", string(data))
	}
}

func TestRunDoctorReportsUnavailableProjectBeforeTransportFailures(t *testing.T) {
	clearRstreamTestEnv(t)
	token := doctorToken(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/whoami":
			_, _ = w.Write([]byte(`{"id":"user-1","role":"owner"}`))
		case "/api/projects/tunnels/resolve/env-dev-pro":
			_, _ = w.Write([]byte(`{"id":"project-1","workspaceId":"workspace-1","name":"env-dev-pro","endpoint":"env-dev-pro","status":"error","routing":"regional","provider":"aws","region":"eu-west-3","plan":"pro","deployment":"shared","issue":{"category":"billing","code":"billing_subscription_canceled","message":"This project's billing subscription was canceled.","occurredAt":"2026-09-24T08:15:59.040Z"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "dev-pro"}},
		Contexts: []config.Context{{
			Name: "dev-pro", APIURL: server.URL, Engine: "127.0.0.1:1", ProjectEndpoint: "env-dev-pro",
			Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{Kind: config.TokenStorageInline, Value: token}}},
		}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	command := runtimeFlagsCommand(t)
	mustSetFlag(t, command, "config", path)
	report, diagnosticErr := runDoctor(command)
	if !errors.Is(diagnosticErr, doctor.ErrChecksFailed) {
		t.Fatalf("diagnostic error = %v", diagnosticErr)
	}
	checks := make(map[string]doctor.Check, len(report.Checks))
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	project := checks["project"]
	if project.Status != doctor.StatusFail || !strings.Contains(project.Message, "billing subscription was canceled") {
		t.Fatalf("project check = %#v", project)
	}
	if project.Details["issueCategory"] != "billing" || project.Details["issueCode"] != "billing_subscription_canceled" {
		t.Fatalf("project details = %#v", project.Details)
	}
	for _, name := range []string{"engine_address", "dns", "tls", "quic_transport", "tunnel_transport", "engine"} {
		if checks[name].Status != doctor.StatusSkip {
			t.Fatalf("%s check = %#v, want skip", name, checks[name])
		}
	}
}

func doctorToken(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestDoctorCLIMatchesPublicAPI(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	command := runtimeFlagsCommand(t)
	command.SetContext(t.Context())
	command.Flags().Bool("deep", true, "")
	command.Flags().String("output", "json", "")
	mustSetFlag(t, command, "config", path)
	var output bytes.Buffer
	command.SetOut(&output)
	if err := doctorCmd.RunE(command, nil); !errors.Is(err, doctor.ErrChecksFailed) {
		t.Fatalf("CLI diagnostic error: %v", err)
	}
	var cliReport doctor.Report
	if err := json.Unmarshal(output.Bytes(), &cliReport); err != nil {
		t.Fatalf("CLI JSON output: %v", err)
	}
	_, cfg, err := loadConfig(command)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveDoctorRuntime(command, cfg)
	if err != nil {
		t.Fatal(err)
	}
	apiReport, err := doctor.Run(t.Context(), resolved, doctor.Options{Deep: true})
	if !errors.Is(err, doctor.ErrChecksFailed) {
		t.Fatalf("API diagnostic error: %v", err)
	}
	if cliReport.ConfigPath != path || len(cliReport.Checks) != len(apiReport.Checks)+1 || cliReport.Checks[0].Name != "config" {
		t.Fatalf("CLI configuration metadata: %+v", cliReport)
	}
	cliReport.Checks = cliReport.Checks[1:]
	cliReport.Summary.Pass--
	cliReport.ConfigPath = ""
	cliReport.GeneratedAt = apiReport.GeneratedAt
	if !reflect.DeepEqual(cliReport, apiReport) {
		t.Fatalf("CLI and API differ:\nCLI: %+v\nAPI: %+v", cliReport, apiReport)
	}
	output.Reset()
	mustSetFlag(t, command, "output", "table")
	if err := doctorCmd.RunE(command, nil); !errors.Is(err, doctor.ErrChecksFailed) || !strings.Contains(output.String(), "tunnel_creation") || !strings.Contains(output.String(), "Summary") {
		t.Fatalf("CLI table output: %q %v", output.String(), err)
	}
}

func TestDoctorCLIReportsConfigurationFailures(t *testing.T) {
	clearRstreamTestEnv(t)
	for _, malformed := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		command := runtimeFlagsCommand(t)
		mustSetFlag(t, command, "config", path)
		want := "context"
		if malformed {
			want = "config"
			if err := os.WriteFile(path, []byte("invalid: ["), 0o600); err != nil {
				t.Fatal(err)
			}
		} else {
			mustSetFlag(t, command, "context", "missing")
		}
		report, err := runDoctor(command)
		if !errors.Is(err, doctor.ErrChecksFailed) || report.Summary.Fail != 1 || report.Checks[len(report.Checks)-1].Name != want {
			t.Fatalf("configuration failure: %+v %v", report, err)
		}
	}
}

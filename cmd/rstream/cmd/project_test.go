// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func TestListProjectsParamsFromFlags(t *testing.T) {
	command := projectListFlagsCommand()
	for _, set := range [][2]string{
		{"q", "prod"},
		{"page", "2"},
		{"page-size", "50"},
		{"sort", "name"},
		{"order", "desc"},
	} {
		mustSetFlag(t, command, set[0], set[1])
	}
	params, sortRequested, err := listProjectsParamsFromFlags(command)
	if err != nil {
		t.Fatalf("listProjectsParamsFromFlags() error = %v", err)
	}
	if !sortRequested || params.Query != "prod" || params.Sort != "name" || params.Order != "desc" {
		t.Fatalf("unexpected params: %#v sort=%v", params, sortRequested)
	}
	if params.Page == nil || *params.Page != 2 || params.PageSize == nil || *params.PageSize != 50 {
		t.Fatalf("pagination flags not parsed: %#v", params)
	}
}

func TestListProjectsParamsFromFlagsValidation(t *testing.T) {
	cases := []struct {
		name  string
		flag  string
		value string
	}{
		{name: "page", flag: "page", value: "0"},
		{name: "page size", flag: "page-size", value: "101"},
		{name: "sort", flag: "sort", value: "region"},
		{name: "order", flag: "order", value: "sideways"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			command := projectListFlagsCommand()
			mustSetFlag(t, command, tt.flag, tt.value)
			if _, _, err := listProjectsParamsFromFlags(command); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestProjectContextHelpers(t *testing.T) {
	cfg := &config.Config{Contexts: []config.Context{
		{Name: "my-project", APIURL: "https://api.example.com", ProjectEndpoint: "demo.rstream.io"},
		{Name: "my-project-2"},
	}}
	if got := findContextByProjectEndpoint(cfg, "https://api.example.com", "demo.rstream.io"); got == nil || got.Name != "my-project" {
		t.Fatalf("project context lookup failed: %#v", got)
	}
	if got := uniqueContextName("my-project", cfg); got != "my-project-3" {
		t.Fatalf("uniqueContextName() = %q", got)
	}
	if got := slugifyName(" My Project: EU/West! "); got != "my-project-eu-west" {
		t.Fatalf("slugifyName() = %q", got)
	}
	if got := endpointPrefix("demo.rstream.io:443"); got != "demo" {
		t.Fatalf("endpointPrefix() = %q", got)
	}
	if !isAllowed("name", "id", "name") || isAllowed("region", "id", "name") {
		t.Fatalf("isAllowed returned unexpected result")
	}
}

func TestMapControlPlaneError(t *testing.T) {
	err := mapControlPlaneError(controlplane.ErrUnauthorized)
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("unauthorized error not mapped: %v", err)
	}
	other := errors.New("boom")
	if got := mapControlPlaneError(other); got != other {
		t.Fatalf("non-Control plane API error should be returned as-is")
	}
}

func projectListFlagsCommand() *cobra.Command {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("q", "", "")
	command.Flags().Int("page", 0, "")
	command.Flags().Int("page-size", 0, "")
	command.Flags().String("sort", "", "")
	command.Flags().String("order", "", "")
	return command
}

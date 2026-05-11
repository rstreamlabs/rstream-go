// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestSortWebTTYServers(t *testing.T) {
	list := []webtty.ServerInfo{
		{Target: "shell", Publish: true, TunnelID: "b"},
		{Target: "api", Publish: true, TunnelID: "c"},
		{Target: "shell", Publish: false, TunnelID: "a"},
	}
	sortWebTTYServers(list)
	if list[0].Target != "api" || list[1].TunnelID != "a" || list[2].TunnelID != "b" {
		t.Fatalf("unexpected order: %#v", list)
	}
}

func TestPrintWebTTYServersOutput(t *testing.T) {
	hostname := "shell.example.com"
	osPretty := "Ubuntu 24.04 LTS"
	arch := "arm64"
	host := "https://shell.example.com"
	list := []webtty.ServerInfo{{
		RstreamURL:   "rstrm://shell",
		Hostname:     &hostname,
		OSPrettyName: &osPretty,
		Arch:         &arch,
		Host:         &host,
	}}
	var table bytes.Buffer
	if err := printWebTTYServersTable(&table, list); err != nil {
		t.Fatalf("print table: %v", err)
	}
	tableOutput := table.String()
	for _, want := range []string{"RSTREAM_URL", "rstrm://shell", "Ubuntu 24.04 LTS", "arm64"} {
		if !strings.Contains(tableOutput, want) {
			t.Fatalf("table output missing %q:\n%s", want, tableOutput)
		}
	}
	var jsonOut bytes.Buffer
	if err := printWebTTYServersJSON(&jsonOut, list); err != nil {
		t.Fatalf("print json: %v", err)
	}
	if !strings.Contains(jsonOut.String(), `"rstream_url": "rstrm://shell"`) {
		t.Fatalf("json output missing rstream URL:\n%s", jsonOut.String())
	}
}

func TestWebTTYDisplayHelpers(t *testing.T) {
	if got := webTTYValue(nil); got != "-" {
		t.Fatalf("nil value = %q", got)
	}
	blank := "  "
	if got := webTTYValue(&blank); got != "-" {
		t.Fatalf("blank value = %q", got)
	}
	family := "linux"
	if got := webTTYSystem(webtty.ServerInfo{OSFamily: &family}); got != "linux" {
		t.Fatalf("system fallback = %q", got)
	}
	pretty := "Ubuntu"
	if got := webTTYSystem(webtty.ServerInfo{OSPrettyName: &pretty, OSFamily: &family}); got != "Ubuntu" {
		t.Fatalf("pretty system = %q", got)
	}
}

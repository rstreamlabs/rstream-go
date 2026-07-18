// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func TestWriteProjectTCPAddressesTable(t *testing.T) {
	var out bytes.Buffer
	err := writeProjectTCPAddressesTable(&out, []controlplane.ProjectTCPAddress{
		{ID: "address-2", Address: "tcp.example.com:10043", Port: 10043},
		{ID: "address-1", Address: "tcp.example.com:10042", Port: 10042},
	})
	if err != nil {
		t.Fatalf("writeProjectTCPAddressesTable() error = %v", err)
	}
	result := out.String()
	if strings.Index(result, "10042") > strings.Index(result, "10043") {
		t.Fatalf("addresses are not sorted by port:\n%s", result)
	}
	for _, expected := range []string{"ADDRESS", "tcp.example.com:10042", "address-1"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("address table missing %q:\n%s", expected, result)
		}
	}
}

func TestWriteProjectDomainsTable(t *testing.T) {
	var out bytes.Buffer
	err := writeProjectDomainsTable(&out, []controlplane.ProjectDomain{{"id": "domain-1", "hostname": "app.example.com", "status": "active"}})
	if err != nil {
		t.Fatalf("writeProjectDomainsTable() error = %v", err)
	}
	for _, expected := range []string{"HOSTNAME", "app.example.com", "active", "domain-1"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("domain table missing %q:\n%s", expected, out.String())
		}
	}
}

func TestWriteProjectResourceResult(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("output", "table", "")
	var out bytes.Buffer
	command.SetOut(&out)
	err := writeProjectResourceResult(command, controlplane.ProjectTCPAddress{ID: "address-1", Address: "tcp.example.com:10042"})
	if err != nil {
		t.Fatalf("writeProjectResourceResult() error = %v", err)
	}
	if !strings.Contains(out.String(), "tcp.example.com:10042") {
		t.Fatalf("unexpected result:\n%s", out.String())
	}
}

func TestFindProjectDomainID(t *testing.T) {
	domains := []controlplane.ProjectDomain{
		{"id": "domain-1", "hostname": "app.example.com"},
		{"id": "domain-2", "hostname": "*.example.com"},
	}
	if id, ok := findProjectDomainID(domains, " APP.EXAMPLE.COM "); !ok || id != "domain-1" {
		t.Fatalf("findProjectDomainID() = %q, %t", id, ok)
	}
	if _, ok := findProjectDomainID(domains, "missing.example.com"); ok {
		t.Fatal("findProjectDomainID() unexpectedly found missing hostname")
	}
}

func TestFindProjectTCPAddressID(t *testing.T) {
	addresses := []controlplane.ProjectTCPAddress{
		{ID: "address-1", Port: 10042},
		{ID: "address-2", Port: 10043},
	}
	if id, ok := findProjectTCPAddressID(addresses, 10043); !ok || id != "address-2" {
		t.Fatalf("findProjectTCPAddressID() = %q, %t", id, ok)
	}
	if _, ok := findProjectTCPAddressID(addresses, 10044); ok {
		t.Fatal("findProjectTCPAddressID() unexpectedly found missing port")
	}
}

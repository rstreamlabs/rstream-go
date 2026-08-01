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

func TestNewProjectDomainCreateRequest(t *testing.T) {
	tests := []struct {
		name               string
		hostname           string
		kind               string
		validation         string
		validationExplicit bool
		want               controlplane.CreateProjectDomainRequest
		wantError          string
	}{
		{
			name:       "hostname defaults to TLS-ALPN-01",
			hostname:   " API.Example.com ",
			kind:       "hostname",
			validation: "tls-alpn-01",
			want: controlplane.CreateProjectDomainRequest{
				Hostname:              "api.example.com",
				Kind:                  controlplane.ProjectDomainKindHostname,
				CertificateValidation: controlplane.ProjectDomainCertificateValidationTLSALPN01,
			},
		},
		{
			name:       "wildcard defaults to DNS-01",
			hostname:   "example.com",
			kind:       "wildcard",
			validation: "tls-alpn-01",
			want: controlplane.CreateProjectDomainRequest{
				Hostname:              "example.com",
				Kind:                  controlplane.ProjectDomainKindWildcard,
				CertificateValidation: controlplane.ProjectDomainCertificateValidationDNS01,
			},
		},
		{
			name:               "explicit wildcard TLS validation is rejected",
			hostname:           "example.com",
			kind:               "wildcard",
			validation:         "tls-alpn-01",
			validationExplicit: true,
			wantError:          "wildcard domains require DNS-01 certificate validation",
		},
		{
			name:       "unknown kind is rejected",
			hostname:   "example.com",
			kind:       "apex",
			validation: "dns-01",
			wantError:  "invalid domain kind",
		},
		{
			name:       "unknown validation is rejected",
			hostname:   "example.com",
			kind:       "hostname",
			validation: "http-01",
			wantError:  "invalid certificate validation method",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newProjectDomainCreateRequest(test.hostname, test.kind, test.validation, test.validationExplicit)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("newProjectDomainCreateRequest() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("newProjectDomainCreateRequest() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("newProjectDomainCreateRequest() = %+v, want %+v", got, test.want)
			}
		})
	}
}

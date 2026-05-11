// See LICENSE file in the project root for license information.

package controlplane

import "testing"

func TestProjectEngineAddressPrefersEndpointDomainAndDefaultsPort(t *testing.T) {
	project := Project{
		Endpoint: " endpoint-1 ",
		Domain:   " cluster.example.test ",
	}
	if got := project.EngineAddress(); got != "endpoint-1.cluster.example.test:443" {
		t.Fatalf("EngineAddress() = %q", got)
	}
	project.EnginePort = 8443
	if got := project.EngineAddress(); got != "endpoint-1.cluster.example.test:8443" {
		t.Fatalf("EngineAddress() with port = %q", got)
	}
}

func TestProjectEngineAddressFallsBackToURL(t *testing.T) {
	if got := (Project{URL: " engine.example.test "}).EngineAddress(); got != "engine.example.test:443" {
		t.Fatalf("EngineAddress() URL default port = %q", got)
	}
	if got := (Project{URL: "engine.example.test:8443"}).EngineAddress(); got != "engine.example.test:8443" {
		t.Fatalf("EngineAddress() URL explicit port = %q", got)
	}
	if got := (Project{}).EngineAddress(); got != "" {
		t.Fatalf("EngineAddress() empty = %q", got)
	}
}

// See LICENSE file in the project root for license information.

package controlplane

import (
	"strings"
	"testing"
)

func TestProjectEngineAddressForRegion(t *testing.T) {
	project := Project{
		Endpoint:   "project",
		Domain:     "global.example.test",
		EnginePort: 443,
		Routing:    "global",
		RegionalEndpoints: []ProjectRegionalEndpoint{
			{Provider: "aws", Region: "eu-west-3", Domain: "eu.example.test", EnginePort: 8443},
			{Provider: "aws", Region: "us-east-1", Domain: "us.example.test", EnginePort: 443},
		},
	}
	automatic, err := project.EngineAddressForRegion("auto")
	if err != nil {
		t.Fatal(err)
	}
	if automatic != "project.global.example.test:443" {
		t.Fatalf("unexpected automatic engine address %q", automatic)
	}
	regional, err := project.EngineAddressForRegion(" US-EAST-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if regional != "project.us.example.test:443" {
		t.Fatalf("unexpected regional engine address %q", regional)
	}
	_, err = project.EngineAddressForRegion("ap-southeast-1")
	if err == nil || !strings.Contains(err.Error(), "available: eu-west-3, us-east-1") {
		t.Fatalf("expected available region error, got %v", err)
	}
}

func TestProjectEngineAddressForRegionRejectsRegionalProject(t *testing.T) {
	project := Project{
		Endpoint:   "project",
		Domain:     "regional.example.test",
		EnginePort: 443,
		Routing:    "regional",
		RegionalEndpoints: []ProjectRegionalEndpoint{{
			Region:     "eu-west-3",
			Domain:     "regional.example.test",
			EnginePort: 443,
		}},
	}
	if _, err := project.EngineAddressForRegion("eu-west-3"); err == nil || !strings.Contains(err.Error(), "only available for global projects") {
		t.Fatalf("expected regional project error, got %v", err)
	}
	if engine, err := project.EngineAddressForRegion("auto"); err != nil || engine != "project.regional.example.test:443" {
		t.Fatalf("automatic engine address = %q, %v", engine, err)
	}
}

func TestProjectEngineAddressForRegionRejectsAmbiguousAndInvalidEndpoints(t *testing.T) {
	project := Project{
		Endpoint: "project",
		Routing:  "global",
		RegionalEndpoints: []ProjectRegionalEndpoint{
			{Region: "eu-west-3", Domain: "eu-1.example.test", EnginePort: 443},
			{Region: "EU-WEST-3", Domain: "eu-2.example.test", EnginePort: 443},
		},
	}
	if _, err := project.EngineAddressForRegion("eu-west-3"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous region error, got %v", err)
	}
	project.RegionalEndpoints = []ProjectRegionalEndpoint{{Region: "eu-west-3", Domain: "", EnginePort: 443}}
	if _, err := project.EngineAddressForRegion("eu-west-3"); err == nil || !strings.Contains(err.Error(), "invalid engine endpoint") {
		t.Fatalf("expected invalid endpoint error, got %v", err)
	}
	if _, err := project.EngineAddressForRegion("eu west 3"); err == nil || !strings.Contains(err.Error(), "can only contain") {
		t.Fatalf("expected invalid region error, got %v", err)
	}
}

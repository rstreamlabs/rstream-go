// See LICENSE file in the project root for license information.

package cmd

import "testing"

func TestBuildClientListParams(t *testing.T) {
	params, err := buildClientListParams("id=c1,status=online,user_id=u1,agent=rstream,os=linux,arch=arm64,protocol_version=1.0,labels.env=prod,label.service=*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params == nil || params.Filters == nil {
		t.Fatalf("expected filters, got %#v", params)
	}
	if params.Filters.ID == nil || *params.Filters.ID != "c1" {
		t.Fatalf("unexpected id: %#v", params.Filters.ID)
	}
	if params.Filters.Status == nil || *params.Filters.Status != "online" {
		t.Fatalf("unexpected status: %#v", params.Filters.Status)
	}
	if params.Filters.UserID == nil || *params.Filters.UserID != "u1" {
		t.Fatalf("unexpected user_id: %#v", params.Filters.UserID)
	}
	if params.Filters.Agent == nil || *params.Filters.Agent != "rstream" {
		t.Fatalf("unexpected agent: %#v", params.Filters.Agent)
	}
	if params.Filters.OS == nil || *params.Filters.OS != "linux" {
		t.Fatalf("unexpected os: %#v", params.Filters.OS)
	}
	if params.Filters.Arch == nil || *params.Filters.Arch != "arm64" {
		t.Fatalf("unexpected arch: %#v", params.Filters.Arch)
	}
	if params.Filters.ProtocolVersion == nil || *params.Filters.ProtocolVersion != "1.0" {
		t.Fatalf("unexpected protocol_version: %#v", params.Filters.ProtocolVersion)
	}
	if got, ok := params.Filters.Labels["env"]; !ok || got == nil || *got != "prod" {
		t.Fatalf("unexpected env label: %#v", params.Filters.Labels)
	}
	if got, ok := params.Filters.Labels["service"]; !ok || got != nil {
		t.Fatalf("unexpected service label: %#v", params.Filters.Labels)
	}
}

func TestBuildClientListParamsRejectsUnknownFilterKey(t *testing.T) {
	if _, err := buildClientListParams("foo=bar"); err == nil {
		t.Fatal("expected unknown filter key error")
	}
}

func TestBuildClientListParamsRejectsRemovedFilterKey(t *testing.T) {
	if _, err := buildClientListParams("project_id=p1"); err == nil {
		t.Fatal("expected removed filter key error")
	}
}

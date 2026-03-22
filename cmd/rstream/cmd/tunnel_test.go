// See LICENSE file in the project root for license information.

package cmd

import "testing"

func TestBuildListParams(t *testing.T) {
	params, err := buildTunnelListParams("id=t1,name=ssh-prod-01,type=bytestream,status=online,client_id=c1,user_id=u1,protocol=http,publish=true,http_version=h3,labels.env=prod,label.service=*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params == nil || params.Filters == nil {
		t.Fatalf("expected filters, got %#v", params)
	}
	if params.Filters.ID == nil || *params.Filters.ID != "t1" {
		t.Fatalf("unexpected id: %#v", params.Filters.ID)
	}
	if params.Filters.Name == nil || *params.Filters.Name != "ssh-prod-01" {
		t.Fatalf("unexpected name: %#v", params.Filters.Name)
	}
	if params.Filters.Type == nil || *params.Filters.Type != "bytestream" {
		t.Fatalf("unexpected type: %#v", params.Filters.Type)
	}
	if params.Filters.Status == nil || *params.Filters.Status != "online" {
		t.Fatalf("unexpected status: %#v", params.Filters.Status)
	}
	if params.Filters.ClientID == nil || *params.Filters.ClientID != "c1" {
		t.Fatalf("unexpected client_id: %#v", params.Filters.ClientID)
	}
	if params.Filters.UserID == nil || *params.Filters.UserID != "u1" {
		t.Fatalf("unexpected user_id: %#v", params.Filters.UserID)
	}
	if params.Filters.Protocol == nil || *params.Filters.Protocol != "http" {
		t.Fatalf("unexpected protocol: %#v", params.Filters.Protocol)
	}
	if params.Filters.Publish == nil || !*params.Filters.Publish {
		t.Fatalf("unexpected publish: %#v", params.Filters.Publish)
	}
	if params.Filters.HTTPVersion == nil || *params.Filters.HTTPVersion != "h3" {
		t.Fatalf("unexpected http_version: %#v", params.Filters.HTTPVersion)
	}
	if got, ok := params.Filters.Labels["env"]; !ok || got == nil || *got != "prod" {
		t.Fatalf("unexpected env label: %#v", params.Filters.Labels)
	}
	if got, ok := params.Filters.Labels["service"]; !ok || got != nil {
		t.Fatalf("unexpected service label: %#v", params.Filters.Labels)
	}
}

func TestBuildListParamsRejectsUnknownFilterKey(t *testing.T) {
	if _, err := buildTunnelListParams("foo=bar"); err == nil {
		t.Fatal("expected unknown filter key error")
	}
}

func TestBuildListParamsRejectsInvalidBoolean(t *testing.T) {
	if _, err := buildTunnelListParams("publish=maybe"); err == nil {
		t.Fatal("expected invalid boolean error")
	}
}

func TestBuildListParamsRejectsRemovedFilterKey(t *testing.T) {
	if _, err := buildTunnelListParams("project_id=p1"); err == nil {
		t.Fatal("expected removed filter key error")
	}
}

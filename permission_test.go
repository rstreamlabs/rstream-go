// See LICENSE file in the project root for license information.

package rstream

import "testing"

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
func u16p(v uint16) *uint16 { return &v }

func TestMatchPermissions(t *testing.T) {
	base := TunnelProperties{
		Name:          strp("my.tunnel"),
		Publish:       boolp(true),
		TLSMinVersion: u16p(772),
		Labels:        map[string]string{"env": "prod"},
	}
	noPublish := base
	noPublish.Publish = nil
	tests := []struct {
		name    string
		props   TunnelProperties
		perms   string
		action  string
		want    bool
		wantErr bool
	}{
		{"action absent", base, `{}`, "connect", true, false},
		{"action bool false", base, `{"connect":false}`, "connect", false, false},
		{"action bool true", base, `{"connect":true}`, "connect", true, false},
		{"action empty object", base, `{"connect":{}}`, "connect", true, false},
		{"AND negative", base, `{"connect":{"filters":{"AND":[{"name":"my.tunnel"},{"publish":false}]}}}`, "connect", false, false},
		{"AND positive", base, `{"connect":{"filters":{"AND":[{"name":"my.tunnel"},{"publish":true}]}}}`, "connect", true, false},
		{"bool negative", base, `{"connect":{"filters":{"publish":false}}}`, "connect", false, false},
		{"bool positive", base, `{"connect":{"filters":{"publish":true}}}`, "connect", true, false},
		{"exact string negative", base, `{"connect":{"filters":{"name":"other"}}}`, "connect", false, false},
		{"exact string positive", base, `{"connect":{"filters":{"name":"my.tunnel"}}}`, "connect", true, false},
		{"invalid permissions json", base, `invalid`, "connect", false, true},
		{"invalid regex", base, `{"connect":{"filters":{"name":{"regex":"*(bad"}}}}`, "connect", false, true},
		{"multi field negative", base, `{"connect":{"filters":{"name":"my.tunnel","publish":false}}}`, "connect", false, false},
		{"multi field positive", base, `{"connect":{"filters":{"name":"my.tunnel","publish":true}}}`, "connect", true, false},
		{"nested map negative", base, `{"connect":{"filters":{"labels":{"env":"stage"}}}}`, "connect", false, false},
		{"nested map positive", base, `{"connect":{"filters":{"labels":{"env":"prod"}}}}`, "connect", true, false},
		{"nested regex positive", base, `{"connect":{"filters":{"labels":{"env":{"regex":"^pro.*$"}}}}}`, "connect", true, false},
		{"numeric negative", base, `{"connect":{"filters":{"tls_min_version":771}}}`, "connect", false, false},
		{"numeric positive", base, `{"connect":{"filters":{"tls_min_version":772}}}`, "connect", true, false},
		{"oneof negative", base, `{"connect":{"filters":{"name":{"oneof":["a","b"]}}}}`, "connect", false, false},
		{"oneof positive", base, `{"connect":{"filters":{"name":{"oneof":["a","my.tunnel"]}}}}`, "connect", true, false},
		{"OR negative", base, `{"connect":{"filters":{"OR":[{"name":"a"},{"name":"b"}]}}}`, "connect", false, false},
		{"OR positive", base, `{"connect":{"filters":{"OR":[{"name":"other"},{"name":"my.tunnel"}]}}}`, "connect", true, false},
		{"publish nil negative", noPublish, `{"connect":{"filters":{"publish":true}}}`, "connect", false, false},
		{"regex negative", base, `{"connect":{"filters":{"name":{"regex":"^other"}}}}`, "connect", false, false},
		{"regex positive", base, `{"connect":{"filters":{"name":{"regex":"^my\\..*$"}}}}`, "connect", true, false},
	}
	for _, tc := range tests {
		got, err := MatchPermissions(&tc.props, []byte(tc.perms), tc.action)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error, got nil", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

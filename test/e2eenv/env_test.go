// See LICENSE file in the project root for license information.

package e2eenv

import (
	"os"
	"testing"
)

func TestAllowCrossRegionRouting(t *testing.T) {
	original, wasSet := os.LookupEnv(allowCrossRegionRoutingEnv)
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(allowCrossRegionRoutingEnv, original)
			return
		}
		_ = os.Unsetenv(allowCrossRegionRoutingEnv)
	})
	tests := []struct {
		name    string
		value   string
		set     bool
		want    *bool
		wantErr bool
	}{
		{name: "unset"},
		{name: "enabled", value: "1", set: true, want: boolPtr(true)},
		{name: "disabled", value: "0", set: true, want: boolPtr(false)},
		{name: "invalid", value: "sometimes", set: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Unsetenv(allowCrossRegionRoutingEnv)
			if tt.set {
				_ = os.Setenv(allowCrossRegionRoutingEnv, tt.value)
			}
			got, err := AllowCrossRegionRouting()
			if tt.wantErr {
				if err == nil {
					t.Fatal("AllowCrossRegionRouting() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("AllowCrossRegionRouting() error = %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("AllowCrossRegionRouting() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("AllowCrossRegionRouting() = %v, want %v", got, *tt.want)
			}
		})
	}
}

func boolPtr(value bool) *bool {
	return &value
}

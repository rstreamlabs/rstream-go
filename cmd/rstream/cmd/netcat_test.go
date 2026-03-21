// See LICENSE file in the project root for license information.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func newTestNetcatCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "netcat"}
	cmd.Flags().String("listen", "", "")
	cmd.Flags().String("remote", "", "")
	cmd.Flags().String("exec", "", "")
	cmd.Flags().String("sh-exec", "", "")
	cmd.Flags().Bool("interactive", false, "")
	cmd.Flags().Bool("no-interactive", false, "")
	return cmd
}

func TestValidateNetcatFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		config  func(*cobra.Command) error
		wantErr bool
	}{
		{
			name:    "client mode accepts one positional endpoint",
			args:    []string{"127.0.0.1:22"},
			wantErr: false,
		},
		{
			name:    "client mode requires positional endpoint",
			wantErr: true,
		},
		{
			name: "server mode requires backend selector",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("listen", ":2222")
			},
			wantErr: true,
		},
		{
			name: "server mode accepts remote",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", ":2222"); err != nil {
					return err
				}
				return cmd.Flags().Set("remote", "127.0.0.1:22")
			},
			wantErr: false,
		},
		{
			name: "server mode rejects interactive flags",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", ":2222"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("remote", "127.0.0.1:22"); err != nil {
					return err
				}
				return cmd.Flags().Set("interactive", "true")
			},
			wantErr: true,
		},
		{
			name: "server mode rejects positional args",
			args: []string{"127.0.0.1:22"},
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", ":2222"); err != nil {
					return err
				}
				return cmd.Flags().Set("remote", "127.0.0.1:22")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestNetcatCommand()
			if tt.config != nil {
				if err := tt.config(cmd); err != nil {
					t.Fatalf("failed to configure command: %v", err)
				}
			}
			err := validateNetcatFlags(cmd, tt.args)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseNetcatDialTarget(t *testing.T) {
	t.Run("tcp target", func(t *testing.T) {
		target, err := parseNetcatDialTarget("127.0.0.1:22")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target.Kind != netcatEndpointTCP {
			t.Fatalf("unexpected target kind: %v", target.Kind)
		}
		if target.Address != "127.0.0.1:22" {
			t.Fatalf("unexpected target address: %q", target.Address)
		}
	})
	t.Run("rstream target", func(t *testing.T) {
		target, err := parseNetcatDialTarget("rstrm://ssh-server")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target.Kind != netcatEndpointRstream {
			t.Fatalf("unexpected target kind: %v", target.Kind)
		}
		if target.Address != "ssh-server" {
			t.Fatalf("unexpected tunnel address: %q", target.Address)
		}
	})
	t.Run("reject missing tunnel identifier", func(t *testing.T) {
		if _, err := parseNetcatDialTarget("rstrm://"); err == nil {
			t.Fatalf("expected error for missing tunnel identifier")
		}
	})
}

func TestParseNetcatListenTarget(t *testing.T) {
	t.Run("plain tcp", func(t *testing.T) {
		target, err := parseNetcatListenTarget(":2222")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target.Kind != netcatEndpointTCP {
			t.Fatalf("unexpected target kind: %v", target.Kind)
		}
		if target.Address != ":2222" {
			t.Fatalf("unexpected target address: %q", target.Address)
		}
	})
	t.Run("named rstream tunnel", func(t *testing.T) {
		target, err := parseNetcatListenTarget("rstrm://ssh-server")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target.Kind != netcatEndpointRstream {
			t.Fatalf("unexpected target kind: %v", target.Kind)
		}
		if target.Name == nil || *target.Name != "ssh-server" {
			t.Fatalf("unexpected tunnel name: %#v", target.Name)
		}
	})
	t.Run("anonymous rstream tunnel", func(t *testing.T) {
		target, err := parseNetcatListenTarget("rstrm://")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target.Kind != netcatEndpointRstream {
			t.Fatalf("unexpected target kind: %v", target.Kind)
		}
		if target.Name != nil {
			t.Fatalf("expected unnamed tunnel, got %#v", target.Name)
		}
	})
}

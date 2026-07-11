// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
)

func TestNetcatRunResultMasksContextCancellation(t *testing.T) {
	if err := netcatRunResult(context.Canceled); err != nil {
		t.Fatalf("netcatRunResult(context.Canceled) = %v, want nil", err)
	}
	if err := netcatRunResult(fmt.Errorf("session: %w", context.Canceled)); err != nil {
		t.Fatalf("netcatRunResult(wrapped cancel) = %v, want nil", err)
	}
	cause := errors.New("session failed")
	if err := netcatRunResult(cause); !errors.Is(err, cause) {
		t.Fatalf("netcatRunResult(real error) = %v, want passthrough", err)
	}
	if err := netcatRunResult(nil); err != nil {
		t.Fatalf("netcatRunResult(nil) = %v, want nil", err)
	}
}

func newTestNetcatCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "netcat"}
	cmd.Flags().String("listen", "", "")
	cmd.Flags().String("remote", "", "")
	cmd.Flags().String("exec", "", "")
	cmd.Flags().String("sh-exec", "", "")
	cmd.Flags().Bool("interactive", false, "")
	cmd.Flags().Bool("no-interactive", false, "")
	cmd.Flags().Bool("datagram", false, "")
	cmd.Flags().String("framing", string(netcatFramingRFC4571), "")
	cmd.Flags().Duration("idle-timeout", 0, "")
	cmd.Flags().Bool("datagram-guaranteed-delivery", false, "")
	cmd.Flags().String("udp-peer", "", "")
	cmd.Flags().Int("max-connections", defaultNetcatMaxConns, "")
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
			name: "server mode rejects invalid max connections",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", ":2222"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("remote", "127.0.0.1:22"); err != nil {
					return err
				}
				return cmd.Flags().Set("max-connections", "0")
			},
			wantErr: true,
		},
		{
			name: "client mode rejects max connections",
			args: []string{"127.0.0.1:22"},
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("max-connections", "2")
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
		{
			name: "client mode accepts exec",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("sh-exec", "cat")
			},
			wantErr: false,
		},
		{
			name: "client mode rejects interactive flags with exec",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("sh-exec", "cat"); err != nil {
					return err
				}
				return cmd.Flags().Set("interactive", "true")
			},
			wantErr: true,
		},
		{
			name: "client mode rejects remote",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("remote", "127.0.0.1:22")
			},
			wantErr: true,
		},
		{
			name: "framing requires datagram",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("framing", "rfc4571")
			},
			wantErr: true,
		},
		{
			name: "idle timeout requires datagram",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("idle-timeout", "30s")
			},
			wantErr: true,
		},
		{
			name: "datagram client accepts rfc4571 framing",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("framing", "rfc4571")
			},
			wantErr: false,
		},
		{
			name: "datagram rejects unsupported framing",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("framing", "none")
			},
			wantErr: true,
		},
		{
			name: "datagram rejects negative idle timeout",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("idle-timeout", "-1s")
			},
			wantErr: true,
		},
		{
			name: "datagram server mode rejects remote",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "rstrm://media"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("remote", "127.0.0.1:5004")
			},
			wantErr: true,
		},
		{
			name: "datagram server mode accepts exec",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "rstrm://media"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("sh-exec", "cat")
			},
			wantErr: false,
		},
		{
			name: "guaranteed delivery requires datagram",
			args: []string{"rstrm://media"},
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("datagram-guaranteed-delivery", "true")
			},
			wantErr: true,
		},
		{
			name: "guaranteed delivery accepted for created datagram tunnel",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "rstrm://media"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("sh-exec", "cat"); err != nil {
					return err
				}
				return cmd.Flags().Set("datagram-guaranteed-delivery", "true")
			},
			wantErr: false,
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

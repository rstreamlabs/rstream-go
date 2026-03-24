// See LICENSE file in the project root for license information.

package cmd

import (
	"testing"

	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

func newTestWebTTYServerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "server"}
	cmd.Flags().String("listen", ":8080", "")
	cmd.Flags().Bool("rstream", false, "")
	cmd.Flags().Bool("web", false, "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().Bool("publish", false, "")
	cmd.Flags().Bool("no-publish", false, "")
	return cmd
}

func TestWebTTYServerUsesRstream(t *testing.T) {
	cmd := newTestWebTTYServerCommand()
	if webttyServerUsesRstream(cmd) {
		t.Fatalf("expected --rstream to be disabled by default")
	}
	if err := cmd.Flags().Set("rstream", "true"); err != nil {
		t.Fatalf("failed to set --rstream: %v", err)
	}
	if !webttyServerUsesRstream(cmd) {
		t.Fatalf("expected --rstream to enable rstream mode")
	}
	cmd = newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("web", "true"); err != nil {
		t.Fatalf("failed to set --web: %v", err)
	}
	if !webttyServerUsesRstream(cmd) {
		t.Fatalf("expected --web alias to enable rstream mode")
	}
}

func TestValidateWebTTYServerFlags(t *testing.T) {
	tests := []struct {
		name    string
		config  func(*cobra.Command) error
		wantErr bool
	}{
		{
			name: "name requires rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("name", "shell")
			},
			wantErr: true,
		},
		{
			name: "publish requires rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("publish", "true")
			},
			wantErr: true,
		},
		{
			name: "no publish requires rstream",
			config: func(cmd *cobra.Command) error {
				return cmd.Flags().Set("no-publish", "true")
			},
			wantErr: true,
		},
		{
			name: "listen conflicts with rstream alias",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", ":9090"); err != nil {
					return err
				}
				return cmd.Flags().Set("web", "true")
			},
			wantErr: true,
		},
		{
			name: "rstream without listen override is valid",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("rstream", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("name", "shell")
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestWebTTYServerCommand()
			if err := tt.config(cmd); err != nil {
				t.Fatalf("failed to configure flags: %v", err)
			}
			err := validateWebTTYServerFlags(cmd)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewWebTTYServerTunnelProperties(t *testing.T) {
	t.Run("published by default", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		props := newWebTTYServerTunnelProperties(cmd)
		if props.Publish == nil || !*props.Publish {
			t.Fatalf("expected published tunnel by default")
		}
		if props.Protocol == nil || *props.Protocol != "http" {
			t.Fatalf("expected HTTP protocol for published tunnel, got %#v", props.Protocol)
		}
		if props.HTTPVersion == nil || *props.HTTPVersion != "http/1.1" {
			t.Fatalf("expected HTTP/1.1 for published tunnel, got %#v", props.HTTPVersion)
		}
		if props.TokenAuth == nil || !*props.TokenAuth {
			t.Fatalf("expected token auth for published tunnel")
		}
		if got := props.Labels[webtty.WebTTYApplicationProtocolKey]; got != webtty.WebTTYApplicationProtocol {
			t.Fatalf("unexpected application-protocol label: got %q want %q", got, webtty.WebTTYApplicationProtocol)
		}
	})
	t.Run("private tunnel omits HTTP edge settings", func(t *testing.T) {
		cmd := newTestWebTTYServerCommand()
		if err := cmd.Flags().Set("name", "shell"); err != nil {
			t.Fatalf("failed to set --name: %v", err)
		}
		if err := cmd.Flags().Set("no-publish", "true"); err != nil {
			t.Fatalf("failed to set --no-publish: %v", err)
		}
		props := newWebTTYServerTunnelProperties(cmd)
		if props.Name == nil || *props.Name != "shell" {
			t.Fatalf("unexpected tunnel name: %#v", props.Name)
		}
		if props.Publish == nil || *props.Publish {
			t.Fatalf("expected private tunnel")
		}
		if props.Protocol != nil {
			t.Fatalf("expected protocol to be unset for private tunnel, got %#v", props.Protocol)
		}
		if props.HTTPVersion != nil {
			t.Fatalf("expected HTTP version to be unset for private tunnel, got %#v", props.HTTPVersion)
		}
		if props.TokenAuth != nil {
			t.Fatalf("expected token auth to be unset for private tunnel, got %#v", props.TokenAuth)
		}
	})
}

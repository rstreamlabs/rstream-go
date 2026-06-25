// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestParseNetcatUDPTargets(t *testing.T) {
	target, err := parseNetcatDialTarget("udp://127.0.0.1:5004")
	if err != nil || target.Kind != netcatEndpointUDP || target.Address != "127.0.0.1:5004" {
		t.Fatalf("parseNetcatDialTarget(udp) = %#v err=%v", target, err)
	}
	if target.String() != "udp://127.0.0.1:5004" {
		t.Fatalf("dial target String() = %q", target.String())
	}
	listen, err := parseNetcatListenTarget("udp://127.0.0.1:5004")
	if err != nil || listen.Kind != netcatEndpointUDP || listen.Address != "127.0.0.1:5004" {
		t.Fatalf("parseNetcatListenTarget(udp) = %#v err=%v", listen, err)
	}
	for _, raw := range []string{"udp://", "udp://nohost", "udp://user@h:1", "udp://h:1?x=1", "udp://h:1/path"} {
		if _, err := parseNetcatDialTarget(raw); err == nil {
			t.Fatalf("parseNetcatDialTarget(%q) succeeded unexpectedly", raw)
		}
	}
}

func TestValidateNetcatUDPFlagCombos(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		config  func(*cobra.Command) error
		wantErr bool
	}{
		{
			name: "datagram listen rstream with udp remote",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "rstrm://media"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("remote", "udp://127.0.0.1:5004")
			},
			wantErr: false,
		},
		{
			name: "datagram listen udp with rstream remote",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "udp://127.0.0.1:5004"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("remote", "rstrm://media")
			},
			wantErr: false,
		},
		{
			name: "udp endpoints require datagram",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "udp://127.0.0.1:5004"); err != nil {
					return err
				}
				return cmd.Flags().Set("remote", "rstrm://media")
			},
			wantErr: true,
		},
		{
			name: "udp listen requires rstream remote",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "udp://127.0.0.1:5004"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				return cmd.Flags().Set("sh-exec", "cat")
			},
			wantErr: true,
		},
		{
			name: "datagram rejects tcp remote",
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
			name: "framing rejected with udp endpoints",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "rstrm://media"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("remote", "udp://127.0.0.1:5004"); err != nil {
					return err
				}
				return cmd.Flags().Set("framing", "rfc4571")
			},
			wantErr: true,
		},
		{
			name: "udp-peer requires udp listen",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "rstrm://media"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("remote", "udp://127.0.0.1:5004"); err != nil {
					return err
				}
				return cmd.Flags().Set("udp-peer", "127.0.0.1:5006")
			},
			wantErr: true,
		},
		{
			name: "udp-peer accepted with udp listen",
			config: func(cmd *cobra.Command) error {
				if err := cmd.Flags().Set("listen", "udp://127.0.0.1:5004"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("datagram", "true"); err != nil {
					return err
				}
				if err := cmd.Flags().Set("remote", "rstrm://media"); err != nil {
					return err
				}
				return cmd.Flags().Set("udp-peer", "127.0.0.1:5006")
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

func TestNewNetcatClientConfigRejectsUDPTarget(t *testing.T) {
	command := newTestNetcatCommand()
	if _, err := newNetcatClientConfig(command, slog.Default(), "udp://127.0.0.1:5004"); err == nil || !strings.Contains(err.Error(), "server mode") {
		t.Fatalf("expected udp client target rejection, got %v", err)
	}
}

func TestRunNetcatUDPUpstreamSessionBridgesPackets(t *testing.T) {
	tunnelA := newNetcatTestUDPConn(t)
	tunnelB := newNetcatTestUDPConn(t)
	echo := newNetcatTestUDPConn(t)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, raddr, err := echo.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := echo.WriteTo(buf[:n], raddr); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sessionErrCh := make(chan error, 1)
	go func() {
		sessionErrCh <- runNetcatUDPUpstreamSession(ctx, tunnelA, tunnelB.LocalAddr(), echo.LocalAddr().String(), 0, slog.Default())
	}()
	if err := tunnelB.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, err := tunnelB.WriteTo([]byte("ping"), tunnelA.LocalAddr()); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	buf := make([]byte, 2048)
	n, _, err := tunnelB.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("echoed packet = %q, want ping", buf[:n])
	}
	cancel()
	if err := <-sessionErrCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("session error = %v", err)
	}
}

// echoTunnelConn is an in-memory net.PacketConn standing in for a dialed
// datagram tunnel session that echoes every datagram back.
type echoTunnelConn struct {
	ch     chan []byte
	closed chan struct{}
	once   sync.Once
}

func newEchoTunnelConn() *echoTunnelConn {
	return &echoTunnelConn{ch: make(chan []byte, 16), closed: make(chan struct{})}
}

func (c *echoTunnelConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data := <-c.ch:
		return copy(p, data), netcatTestAddr("tunnel"), nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *echoTunnelConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case c.ch <- append([]byte(nil), p...):
		return len(p), nil
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *echoTunnelConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *echoTunnelConn) LocalAddr() net.Addr                { return netcatTestAddr("tunnel") }
func (c *echoTunnelConn) SetDeadline(time.Time) error        { return nil }
func (c *echoTunnelConn) SetReadDeadline(time.Time) error    { return nil }
func (c *echoTunnelConn) SetWriteDeadline(t time.Time) error { return nil }

func TestRunNetcatUDPBridgeLoopEchoesThroughTunnel(t *testing.T) {
	bridgeSocket := newNetcatTestUDPConn(t)
	client := newNetcatTestUDPConn(t)
	var dials atomic.Int32
	cfg := &netcatServerConfig{
		Datagram:    true,
		OpenTimeout: time.Second,
		PacketDial: func(context.Context) (net.PacketConn, net.Addr, error) {
			dials.Add(1)
			return newEchoTunnelConn(), netcatTestAddr("tunnel"), nil
		},
		Stderr: io.Discard,
		Logger: slog.Default(),
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bridgeErrCh := make(chan error, 1)
	go func() { bridgeErrCh <- runNetcatUDPBridgeLoop(ctx, cfg, bridgeSocket) }()
	if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	for _, payload := range []string{"alpha", "beta"} {
		if _, err := client.WriteTo([]byte(payload), bridgeSocket.LocalAddr()); err != nil {
			t.Fatalf("WriteTo(%q) error = %v", payload, err)
		}
		buf := make([]byte, 2048)
		n, _, err := client.ReadFrom(buf)
		if err != nil {
			t.Fatalf("ReadFrom() after %q error = %v", payload, err)
		}
		if string(buf[:n]) != payload {
			t.Fatalf("bridge echo = %q, want %q", buf[:n], payload)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("expected one tunnel session for one peer, got %d", got)
	}
	cancel()
	if err := <-bridgeErrCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("bridge error = %v", err)
	}
}

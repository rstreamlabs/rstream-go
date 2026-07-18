// See LICENSE file in the project root for license information.

package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

func TestFlagHelpersParseChangedValues(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	command.Flags().StringArray("label", nil, "")
	command.Flags().StringSlice("trusted-ips", nil, "")
	command.Flags().String("name", "", "")
	command.Flags().Bool("enabled", false, "")
	command.Flags().Int64("limit", 0, "")
	mustSetFlag(t, command, "label", "env=prod")
	mustSetFlag(t, command, "label", "ignored")
	mustSetFlag(t, command, "trusted-ips", "10.0.0.0/8,  192.168.0.0/16 ,,")
	mustSetFlag(t, command, "name", "demo")
	mustSetFlag(t, command, "enabled", "true")
	mustSetFlag(t, command, "limit", "42")
	if got := getStringArrayMap(command, "label"); !reflect.DeepEqual(got, map[string]string{"env": "prod"}) {
		t.Fatalf("unexpected labels: %#v", got)
	}
	if got := getStringSlice(command, "trusted-ips"); !reflect.DeepEqual(got, []string{"10.0.0.0/8", "192.168.0.0/16"}) {
		t.Fatalf("unexpected trusted IPs: %#v", got)
	}
	if got := getStringPtr(command, "name"); got == nil || *got != "demo" {
		t.Fatalf("unexpected name pointer: %v", got)
	}
	if got := getBoolPtr(command, "enabled"); got == nil || !*got {
		t.Fatalf("unexpected bool pointer: %v", got)
	}
	if got := getInt64Ptr(command, "limit"); got == nil || *got != 42 {
		t.Fatalf("unexpected int64 pointer: %v", got)
	}
}

func TestNewTunnelPropertiesFromFlags(t *testing.T) {
	command := tunnelFlagsCommand()
	for _, set := range [][2]string{
		{"name", "web"},
		{"http", "true"},
		{"publish", "true"},
		{"host", "web.example.com"},
		{"http-version", "h3"},
		{"upstream-tls", "true"},
		{"token-auth", "true"},
		{"rstream-auth", "false"},
		{"challenge-mode", "true"},
		{"label", "tier=edge"},
		{"geoip", "FR,US"},
		{"trusted-ips", "10.0.0.0/8"},
	} {
		mustSetFlag(t, command, set[0], set[1])
	}
	props, err := newTunnelPropertiesFromFlags(command)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if props.Name == nil || *props.Name != "web" {
		t.Fatalf("unexpected name: %#v", props.Name)
	}
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP {
		t.Fatalf("unexpected protocol: %#v", props.Protocol)
	}
	if props.HTTPVersion == nil || *props.HTTPVersion != rstream.HTTP3 {
		t.Fatalf("unexpected http version: %#v", props.HTTPVersion)
	}
	if props.UpstreamTLS == nil || !*props.UpstreamTLS || props.HTTPUseTLS == nil || !*props.HTTPUseTLS {
		t.Fatalf("expected upstream/http TLS to be enabled")
	}
	if props.TokenAuth == nil || !*props.TokenAuth || props.RstreamAuth == nil || *props.RstreamAuth || props.ChallengeMode == nil || !*props.ChallengeMode {
		t.Fatalf("unexpected HTTP auth/gate flags: %#v", props)
	}
	if !reflect.DeepEqual(props.Labels, map[string]string{"tier": "edge"}) {
		t.Fatalf("unexpected labels: %#v", props.Labels)
	}
	if !reflect.DeepEqual(props.GeoIP, []string{"FR", "US"}) || !reflect.DeepEqual(props.TrustedIPs, []string{"10.0.0.0/8"}) {
		t.Fatalf("unexpected geo/trusted lists: %#v %#v", props.GeoIP, props.TrustedIPs)
	}
}

func TestNewTunnelPropertiesFromFlagsLeavesExposureDefaultsUnset(t *testing.T) {
	props, err := newTunnelPropertiesFromFlags(tunnelFlagsCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if props.Publish != nil || props.Protocol != nil || props.Type != nil || props.Hostname != nil || props.Host != nil {
		t.Fatalf("default forward flags should not force public exposure options: %#v", props)
	}
}

func TestNewTunnelPropertiesFromFlagsRejectsInvalidForwardEnums(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		value   string
		wantErr string
	}{
		{name: "tls mode", flag: "tls-mode", value: "optional", wantErr: "invalid --tls-mode"},
		{name: "http version", flag: "http-version", value: "http/4", wantErr: "invalid --http-version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := tunnelFlagsCommand()
			mustSetFlag(t, command, tt.flag, tt.value)
			_, err := newTunnelPropertiesFromFlags(command)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected %q error, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNewTunnelPropertiesFromFlagsAllowsMultiplePublishedAuthMethods(t *testing.T) {
	command := tunnelFlagsCommand()
	mustSetFlag(t, command, "http", "true")
	mustSetFlag(t, command, "mtls", "true")
	mustSetFlag(t, command, "token-auth", "true")
	mustSetFlag(t, command, "rstream-auth", "true")
	props, err := newTunnelPropertiesFromFlags(command)
	if err != nil {
		t.Fatalf("newTunnelPropertiesFromFlags() error = %v", err)
	}
	if props.MTLSAuth == nil || !*props.MTLSAuth || props.TokenAuth == nil || !*props.TokenAuth || props.RstreamAuth == nil || !*props.RstreamAuth {
		t.Fatalf("expected published auth methods to be preserved, got %#v", props)
	}
}

func TestNewTunnelPropertiesFromFlagsRejectsHTTPAuthWithoutHTTP(t *testing.T) {
	command := tunnelFlagsCommand()
	mustSetFlag(t, command, "tls", "true")
	mustSetFlag(t, command, "token-auth", "true")
	_, err := newTunnelPropertiesFromFlags(command)
	if err == nil || !strings.Contains(err.Error(), "require --http") {
		t.Fatalf("expected HTTP flag validation error, got %v", err)
	}
}

func TestNewTunnelPropertiesFromFlagsMapsDatagramGuaranteedDelivery(t *testing.T) {
	command := tunnelFlagsCommand()
	mustSetFlag(t, command, "datagram", "true")
	mustSetFlag(t, command, "datagram-guaranteed-delivery", "true")
	props, err := newTunnelPropertiesFromFlags(command)
	if err != nil {
		t.Fatalf("newTunnelPropertiesFromFlags() error = %v", err)
	}
	if props.DatagramGuaranteedDelivery == nil || !*props.DatagramGuaranteedDelivery {
		t.Fatalf("DatagramGuaranteedDelivery = %#v, want true", props.DatagramGuaranteedDelivery)
	}
}

func TestNewTunnelPropertiesFromFlagsRejectsDatagramGuaranteedDeliveryForBytestream(t *testing.T) {
	command := tunnelFlagsCommand()
	mustSetFlag(t, command, "bytestream", "true")
	mustSetFlag(t, command, "datagram-guaranteed-delivery", "true")
	_, err := newTunnelPropertiesFromFlags(command)
	if err == nil || !strings.Contains(err.Error(), "requires a datagram tunnel") {
		t.Fatalf("expected datagram tunnel validation error, got %v", err)
	}
}

func tunnelFlagsCommand() *cobra.Command {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("name", "", "")
	command.Flags().Bool("bytestream", false, "")
	command.Flags().Bool("datagram", false, "")
	command.Flags().Bool("publish", false, "")
	command.Flags().Bool("no-publish", false, "")
	command.Flags().Bool("tls", false, "")
	command.Flags().Bool("tcp", false, "")
	command.Flags().Bool("dtls", false, "")
	command.Flags().Bool("quic", false, "")
	command.Flags().Bool("http", false, "")
	command.Flags().Uint32("tcp-port", 0, "")
	command.Flags().StringArray("label", nil, "")
	command.Flags().StringSlice("geoip", nil, "")
	command.Flags().StringSlice("trusted-ips", nil, "")
	command.Flags().String("host", "", "")
	command.Flags().String("tls-mode", "", "")
	command.Flags().String("tls-alpn", "", "")
	command.Flags().String("tls-min-version", "", "")
	command.Flags().StringSlice("tls-ciphers", nil, "")
	command.Flags().Bool("mtls", false, "")
	command.Flags().String("http-version", "", "")
	command.Flags().Bool("http-use-tls", false, "")
	command.Flags().Bool("upstream-tls", false, "")
	command.Flags().Bool("datagram-guaranteed-delivery", false, "")
	command.Flags().Bool("token-auth", false, "")
	command.Flags().Bool("rstream-auth", false, "")
	command.Flags().Bool("challenge-mode", false, "")
	return command
}

func TestNewTunnelPropertiesFromFlagsPublishedTCP(t *testing.T) {
	command := tunnelFlagsCommand()
	mustSetFlag(t, command, "tcp", "true")
	mustSetFlag(t, command, "tcp-port", "10042")
	props, err := newTunnelPropertiesFromFlags(command)
	if err != nil {
		t.Fatalf("newTunnelPropertiesFromFlags() error = %v", err)
	}
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolTCP || props.Type == nil || *props.Type != rstream.TunnelTypeBytestream {
		t.Fatalf("unexpected TCP properties: %#v", props)
	}
	if props.Publish == nil || !*props.Publish || props.Port == nil || *props.Port != 10042 {
		t.Fatalf("unexpected TCP publication properties: %#v", props)
	}
}

func TestNewTunnelPropertiesFromFlagsRejectsInvalidPublishedTCP(t *testing.T) {
	tests := []struct {
		name    string
		flags   [][2]string
		wantErr string
	}{
		{name: "port without protocol", flags: [][2]string{{"tcp-port", "10042"}}, wantErr: "requires --tcp"},
		{name: "zero port", flags: [][2]string{{"tcp", "true"}, {"tcp-port", "0"}}, wantErr: "between 1 and 65535"},
		{name: "datagram", flags: [][2]string{{"tcp", "true"}, {"datagram", "true"}}, wantErr: "requires a bytestream"},
		{name: "private", flags: [][2]string{{"tcp", "true"}, {"no-publish", "true"}}, wantErr: "cannot be used with --no-publish"},
		{name: "hostname", flags: [][2]string{{"tcp", "true"}, {"host", "ssh.example.com"}}, wantErr: "--host cannot be used with --tcp"},
		{name: "TLS option", flags: [][2]string{{"tcp", "true"}, {"upstream-tls", "true"}}, wantErr: "--upstream-tls cannot be used with --tcp"},
		{name: "edge authentication", flags: [][2]string{{"tcp", "true"}, {"mtls", "true"}}, wantErr: "--mtls cannot be used with --tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := tunnelFlagsCommand()
			for _, flag := range tt.flags {
				mustSetFlag(t, command, flag[0], flag[1])
			}
			_, err := newTunnelPropertiesFromFlags(command)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected %q error, got %v", tt.wantErr, err)
			}
		})
	}
}

func mustSetFlag(t *testing.T, command *cobra.Command, name, value string) {
	t.Helper()
	if err := command.Flags().Set(name, value); err != nil {
		t.Fatalf("set %s=%s: %v", name, value, err)
	}
}

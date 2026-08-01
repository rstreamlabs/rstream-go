// See LICENSE file in the project root for license information.

package main

import "testing"

func TestSameAuthority(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "same host", left: "proxy.example.com", right: "proxy.example.com", want: true},
		{name: "implicit HTTPS port", left: "proxy.example.com:443", right: "proxy.example.com", want: true},
		{name: "case insensitive host", left: "PROXY.example.com", right: "proxy.EXAMPLE.com", want: true},
		{name: "different host", left: "other.example.com", right: "proxy.example.com", want: false},
		{name: "different port", left: "proxy.example.com:8443", right: "proxy.example.com", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameAuthority(tt.left, tt.right); got != tt.want {
				t.Fatalf("sameAuthority() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestForwardingAuthority(t *testing.T) {
	tests := []struct {
		name       string
		forwarding string
		publicPort string
		want       string
		wantErr    bool
	}{
		{name: "owner authority", forwarding: "https://proxy.example.com:8443", want: "proxy.example.com:8443"},
		{name: "public port override", forwarding: "https://proxy.example.com:8443", publicPort: "443", want: "proxy.example.com:443"},
		{name: "IPv6 public port override", forwarding: "https://[2001:db8::1]:8443", publicPort: "443", want: "[2001:db8::1]:443"},
		{name: "invalid forwarding address", forwarding: "proxy.example.com", wantErr: true},
		{name: "invalid public port", forwarding: "https://proxy.example.com", publicPort: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := forwardingAuthority(tt.forwarding, tt.publicPort)
			if tt.wantErr {
				if err == nil {
					t.Fatal("forwardingAuthority() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("forwardingAuthority() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("forwardingAuthority() = %q, want %q", got, tt.want)
			}
		})
	}
}

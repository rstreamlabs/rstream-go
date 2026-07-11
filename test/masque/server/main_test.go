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

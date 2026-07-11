// See LICENSE file in the project root for license information.

package main

import (
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "no origin", host: "viewer.example.com", want: true},
		{name: "matching origin", host: "viewer.example.com", origin: "https://viewer.example.com", want: true},
		{name: "matching origin case insensitive", host: "VIEWER.example.com", origin: "https://viewer.EXAMPLE.com", want: true},
		{name: "different origin", host: "viewer.example.com", origin: "https://other.example.com", want: false},
		{name: "invalid origin", host: "viewer.example.com", origin: "://invalid", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("CONNECT", "https://"+tt.host+"/ws", nil)
			req.Host = tt.host
			req.Header.Set("Origin", tt.origin)
			if got := sameOrigin(req); got != tt.want {
				t.Fatalf("sameOrigin() = %t, want %t", got, tt.want)
			}
		})
	}
}

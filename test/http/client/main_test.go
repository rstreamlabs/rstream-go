// See LICENSE file in the project root for license information.

package main

import "testing"

func TestHostPortFromPublishedHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "bare host", host: "abc.t.c.localhost.rstream.io", want: "abc.t.c.localhost.rstream.io:443"},
		{name: "host with port", host: "abc.t.c.localhost.rstream.io:9443", want: "abc.t.c.localhost.rstream.io:9443"},
		{name: "url with port", host: "https://abc.t.c.localhost.rstream.io:9443", want: "abc.t.c.localhost.rstream.io:9443"},
		{name: "url without port", host: "https://abc.t.c.localhost.rstream.io", want: "abc.t.c.localhost.rstream.io:443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hostPortFromPublishedHost(tt.host)
			if err != nil {
				t.Fatalf("hostPortFromPublishedHost() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("hostPortFromPublishedHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

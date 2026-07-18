// See LICENSE file in the project root for license information.

package main

import "testing"

func TestNormalizeAddress(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "host and port", value: "tcp.example.test:10042", want: "tcp.example.test:10042"},
		{name: "SDK display address", value: "tcp.example.test:10042 (tcp)", want: "tcp.example.test:10042"},
		{name: "TCP URL", value: "tcp://tcp.example.test:10042", want: "tcp.example.test:10042"},
		{name: "IPv6 URL", value: "tcp://[2001:db8::1]:10042", want: "[2001:db8::1]:10042"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeAddress(test.value)
			if err != nil {
				t.Fatalf("normalizeAddress() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("normalizeAddress() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeAddressRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "tcp.example.test", "tcp.example.test:ssh", "tcp.example.test:0", "https://tcp.example.test:10042", "tcp://user@tcp.example.test:10042", "tcp://tcp.example.test:10042/path"} {
		t.Run(value, func(t *testing.T) {
			if _, err := normalizeAddress(value); err == nil {
				t.Fatalf("normalizeAddress(%q) returned no error", value)
			}
		})
	}
}

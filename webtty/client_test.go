// See LICENSE file in the project root for license information.

package webtty

import (
	"testing"
)

func TestNormalizeWebTTYURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default scheme", input: "127.0.0.1:8080", want: "ws://127.0.0.1:8080/"},
		{name: "ws scheme", input: "ws://localhost:8080/path", want: "ws://localhost:8080/path"},
		{name: "wss scheme", input: "wss://example.com", want: "wss://example.com/"},
		{name: "invalid scheme", input: "http://localhost", wantErr: true},
		{name: "missing host", input: "ws:///path", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeWebTTYURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeWebTTYURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected normalized url: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestParseClientEnvVars(t *testing.T) {
	t.Setenv("RSTREAM_TEST_ENV", "from-env")
	values, err := parseClientEnvVars([]string{"A=1", "RSTREAM_TEST_ENV", "EMPTY="})
	if err != nil {
		t.Fatalf("parseClientEnvVars returned error: %v", err)
	}
	if len(values) != 3 {
		t.Fatalf("unexpected env var count: got %d want 3", len(values))
	}
	if values[0].Key != "A" || values[0].Value != "1" {
		t.Fatalf("unexpected first env var: %#v", values[0])
	}
	if values[1].Key != "RSTREAM_TEST_ENV" || values[1].Value != "from-env" {
		t.Fatalf("unexpected second env var: %#v", values[1])
	}
	if values[2].Key != "EMPTY" || values[2].Value != "" {
		t.Fatalf("unexpected third env var: %#v", values[2])
	}
}

func TestParseClientUsername(t *testing.T) {
	idValue := "42"
	id, err := parseClientUsername(&idValue)
	if err != nil {
		t.Fatalf("parseClientUsername(id) returned error: %v", err)
	}
	if id.GetId() != 42 {
		t.Fatalf("unexpected numeric id: got %d want 42", id.GetId())
	}
	nameValue := "alice"
	name, err := parseClientUsername(&nameValue)
	if err != nil {
		t.Fatalf("parseClientUsername(name) returned error: %v", err)
	}
	if name.GetName() != "alice" {
		t.Fatalf("unexpected username: got %q want %q", name.GetName(), "alice")
	}
}

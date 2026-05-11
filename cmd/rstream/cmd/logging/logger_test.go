// See LICENSE file in the project root for license information.

package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLoggerFormatsAndLevels(t *testing.T) {
	for _, format := range []string{"auto", "json", "json-pretty", "text"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			logger, err := New(Config{Level: "debug", Format: format, Output: &buf})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			logger.Debug("message", "key", "value")
			if got := buf.String(); !strings.Contains(got, "message") || !strings.Contains(got, "value") {
				t.Fatalf("log output missing fields: %q", got)
			}
		})
	}
	if _, err := New(Config{Level: "bad", Format: "text", Output: &bytes.Buffer{}}); err == nil {
		t.Fatalf("expected invalid level error")
	}
	if _, err := New(Config{Level: "info", Format: "xml", Output: &bytes.Buffer{}}); err == nil {
		t.Fatalf("expected invalid format error")
	}
}

func TestParseLevelAliases(t *testing.T) {
	for _, level := range []string{"", "info", "debug", "warn", "warning", "error"} {
		if _, err := parseLevel(level); err != nil {
			t.Fatalf("parseLevel(%q) error = %v", level, err)
		}
	}
	if _, err := parseLevel("trace"); err == nil {
		t.Fatalf("expected invalid level error")
	}
}

func TestPrettyJSONWriterFormatsObjectsAndNormalizesFallbacks(t *testing.T) {
	var buf bytes.Buffer
	writer := newPrettyJSONWriter(&buf)
	n, err := writer.Write([]byte(`{"msg":"ok","n":1}`))
	if err != nil || n == 0 {
		t.Fatalf("Write(json) = %d, %v", n, err)
	}
	if got := buf.String(); !strings.Contains(got, "\n  \"msg\": \"ok\"") {
		t.Fatalf("JSON was not pretty printed: %q", got)
	}
	buf.Reset()
	n, err = writer.Write([]byte("plain"))
	if err != nil || n != len("plain") {
		t.Fatalf("Write(plain) = %d, %v", n, err)
	}
	if got := buf.String(); got != "plain\n" {
		t.Fatalf("fallback output = %q, want plain newline", got)
	}
}

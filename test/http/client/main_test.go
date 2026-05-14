// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestSSEURLFromPingURL(t *testing.T) {
	got := sseURLFromPingURL("https://example.com/ping")
	if got != "https://example.com/events" {
		t.Fatalf("sseURLFromPingURL() = %q", got)
	}
}

func TestReadExpectedSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 1; i <= sseEventCount; i++ {
			fmt.Fprintf(w, "id: %d\nevent: tick\ndata: event-%d\n\n", i, i)
		}
	}))
	defer server.Close()
	if err := readExpectedSSE(context.Background(), server.Client(), server.URL); err != nil {
		t.Fatalf("readExpectedSSE() error = %v", err)
	}
}

func TestReadExpectedSSERejectsWrongContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "data: event-1")
	}))
	defer server.Close()
	err := readExpectedSSE(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("readExpectedSSE() error = %v", err)
	}
}

// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"
)

func TestRunNetcatClientReturnsWhenRemoteCloses(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer stdinReader.Close()
	defer stdinWriter.Close()
	var stdout bytes.Buffer
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runNetcatClient(context.Background(), &netcatClientConfig{
			Target:      "pipe",
			Interactive: true,
			Dial: func(context.Context) (net.Conn, error) {
				return client, nil
			},
			Stdin:  stdinReader,
			Stdout: &stdout,
			Logger: slog.Default(),
		})
	}()
	if _, err := server.Write([]byte("hello")); err != nil {
		t.Fatalf("failed to write server payload: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("failed to close server side: %v", err)
	}
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("client did not exit after remote close")
	}
	if got := stdout.String(); got != "hello" {
		t.Fatalf("unexpected stdout: got %q want %q", got, "hello")
	}
}

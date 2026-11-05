// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebDAVEarlyUploadRefusal(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		for _, protocol := range []string{"h1", "h2"} {
			for _, length := range []string{"known", "chunked"} {
				for _, failure := range []string{"read-only", "unavailable"} {
					t.Run(network+"/"+protocol+"/"+length+"/"+failure, func(t *testing.T) {
						testWebDAVEarlyUploadRefusal(t, network, protocol, length, failure)
					})
				}
			}
		}
	}
}

func testWebDAVEarlyUploadRefusal(t *testing.T, network, protocol, length, failure string) {
	t.Helper()
	local, err := Open(t.TempDir(), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWebDAV(local, WebDAVConfig{ReadOnly: failure == "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusForbidden
	if failure == "unavailable" {
		if err := local.Close(); err != nil {
			t.Fatal(err)
		}
		status = http.StatusServiceUnavailable
	} else {
		defer local.Close()
	}
	server := httptest.NewUnstartedServer(handler)
	address := "127.0.0.1:0"
	if network == "tcp6" {
		address = "[::1]:0"
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.EnableHTTP2 = protocol == "h2"
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = reader.CloseWithError(ctx.Err())
		_ = writer.CloseWithError(ctx.Err())
	})
	defer stop()
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = writer.Write([]byte("unfinished upload"))
	}()
	t.Cleanup(func() { <-written })
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+"/fs/target", reader)
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = -1
	if length == "known" {
		request.ContentLength = 64 << 20
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("refusal waited for the unfinished upload: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status || len(body) == 0 {
		t.Fatalf("refusal lost: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	if (response.ProtoMajor == 2) != (protocol == "h2") {
		t.Fatalf("unexpected response protocol: %s", response.Proto)
	}
}

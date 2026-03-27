// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWSConnReadTimesOutWithoutPong(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-r.Context().Done()
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(testWebsocketURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	watch := newWSConn(conn, 10*time.Millisecond, 50*time.Millisecond)
	defer watch.Close()
	start := time.Now()
	_, err = watch.Read(context.Background())
	if err == nil {
		t.Fatalf("Read() error = nil, want timeout")
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("Read() elapsed = %s, want <= 300ms", elapsed)
	}
}

func TestWSConnReadStaysAliveWithPong(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		time.AfterFunc(120*time.Millisecond, func() {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"state.initial","object":{}}`))
		})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(testWebsocketURL(server.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	watch := newWSConn(conn, 20*time.Millisecond, 80*time.Millisecond)
	defer watch.Close()
	data, err := watch.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(data) != `{"type":"state.initial","object":{}}` {
		t.Fatalf("Read() = %q, want event payload", data)
	}
}

func testWebsocketURL(raw string) string {
	return "ws" + strings.TrimPrefix(raw, "http")
}

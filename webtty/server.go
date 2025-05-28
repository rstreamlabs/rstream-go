// See LICENSE file in the project root for license information.

package webtty

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

type ServerConfig struct {
	MaxMessageSize  int64
	ReadBufferSize  int
	WriteBufferSize int
}

func NewWebTTYHandler(cfg ServerConfig) http.Handler {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  cfg.ReadBufferSize,
		WriteBufferSize: cfg.WriteBufferSize,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("failed to upgrade connection: %v", err)
		} else {
			s := newSession(conn, cfg)
			go s.run()
		}
	})
}

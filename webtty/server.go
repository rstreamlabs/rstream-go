// See LICENSE file in the project root for license information.

package webtty

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

type ServerConfig struct {
	MaxMessageSize  *int64
	ReadBufferSize  *int
	WriteBufferSize *int
	EnvVars         *map[string]string
}

func NewWebTTYHandler(cfg *ServerConfig) http.Handler {
	if cfg == nil {
		cfg = &ServerConfig{}
	}
	if cfg.MaxMessageSize == nil {
		defaultSize := int64(1024 * 1024) // 1 MB
		cfg.MaxMessageSize = &defaultSize
	}
	if cfg.ReadBufferSize == nil {
		defaultReadSize := 1024 // 1 KB
		cfg.ReadBufferSize = &defaultReadSize
	}
	if cfg.WriteBufferSize == nil {
		defaultWriteSize := 1024 // 1 KB
		cfg.WriteBufferSize = &defaultWriteSize
	}
	if cfg.EnvVars == nil {
		defaultEnvVars := make(map[string]string)
		cfg.EnvVars = &defaultEnvVars
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  *cfg.ReadBufferSize,
		WriteBufferSize: *cfg.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
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

// See LICENSE file in the project root for license information.

package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type response struct {
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Host       string              `json:"host"`
	Proto      string              `json:"proto"`
	RemoteAddr string              `json:"remote_addr"`
	ClientIP   string              `json:"client_ip"`
	Headers    map[string][]string `json:"headers"`
	Time       string              `json:"time"`
}

func main() {
	listen := flag.String("listen", envOrDefault("WHOAMI_LISTEN", ":8080"), "listen address")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		clientIP := clientIP(r)
		resp := response{
			Method:     r.Method,
			Path:       r.URL.RequestURI(),
			Host:       r.Host,
			Proto:      r.Proto,
			RemoteAddr: r.RemoteAddr,
			ClientIP:   clientIP,
			Headers:    r.Header,
			Time:       time.Now().UTC().Format(time.RFC3339),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("whoami listening", "addr", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

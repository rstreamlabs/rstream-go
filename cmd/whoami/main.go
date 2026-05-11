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
	Headers    map[string][]string `json:"headers,omitempty"`
	Time       string              `json:"time"`
}

func main() {
	listen := flag.String("listen", envOrDefault("WHOAMI_LISTEN", ":8080"), "listen address")
	includeHeaders := flag.Bool("include-headers", envBoolOrDefault("WHOAMI_INCLUDE_HEADERS", false), "include redacted request headers in responses")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server := &http.Server{
		Addr:              *listen,
		Handler:           whoamiHandler(*includeHeaders),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("whoami listening", "addr", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func whoamiHandler(includeHeaders bool) http.Handler {
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
			Time:       time.Now().UTC().Format(time.RFC3339),
		}
		if includeHeaders {
			resp.Headers = safeHeaders(r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}

func envOrDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

func envBoolOrDefault(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

func safeHeaders(headers http.Header) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if sensitiveRequestHeader(key) {
			out[key] = []string{"<redacted>"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func sensitiveRequestHeader(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "x-auth-key", "x-auth-token", "x-csrf-token":
		return true
	default:
		return strings.Contains(key, "auth") ||
			strings.Contains(key, "credential") ||
			strings.Contains(key, "email") ||
			strings.Contains(key, "jwt") ||
			strings.Contains(key, "key") ||
			strings.Contains(key, "oidc") ||
			strings.Contains(key, "password") ||
			strings.Contains(key, "saml") ||
			strings.Contains(key, "secret") ||
			strings.Contains(key, "session") ||
			strings.Contains(key, "token") ||
			strings.Contains(key, "user")
	}
}

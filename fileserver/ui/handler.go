// See LICENSE file in the project root for license information.

// Package ui serves the generated, self-contained Next.js file browser.
package ui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/index.html assets/index.html.gz assets/manifest.json
var assets embed.FS

type manifest struct {
	Version      int    `json:"version"`
	Contract     int    `json:"contractVersion"`
	HTMLSHA256   string `json:"htmlSHA256"`
	ScriptSHA256 string `json:"scriptSHA256"`
	StyleSHA256  string `json:"styleSHA256"`
}

func Handler() http.Handler {
	html, err := assets.ReadFile("assets/index.html")
	if err != nil {
		panic(err)
	}
	compressed, err := assets.ReadFile("assets/index.html.gz")
	if err != nil {
		panic(err)
	}
	raw, err := assets.ReadFile("assets/manifest.json")
	if err != nil {
		panic(err)
	}
	var meta manifest
	if err := json.Unmarshal(raw, &meta); err != nil {
		panic(err)
	}
	digest := sha256.Sum256(html)
	if meta.Version != 1 || meta.Contract != 1 || hex.EncodeToString(digest[:]) != meta.HTMLSHA256 {
		panic("file browser assets do not match their manifest")
	}
	csp := fmt.Sprintf("default-src 'none'; script-src 'sha256-%s'; style-src 'sha256-%s'; font-src data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'", meta.ScriptSHA256, meta.StyleSHA256)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("Vary", "Accept-Encoding")
		body := html
		etag := meta.HTMLSHA256
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body = compressed
			etag += "-gzip"
			w.Header().Set("Content-Encoding", "gzip")
		}
		w.Header().Set("ETag", strconv.Quote(etag))
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(body))
	})
}

func acceptsGzip(header string) bool {
	wildcard := false
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		coding := strings.TrimSpace(strings.ToLower(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && key == "q" {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if coding == "gzip" {
			return quality > 0
		}
		if coding == "*" {
			wildcard = quality > 0
		}
	}
	return wildcard
}

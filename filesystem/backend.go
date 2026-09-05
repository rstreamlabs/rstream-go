// See LICENSE file in the project root for license information.

package filesystem

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
)

const BackendWebDAV = "webdav"
const BackendWebRTC = "webrtc"

type BackendConfig struct {
	Backend     string
	WebDAV      WebDAVConfig
	RTC         rtc.ServerConfig
	ArchivePath string
	Archive     http.Handler
}

type BackendHandler struct {
	handler http.Handler
	rtc     *rtc.Server
}

func ResolveBackend(value string) (string, error) {
	if value == "" {
		return BackendWebDAV, nil
	}
	if value != BackendWebDAV && value != BackendWebRTC {
		return "", fmt.Errorf("invalid filesystem backend %q (valid: webdav, webrtc)", value)
	}
	return value, nil
}

// NewBackend shares filesystem semantics and policy between HTTP and WebRTC.
// Close the handler before closing the Local that owns the underlying root.
func NewBackend(local *Local, config BackendConfig) (*BackendHandler, error) {
	if config.WebDAV.Prefix == "" {
		config.WebDAV.Prefix = "/fs"
	}
	backend, err := ResolveBackend(config.Backend)
	if err != nil {
		return nil, err
	}
	if backend == BackendWebRTC {
		config.WebDAV.ReadOnly = true
		config.WebDAV.BoundedDepth = true
	}
	dav, err := NewWebDAV(local, config.WebDAV)
	if err != nil {
		return nil, err
	}
	root := strings.TrimRight(config.WebDAV.Prefix, "/")
	data := http.NewServeMux()
	data.Handle(root, dav)
	data.Handle(root+"/", dav)
	if config.Archive != nil {
		data.Handle(config.ArchivePath, config.Archive)
	}
	result := &BackendHandler{}
	mux := http.NewServeMux()
	mux.Handle("/", data)
	endpoint := root + rtc.Endpoint
	if backend == BackendWebRTC {
		config.RTC.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rtc.ReadOnly(r.Method) {
				http.Error(w, "WebRTC filesystem is read-only; writing is not supported", http.StatusForbidden)
				return
			}
			data.ServeHTTP(w, r)
		})
		result.rtc = rtc.NewServer(config.RTC)
		mux.Handle(endpoint, result.rtc)
		result.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != endpoint && !rtc.ReadOnly(r.Method) {
				http.Error(w, "WebRTC filesystem is read-only; writing is not supported", http.StatusForbidden)
				return
			}
			mux.ServeHTTP(w, r)
		})
	} else {
		mux.HandleFunc("GET "+endpoint, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if err := json.NewEncoder(w).Encode(rtc.Info{Version: 1, Backend: BackendWebDAV}); err != nil && config.WebDAV.Logger != nil {
				config.WebDAV.Logger.Debug("write filesystem backend", "error", err)
			}
		})
		result.handler = mux
	}
	return result, nil
}

func (h *BackendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

func (h *BackendHandler) Close() error {
	if h.rtc != nil {
		return h.rtc.Close()
	}
	return nil
}

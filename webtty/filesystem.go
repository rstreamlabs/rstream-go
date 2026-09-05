// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/rstreamlabs/rstream-go/filesystem"
	"github.com/rstreamlabs/rstream-go/filesystem/rtc"
)

type FileSystemConfig struct {
	Backend       string
	RTC           rtc.ServerConfig
	Root          string
	ReadOnly      bool
	MaxUploadSize *int64
	Logger        *slog.Logger
}

// NewFileSystemHandler preserves the WebTTY /fs endpoint and write defaults.
// The returned handler implements io.Closer; close it after serving has stopped.
func NewFileSystemHandler(cfg *FileSystemConfig) (http.Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("filesystem config is required")
	}
	backend, err := filesystem.ResolveBackend(cfg.Backend)
	if err != nil {
		return nil, err
	}
	policy := filesystem.Policy{ReadOnly: cfg.ReadOnly || backend == filesystem.BackendWebRTC}
	if backend == filesystem.BackendWebRTC {
		policy.MaxEntries = 10000
	}
	local, err := filesystem.Open(cfg.Root, policy)
	if err != nil {
		return nil, err
	}
	maxUpload := int64(0)
	if cfg.MaxUploadSize != nil {
		maxUpload = *cfg.MaxUploadSize
	}
	handler, err := filesystem.NewBackend(local, filesystem.BackendConfig{Backend: backend, RTC: cfg.RTC, WebDAV: filesystem.WebDAVConfig{Prefix: WebTTYDefaultFSPath, ReadOnly: policy.ReadOnly, MaxUploadSize: maxUpload, Logger: cfg.Logger}})
	if err != nil {
		_ = local.Close()
		return nil, err
	}
	return &fileSystemHandler{handler: handler, backend: handler, local: local}, nil
}

type fileSystemHandler struct {
	backend   *filesystem.BackendHandler
	handler   http.Handler
	local     *filesystem.Local
	gate      sync.Mutex
	active    sync.WaitGroup
	closing   bool
	closeOnce sync.Once
	closeErr  error
}

func (h *fileSystemHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.gate.Lock()
	if h.closing {
		h.gate.Unlock()
		http.Error(w, "Filesystem is stopping", http.StatusServiceUnavailable)
		return
	}
	h.active.Add(1)
	h.gate.Unlock()
	defer h.active.Done()
	h.handler.ServeHTTP(w, r)
}

func (h *fileSystemHandler) Close() error {
	h.closeOnce.Do(func() {
		h.gate.Lock()
		h.closing = true
		h.gate.Unlock()
		_ = h.backend.Close()
		h.active.Wait()
		h.closeErr = h.local.Close()
	})
	return h.closeErr
}

// See LICENSE file in the project root for license information.

package webtty

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/rstreamlabs/rstream-go/filesystem"
)

type FileSystemConfig struct {
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
	local, err := filesystem.Open(cfg.Root, filesystem.Policy{ReadOnly: cfg.ReadOnly})
	if err != nil {
		return nil, err
	}
	maxUpload := int64(0)
	if cfg.MaxUploadSize != nil {
		maxUpload = *cfg.MaxUploadSize
	}
	handler, err := filesystem.NewWebDAV(local, filesystem.WebDAVConfig{Prefix: WebTTYDefaultFSPath, ReadOnly: cfg.ReadOnly, MaxUploadSize: maxUpload, Logger: cfg.Logger})
	if err != nil {
		_ = local.Close()
		return nil, err
	}
	return &fileSystemHandler{handler: handler, local: local}, nil
}

type fileSystemHandler struct {
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
		h.active.Wait()
		h.closeErr = h.local.Close()
	})
	return h.closeErr
}

// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// The admission token belongs to the exported namespace, not an individual URL:
// aliases and ancestor MOVE/DELETE requests must not race a conditional mutation.
// It is context-cancellable and shared by all WebDAV handlers using this Local.
func (f *Local) serveMutation(w http.ResponseWriter, r *http.Request, next http.Handler, prefix string) {
	if f.policy.ReadOnly {
		http.Error(w, "Filesystem is read-only", http.StatusForbidden)
		return
	}
	select {
	case f.mutations <- struct{}{}:
		defer func() { <-f.mutations }()
	case <-r.Context().Done():
		http.Error(w, "Filesystem request cancelled", http.StatusRequestTimeout)
		return
	}
	release, err := f.lockMutation(r.Context())
	if err != nil {
		status := http.StatusServiceUnavailable
		if r.Context().Err() != nil {
			status = http.StatusRequestTimeout
		}
		http.Error(w, "Filesystem mutation admission unavailable", status)
		return
	}
	defer release()
	if !strings.HasPrefix(r.URL.Path, prefix+"/") && r.URL.Path != prefix {
		http.NotFound(w, r)
		return
	}
	if status := f.checkConditions(r.Context(), strings.TrimPrefix(r.URL.Path, prefix), r.Header); status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if r.Method == http.MethodPut {
		body, err := newUploadBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = body
		r = r.WithContext(context.WithValue(r.Context(), uploadKey{}, body))
		w = &uploadResponse{ResponseWriter: w, body: body}
	}
	next.ServeHTTP(w, r)
}

type uploadResponse struct {
	http.ResponseWriter
	body *uploadBody
}

func (w *uploadResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *uploadResponse) WriteHeader(status int) {
	if w.body.status != 0 {
		status = w.body.status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *uploadResponse) Write(p []byte) (int, error) {
	if w.body.status != 0 {
		_, err := io.WriteString(w.ResponseWriter, http.StatusText(w.body.status)+"\n")
		return len(p), err
	}
	return w.ResponseWriter.Write(p)
}

func (f *Local) checkConditions(ctx context.Context, name string, headers http.Header) int {
	match, none := headers.Get("If-Match"), headers.Get("If-None-Match")
	if match == "" && none == "" {
		return 0
	}
	info, err := f.Stat(ctx, name)
	if err != nil && !os.IsNotExist(err) {
		return http.StatusForbidden
	}
	exists, revision := err == nil, ""
	if exists {
		revision, err = info.(revisionInfo).ETag(ctx)
		if err != nil {
			return http.StatusServiceUnavailable
		}
	}
	if match != "" {
		matched, valid := matchRevision(match, revision, exists, false)
		if !valid {
			return http.StatusBadRequest
		}
		if !matched {
			return http.StatusPreconditionFailed
		}
	}
	if none != "" {
		matched, valid := matchRevision(none, revision, exists, true)
		if !valid {
			return http.StatusBadRequest
		}
		if matched {
			return http.StatusPreconditionFailed
		}
	}
	return 0
}

// This matches the validator emitted by the existing WebDAV implementation.
func fileRevision(info os.FileInfo) string {
	return fmt.Sprintf(`"%x%x"`, info.ModTime().UnixNano(), info.Size())
}

func matchRevision(value, revision string, exists, weakAllowed bool) (bool, bool) {
	value = strings.TrimSpace(value)
	if value == "*" {
		return exists, true
	}
	matched := false
	for value != "" {
		weak := strings.HasPrefix(value, "W/")
		if weak {
			value = value[2:]
		}
		if len(value) < 2 || value[0] != '"' {
			return false, false
		}
		end := strings.IndexByte(value[1:], '"') + 1
		if end == 0 {
			return false, false
		}
		for _, c := range []byte(value[1:end]) {
			if c < 0x21 || c == 0x7f {
				return false, false
			}
		}
		matched = matched || exists && (!weak || weakAllowed) && value[:end+1] == revision
		value = strings.TrimSpace(value[end+1:])
		if value == "" {
			return matched, true
		}
		if value[0] != ',' {
			return false, false
		}
		value = strings.TrimSpace(value[1:])
		if value == "" {
			return false, false
		}
	}
	return false, false
}

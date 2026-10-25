// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// The admission token belongs to the exported namespace, not an individual URL:
// aliases and ancestor MOVE/DELETE requests must not race a conditional mutation.
// It is context-cancellable and shared by all WebDAV handlers using this Local.
func (f *Local) serveMutation(w http.ResponseWriter, r *http.Request, next http.Handler, prefix string, logger *slog.Logger) {
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
	if err := f.recoverReceipt(r.Context()); err != nil {
		http.Error(w, "Filesystem recovery unavailable", http.StatusServiceUnavailable)
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") && r.URL.Path != prefix {
		http.NotFound(w, r)
		return
	}
	var body *uploadBody
	var receipt *receiptRecord
	if r.Method == http.MethodPut {
		body, err = newUploadBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Header.Get("Upload-Id") != "" {
			receipt, err = f.newReceipt(r, strings.TrimPrefix(r.URL.Path, prefix), body)
			if err != nil {
				http.Error(w, "Invalid durable upload request", http.StatusBadRequest)
				return
			}
			prior, err := f.readReceiptFile(receiptName(receipt.ID))
			if err == nil {
				if _, err := f.lexical(prior.Target); err != nil {
					http.NotFound(w, r)
					return
				}
				if prior.Fingerprint != receipt.Fingerprint {
					http.Error(w, "Upload identity has different parameters", http.StatusConflict)
					return
				}
				writeUploadReceipt(w, prior)
				return
			}
			if !os.IsNotExist(err) {
				http.Error(w, "Upload receipt unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := f.beginReceipt(receipt); err != nil {
				logger.Warn("upload journal admission failed", "error", err)
				status, message := journalError(err)
				http.Error(w, message, status)
				return
			}
			body.receipt = receipt
		}
		r.Body = body
		r = r.WithContext(context.WithValue(r.Context(), uploadKey{}, body))
	}
	if status := f.checkConditions(r.Context(), strings.TrimPrefix(r.URL.Path, prefix), r.Header); status != 0 {
		if receipt != nil {
			if err := f.completeReceipt(receipt, "rejected", status); err != nil {
				http.Error(w, "Upload receipt unavailable", http.StatusServiceUnavailable)
				return
			}
			writeUploadReceipt(w, receipt)
		} else {
			http.Error(w, http.StatusText(status), status)
		}
		return
	}
	output := w
	var captured *receiptResponse
	if receipt != nil {
		captured = &receiptResponse{ResponseWriter: w}
		output = captured
	}
	if body != nil {
		output = &uploadResponse{ResponseWriter: output, body: body}
	}
	next.ServeHTTP(output, r)
	if receipt != nil {
		if body.effectCommitted && body.status != 0 {
			http.Error(w, "Upload result uncertain; retrieve its receipt", http.StatusServiceUnavailable)
			return
		}
		if !body.effectCommitted {
			status := captured.status
			if status == 0 {
				status = http.StatusInternalServerError
			}
			if err := f.completeReceipt(receipt, "rejected", status); err != nil {
				http.Error(w, "Upload receipt unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		writeUploadReceipt(w, receipt)
	}
}

// Only used for PUT's small response. The durable receipt is published before
// an HTTP success can escape; no response body from WebDAV is retained.
type receiptResponse struct {
	http.ResponseWriter
	status int
}

func (w *receiptResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *receiptResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return len(p), nil
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
	return checkRevisionConditions(err == nil, match, none, func() (string, error) {
		return info.(revisionInfo).ETag(ctx)
	})
}

func checkRevisionConditions(exists bool, match, none string, loadRevision func() (string, error)) int {
	needsRevision := false
	for _, condition := range []string{match, none} {
		if condition == "" {
			continue
		}
		if _, valid := matchRevision(condition, "", exists, true); !valid {
			return http.StatusBadRequest
		}
		needsRevision = needsRevision || strings.TrimSpace(condition) != "*"
	}
	// Existence conditions need only Stat. Hash content once, and only when
	// a valid condition actually compares an entity tag.
	revision := ""
	if exists && needsRevision {
		var err error
		revision, err = loadRevision()
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

// Directories retain the WebDAV metadata validator. Regular files use SHA-256.
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

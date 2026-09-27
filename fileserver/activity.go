// See LICENSE file in the project root for license information.

package fileserver

import (
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// Activity describes one bounded, content-free filesystem operation.
type Activity struct {
	Date      time.Time
	Backend   string
	Operation string
	Method    string
	Path      string
	Status    int
	Bytes     int64
	Duration  time.Duration
	Outcome   string
}

type activityResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *activityResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *activityResponseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(value)
	w.bytes += int64(n)
	return n, err
}

func (w *activityResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if target, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := target.ReadFrom(reader)
		w.bytes += n
		return n, err
	}
	return io.Copy(struct{ io.Writer }{w}, reader)
}

func (w *activityResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func observeActivity(backend string, observer func(Activity), next http.Handler) http.Handler {
	if observer == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		observed := &activityResponseWriter{ResponseWriter: w}
		defer func() {
			observer(newActivity(backend, started, r, observed, ""))
		}()
		next.ServeHTTP(observed, r)
	})
}

func newActivity(backend string, started time.Time, r *http.Request, observed *activityResponseWriter, operation string) Activity {
	status := observed.status
	if status == 0 {
		status = http.StatusOK
	}
	if operation == "" {
		operation = activityOperation(r)
	}
	outcome := "success"
	if r.Context().Err() != nil {
		outcome = "canceled"
	} else if status >= http.StatusBadRequest {
		outcome = "error"
	}
	return Activity{
		Date:      started.UTC(),
		Backend:   backend,
		Operation: operation,
		Method:    r.Method,
		Path:      activityPath(r),
		Status:    status,
		Bytes:     observed.bytes,
		Duration:  time.Since(started),
		Outcome:   outcome,
	}
}

func activityOperation(r *http.Request) string {
	if r.URL.Path == ArchivePath {
		return "archive"
	}
	if r.Method == "PROPFIND" {
		return "list"
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return "download"
	}
	return "request"
}

func activityPath(r *http.Request) string {
	value := r.URL.Path
	if value == ArchivePath {
		value = r.URL.Query().Get("path")
	} else {
		value = strings.TrimPrefix(value, FSPath)
	}
	if value == "" {
		value = "/"
	}
	value = path.Clean("/" + strings.TrimPrefix(value, "/"))
	if len(value) > 512 {
		value = value[:509] + "..."
	}
	return value
}

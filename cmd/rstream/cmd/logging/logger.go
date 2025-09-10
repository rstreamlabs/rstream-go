// See LICENSE file in the project root for license information.

package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"golang.org/x/term"
)

func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func New(c Config) (*slog.Logger, error) {
	if c.Output == nil {
		c.Output = os.Stderr
	}
	format := strings.ToLower(c.Format)
	if format == "auto" {
		if IsTerminal(c.Output) {
			format = "text-pretty"
		} else {
			format = "text"
		}
	}
	level, err := parseLevel(c.Level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level, AddSource: false}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(c.Output, opts)), nil
	case "json-pretty":
		return slog.New(slog.NewJSONHandler(newPrettyJSONWriter(c.Output), opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(c.Output, opts)), nil
	case "text-pretty":
		return slog.New(tint.NewHandler(c.Output, &tint.Options{
			Level:      level,
			AddSource:  opts.AddSource,
			TimeFormat: time.RFC3339,
		})), nil
	default:
		return nil, fmt.Errorf("invalid log format: %q (valid: auto, json, json-pretty, text, text-pretty)", c.Format)
	}
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level: %q (valid: debug, info, warn, error)", s)
	}
}

type prettyJSONWriter struct {
	dst io.Writer
	buf bytes.Buffer
}

func newPrettyJSONWriter(dst io.Writer) *prettyJSONWriter { return &prettyJSONWriter{dst: dst} }

func (w *prettyJSONWriter) Write(p []byte) (int, error) {
	b := bytes.TrimSpace(p)
	if len(b) > 0 && b[0] == '{' {
		var v any
		if json.Unmarshal(b, &v) == nil {
			enc := json.NewEncoder(&w.buf)
			enc.SetIndent("", "  ")
			if err := enc.Encode(v); err == nil {
				if _, err := w.dst.Write(w.buf.Bytes()); err != nil {
					w.buf.Reset()
					return 0, err
				}
				n := len(p)
				w.buf.Reset()
				return n, nil
			}
		}
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	if _, err := w.dst.Write(b); err != nil {
		return 0, err
	}
	return len(p), nil
}

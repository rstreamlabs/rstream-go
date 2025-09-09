// See LICENSE file in the project root for license information.

package logging

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
)

func New(c Config) *slog.Logger {
	w := c.Output
	if w == nil {
		w = os.Stdout
	}
	var level slog.Level
	switch strings.ToLower(c.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "none", "off", "silent":
		level = slog.Level(1000)
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level, AddSource: false}
	switch strings.ToLower(c.Format) {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts))
	case "pretty":
		return slog.New(tint.NewHandler(w, &tint.Options{
			Level:      opts.Level,
			AddSource:  opts.AddSource,
			TimeFormat: time.RFC3339,
		}))
	default:
		return slog.New(slog.NewTextHandler(w, opts))
	}
}

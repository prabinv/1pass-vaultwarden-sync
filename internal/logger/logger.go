// Package logger configures the process-wide slog default logger.
// Call Setup once from main before any other packages log.
package logger

import (
	"io"
	"log/slog"
)

// Setup configures the default slog logger.
//
// w controls where log output is written. Pass io.Discard to suppress all
// output (e.g. during interactive TUI sessions). When debug is true,
// DEBUG-level messages are included; otherwise only INFO and above are emitted.
func Setup(w io.Writer, debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	})))
}

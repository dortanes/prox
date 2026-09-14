package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	mu      sync.Mutex
	writers []*FileWriter // all managed file writers (for reopen/close)
)

// Config holds logger initialization parameters.
type Config struct {
	Level    string // "debug", "info", "warn", "error"
	ErrorLog string // file path for error-level log (empty = disabled)
}

// Setup configures the global slog default with a colorized console handler
// and an optional error log file handler.
func Setup(cfg Config) error {
	mu.Lock()
	defer mu.Unlock()

	closeWritersLocked()

	level := ParseLevel(cfg.Level)

	handlers := []slog.Handler{
		NewConsoleHandler(os.Stderr, &slog.HandlerOptions{Level: level}),
	}

	// Error log file — captures warn and error levels as JSON.
	if cfg.ErrorLog != "" {
		w, err := NewFileWriter(cfg.ErrorLog)
		if err != nil {
			return fmt.Errorf("opening error log %q: %w", cfg.ErrorLog, err)
		}
		writers = append(writers, w)
		handlers = append(handlers, slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		}))
	}

	var handler slog.Handler
	if len(handlers) == 1 {
		handler = handlers[0]
	} else {
		handler = &multiHandler{handlers: handlers}
	}

	slog.SetDefault(slog.New(handler))
	return nil
}

// ReopenFiles closes and re-opens all log files.
// Designed for SIGHUP-triggered rotation with external tools like logrotate.
func ReopenFiles() error {
	mu.Lock()
	defer mu.Unlock()
	for _, w := range writers {
		if err := w.Reopen(); err != nil {
			return err
		}
	}
	return AccessReopenFiles()
}

// Close flushes and closes all managed log files.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	closeWritersLocked()
	AccessClose()
}

func closeWritersLocked() {
	for _, w := range writers {
		w.Close()
	}
	writers = nil
}

// ParseLevel converts a level string to slog.Level.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// multiHandler fans out log records to multiple slog.Handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}

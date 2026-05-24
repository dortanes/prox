// Package logger provides colorized console logging and file-based access/error logging.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// ANSI escape sequences for console coloring.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiGray   = "\033[90m"
)

// ConsoleHandler is a slog.Handler that writes colorized, compact log lines to a writer.
//
// Output format:
//
//	15:04:05 INF message key=value key2=value2
type ConsoleHandler struct {
	opts  slog.HandlerOptions
	w     io.Writer
	mu    *sync.Mutex
	color bool
	attrs []slog.Attr
	group string
}

// NewConsoleHandler creates a handler that writes compact, human-friendly log lines.
// Color output is auto-detected based on terminal capabilities.
func NewConsoleHandler(w io.Writer, opts *slog.HandlerOptions) *ConsoleHandler {
	h := &ConsoleHandler{
		w:  w,
		mu: &sync.Mutex{},
	}
	if opts != nil {
		h.opts = *opts
	}
	if f, ok := w.(*os.File); ok {
		h.color = isTerminal(f)
	}
	return h
}

func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

func (h *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	buf := newBuffer()

	// Timestamp: HH:MM:SS
	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	h.writeTimestamp(buf, ts)
	buf.WriteByte(' ')

	// Level tag (3 chars, colored).
	h.writeLevel(buf, r.Level)
	buf.WriteByte(' ')

	// Message.
	buf.WriteString(r.Message)

	// Pre-stored attrs.
	for _, a := range h.attrs {
		h.writeAttr(buf, a)
	}

	// Record attrs.
	r.Attrs(func(a slog.Attr) bool {
		h.writeAttr(buf, a)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	bufPool.Put(buf)
	return err
}

func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &ConsoleHandler{
		opts:  h.opts,
		w:     h.w,
		mu:    h.mu,
		color: h.color,
		attrs: newAttrs,
		group: h.group,
	}
}

func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &ConsoleHandler{
		opts:  h.opts,
		w:     h.w,
		mu:    h.mu,
		color: h.color,
		attrs: h.attrs,
		group: g,
	}
}

func (h *ConsoleHandler) writeTimestamp(buf *buffer, t time.Time) {
	s := t.Format("15:04:05")
	if h.color {
		buf.WriteString(ansiDim)
		buf.WriteString(s)
		buf.WriteString(ansiReset)
	} else {
		buf.WriteString(s)
	}
}

func (h *ConsoleHandler) writeLevel(buf *buffer, level slog.Level) {
	tag, color := levelTag(level)
	if h.color {
		buf.WriteString(color)
		buf.WriteString(tag)
		buf.WriteString(ansiReset)
	} else {
		buf.WriteString(tag)
	}
}

func (h *ConsoleHandler) writeAttr(buf *buffer, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}

	buf.WriteByte(' ')

	key := a.Key
	if h.group != "" {
		key = h.group + "." + key
	}

	if h.color {
		buf.WriteString(ansiDim)
		buf.WriteString(key)
		buf.WriteByte('=')
		buf.WriteString(ansiReset)
	} else {
		buf.WriteString(key)
		buf.WriteByte('=')
	}

	val := formatValue(a.Value)
	buf.WriteString(val)
}

// levelTag returns the 3-char tag and ANSI color for a log level.
func levelTag(level slog.Level) (string, string) {
	switch {
	case level < slog.LevelInfo:
		return "DBG", ansiGray
	case level < slog.LevelWarn:
		return "INF", ansiCyan
	case level < slog.LevelError:
		return "WRN", ansiYellow
	default:
		return "ERR", ansiRed
	}
}

// formatValue renders a slog.Value as a string, quoting if necessary.
func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if needsQuoting(s) {
			return fmt.Sprintf("%q", s)
		}
		return s
	default:
		return v.String()
	}
}

// needsQuoting returns true if a string value should be quoted in log output.
func needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	for _, c := range s {
		if c <= ' ' || c == '"' || c == '=' || c == '\\' {
			return true
		}
	}
	return false
}

// isTerminal reports whether f is a terminal device.
func isTerminal(f *os.File) bool {
	// On Windows, ModeCharDevice is always set for console handles.
	// On Unix, it is set for TTY devices.
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// --- buffer pool ---

var bufPool = sync.Pool{
	New: func() any { return new(buffer) },
}

type buffer struct {
	buf []byte
}

func newBuffer() *buffer {
	b := bufPool.Get().(*buffer)
	b.buf = b.buf[:0]
	return b
}

func (b *buffer) WriteByte(c byte) error { b.buf = append(b.buf, c); return nil }
func (b *buffer) WriteString(s string)   { b.buf = append(b.buf, s...) }
func (b *buffer) Bytes() []byte          { return b.buf }


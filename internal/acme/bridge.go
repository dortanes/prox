package acme

import (
	"context"
	"log/slog"
	"math"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// newSlogZapBridge creates a *zap.Logger that forwards all log output
// to slog.Default(), prefixed with "acme" as the logger name.
func newSlogZapBridge() *zap.Logger {
	return zap.New(&slogCore{
		handler: slog.Default().Handler(),
		fields:  nil,
	})
}

// slogCore implements zapcore.Core by forwarding to an slog.Handler.
type slogCore struct {
	handler slog.Handler
	fields  []slog.Attr
}

func (c *slogCore) Enabled(lvl zapcore.Level) bool {
	return c.handler.Enabled(context.Background(), zapToSlogLevel(lvl))
}

func (c *slogCore) With(fields []zapcore.Field) zapcore.Core {
	attrs := make([]slog.Attr, 0, len(c.fields)+len(fields))
	attrs = append(attrs, c.fields...)
	for _, f := range fields {
		attrs = append(attrs, zapFieldToSlogAttr(f))
	}
	return &slogCore{
		handler: c.handler,
		fields:  attrs,
	}
}

func (c *slogCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *slogCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	level := zapToSlogLevel(entry.Level)

	attrs := make([]any, 0, len(c.fields)+len(fields))
	for _, a := range c.fields {
		attrs = append(attrs, a)
	}
	for _, f := range fields {
		attrs = append(attrs, zapFieldToSlogAttr(f))
	}

	slog.Default().Log(context.Background(), level, entry.Message, attrs...)
	return nil
}

func (c *slogCore) Sync() error {
	return nil
}

// zapToSlogLevel converts a zap log level to an slog log level.
func zapToSlogLevel(lvl zapcore.Level) slog.Level {
	switch {
	case lvl >= zapcore.ErrorLevel:
		return slog.LevelError
	case lvl >= zapcore.WarnLevel:
		return slog.LevelWarn
	case lvl >= zapcore.InfoLevel:
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

// zapFieldToSlogAttr converts a zap field to an slog attribute.
func zapFieldToSlogAttr(f zapcore.Field) slog.Attr {
	switch f.Type {
	case zapcore.StringType:
		return slog.String(f.Key, f.String)
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return slog.Int64(f.Key, f.Integer)
	case zapcore.Float64Type:
		return slog.Float64(f.Key, math.Float64frombits(uint64(f.Integer)))
	case zapcore.BoolType:
		return slog.Bool(f.Key, f.Integer == 1)
	case zapcore.DurationType:
		return slog.Duration(f.Key, time.Duration(f.Integer))
	case zapcore.ErrorType:
		if f.Interface != nil {
			return slog.String(f.Key, f.Interface.(error).Error())
		}
		return slog.String(f.Key, "<nil>")
	default:
		if f.Interface != nil {
			return slog.Any(f.Key, f.Interface)
		}
		return slog.String(f.Key, f.String)
	}
}

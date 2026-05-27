// Package throttle implements token-bucket bandwidth limiting for proxy connections.
package throttle

import (
	"context"
	"io"
	"sync"
	"time"
)

// maxChunk is the largest write chunk passed through a throttled Writer.
const maxChunk = 32 * 1024

// Bucket is a goroutine-safe token-bucket rate limiter.
type Bucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per nanosecond
	tokens float64
	max    float64
	last   int64 // UnixNano of last refill
}

// NewBucket creates a rate limiter allowing bytesPerSec throughput.
// Returns nil if bytesPerSec <= 0.
func NewBucket(bytesPerSec int64) *Bucket {
	if bytesPerSec <= 0 {
		return nil
	}
	burst := bytesPerSec
	if burst < 64*1024 {
		burst = 64 * 1024
	}
	return &Bucket{
		rate:   float64(bytesPerSec) / 1e9,
		tokens: float64(burst),
		max:    float64(burst),
		last:   time.Now().UnixNano(),
	}
}

// Wait consumes n tokens, sleeping if the bucket is empty.
func (b *Bucket) Wait(n int) {
	b.mu.Lock()
	now := time.Now().UnixNano()
	elapsed := now - b.last
	b.last = now
	b.tokens += float64(elapsed) * b.rate
	if b.tokens > b.max {
		b.tokens = b.max
	}
	b.tokens -= float64(n)
	deficit := b.tokens
	b.mu.Unlock()

	if deficit < 0 {
		time.Sleep(time.Duration(-deficit / b.rate))
	}
}

// Writer throttles writes through one or more Buckets.
type Writer struct {
	w       io.Writer
	buckets []*Bucket
}

// NewWriter wraps w with bandwidth limiting. Nil buckets are filtered out.
// Returns nil if no valid buckets remain (caller should use the original writer).
func NewWriter(w io.Writer, buckets ...*Bucket) *Writer {
	valid := make([]*Bucket, 0, len(buckets))
	for _, b := range buckets {
		if b != nil {
			valid = append(valid, b)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	return &Writer{w: w, buckets: valid}
}

// Write splits p into chunks, throttling each through all buckets.
func (tw *Writer) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := len(p)
		if chunk > maxChunk {
			chunk = maxChunk
		}
		for _, b := range tw.buckets {
			b.Wait(chunk)
		}
		n, err := tw.w.Write(p[:chunk])
		total += n
		if err != nil {
			return total, err
		}
		p = p[chunk:]
	}
	return total, nil
}

// Reader throttles reads through one or more Buckets.
type Reader struct {
	r       io.Reader
	buckets []*Bucket
}

// NewReader wraps r with bandwidth limiting. Nil buckets are filtered out.
// Returns nil if no valid buckets remain (caller should use the original reader).
func NewReader(r io.Reader, buckets ...*Bucket) *Reader {
	valid := make([]*Bucket, 0, len(buckets))
	for _, b := range buckets {
		if b != nil {
			valid = append(valid, b)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	return &Reader{r: r, buckets: valid}
}

// Read reads from the underlying reader, then throttles based on bytes read.
func (tr *Reader) Read(p []byte) (int, error) {
	n, err := tr.r.Read(p)
	if n > 0 {
		for _, b := range tr.buckets {
			b.Wait(n)
		}
	}
	return n, err
}

// MbpsToBytes converts megabits per second to bytes per second.
func MbpsToBytes(mbps float64) int64 {
	return int64(mbps * 1_000_000 / 8)
}

// Limits holds per-direction bandwidth buckets for a connection.
type Limits struct {
	Download []*Bucket
	Upload   []*Bucket
}

type ctxKey struct{}

// NewContext returns a child context carrying the given Limits.
func NewContext(ctx context.Context, l *Limits) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext extracts Limits from the context, or nil if not set.
func FromContext(ctx context.Context) *Limits {
	l, _ := ctx.Value(ctxKey{}).(*Limits)
	return l
}

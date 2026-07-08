// Package logbuf provides an in-memory ring buffer of recent log records so the
// web UI can display application logs without external tooling.
package logbuf

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Entry is a single captured log record.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
}

// Buffer is a fixed-size, concurrency-safe ring buffer of log entries.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	max     int
}

// New returns a Buffer that keeps at most max entries. If max <= 0 a default
// of 500 is used.
func New(max int) *Buffer {
	if max <= 0 {
		max = 500
	}
	return &Buffer{max: max, entries: make([]Entry, 0, max)}
}

// Add appends an entry, discarding the oldest entry when the buffer is full.
func (b *Buffer) Add(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, e)
	if len(b.entries) > b.max {
		b.entries = b.entries[len(b.entries)-b.max:]
	}
}

// Entries returns a copy of the buffered entries in chronological order
// (oldest first).
func (b *Buffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, len(b.entries))
	copy(out, b.entries)
	return out
}

// Handler is an slog.Handler that records every log record into a Buffer and
// then delegates to an inner handler.
type Handler struct {
	inner slog.Handler
	buf   *Buffer
}

// NewHandler wraps inner so that all records are also captured in buf.
func NewHandler(inner slog.Handler, buf *Buffer) *Handler {
	return &Handler{inner: inner, buf: buf}
}

// Enabled reports whether the inner handler is enabled for the given level.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

// Handle records the record in the buffer and forwards it to the inner handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	h.buf.Add(Entry{Time: r.Time, Level: r.Level, Message: r.Message})
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a new Handler with the given attributes added to the inner
// handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(attrs), buf: h.buf}
}

// WithGroup returns a new Handler with the given group added to the inner
// handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), buf: h.buf}
}

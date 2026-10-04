// Package scrub removes known secret values from text before it leaves the
// process: log records, HTTP error bodies, metrics. It is the last line of
// defense; code should not put secrets into messages in the first place.
package scrub

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
)

// Mask replaces a secret.
const Mask = "[REDACTED]"

// Scrubber replaces every registered secret, and its URL-encoded form, with
// Mask. The zero value scrubs nothing. Safe for concurrent use.
type Scrubber struct {
	r atomic.Pointer[strings.Replacer]
}

// New returns a Scrubber for secrets (longest first is handled internally).
func New(secrets []string) *Scrubber {
	s := &Scrubber{}
	s.Set(secrets)
	return s
}

// Set replaces the secret list.
func (s *Scrubber) Set(secrets []string) {
	var pairs []string
	seen := map[string]bool{}
	add := func(v string) {
		if len(v) < 6 || seen[v] {
			return
		}
		seen[v] = true
		pairs = append(pairs, v, Mask)
	}
	// strings.Replacer tries patterns in argument order at each position, so
	// longer secrets go first.
	sorted := append([]string(nil), secrets...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j]) > len(sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	for _, v := range sorted {
		add(v)
		add(url.QueryEscape(v))
		add(url.PathEscape(v))
	}
	if len(pairs) == 0 {
		s.r.Store(nil)
		return
	}
	s.r.Store(strings.NewReplacer(pairs...))
}

// String scrubs s.
func (s *Scrubber) String(v string) string {
	if s == nil {
		return v
	}
	r := s.r.Load()
	if r == nil {
		return v
	}
	return r.Replace(v)
}

// Bytes scrubs b.
func (s *Scrubber) Bytes(b []byte) []byte {
	if s == nil || s.r.Load() == nil {
		return b
	}
	return []byte(s.String(string(b)))
}

// Handler wraps a slog.Handler and scrubs the message and every string,
// error and Stringer attribute, including nested groups.
type Handler struct {
	next slog.Handler
	s    *Scrubber
}

// NewHandler wraps next.
func NewHandler(next slog.Handler, s *Scrubber) *Handler { return &Handler{next: next, s: s} }

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, h.s.String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *Handler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.s.String(v.String()))
	case slog.KindGroup:
		g := v.Group()
		out := make([]any, 0, len(g))
		for _, ga := range g {
			out = append(out, h.attr(ga))
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, h.s.String(x.Error()))
		case interface{ String() string }:
			return slog.String(a.Key, h.s.String(x.String()))
		}
		return a
	default:
		return a
	}
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = h.attr(a)
	}
	return &Handler{next: h.next.WithAttrs(out), s: h.s}
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), s: h.s}
}

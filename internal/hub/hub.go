// Package hub is what all instance listeners of one Airrbag process share:
// the list of running instances (for the dashboard's cross-instance views),
// the log of recent guard decisions, and the redacted configuration.
package hub

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/fishingpvalues/airrbag/internal/config"
	"github.com/fishingpvalues/airrbag/internal/engine"
)

// Instance is one running *Arr front.
type Instance struct {
	Name       string
	App        string
	AppVersion string
	Listen     string
	Engine     *engine.Engine
	// Health reports download-client reachability; fresh bypasses the cache.
	Health func(ctx context.Context, fresh bool) map[string]string
}

// Decision is what the guard did with one DELETE.
type Decision string

const (
	Blocked     Decision = "blocked"      // answered 409
	WouldBlock  Decision = "would-block"  // dry run: logged and passed
	Overridden  Decision = "overridden"   // a keep file, let through by an override
	ErrorClosed Decision = "error-closed" // could not verify, failed closed (503)
	ErrorOpen   Decision = "error-open"   // could not verify, passed
	// UnknownPassed: a file with unproven origin, deleted without a
	// confirmation under guard.unknown "confirm" (an API caller).
	UnknownPassed Decision = "unknown-passed"
)

// GuardEvent is one recorded guard decision.
type GuardEvent struct {
	Time     time.Time `json:"time"`
	Instance string    `json:"instance"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Decision Decision  `json:"decision"`
	Reason   string    `json:"reason"`
	Files    int       `json:"files"`
	Titles   []string  `json:"titles,omitempty"`
	Override string    `json:"override,omitempty"`
}

// maxEvents bounds the in-memory guard log.
const maxEvents = 500

// Hub is safe for concurrent use.
type Hub struct {
	started  time.Time
	version  string
	redacted config.Redacted

	mu        sync.RWMutex
	instances map[string]*Instance
	events    []GuardEvent // ring, oldest first once full
	next      int
	full      bool
}

// New creates a Hub.
func New(version string, cfg *config.Config) *Hub {
	h := &Hub{started: time.Now(), version: version, instances: map[string]*Instance{}}
	if cfg != nil {
		h.redacted = cfg.Redact()
	}
	return h
}

// Started is when the process started.
func (h *Hub) Started() time.Time { return h.started }

// Version is the Airrbag version.
func (h *Hub) Version() string { return h.version }

// Config is the redacted configuration.
func (h *Hub) Config() config.Redacted { return h.redacted }

// Register adds or replaces a running instance.
func (h *Hub) Register(i *Instance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instances[i.Name] = i
}

// Instances returns the running instances sorted by name.
func (h *Hub) Instances() []*Instance {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Instance, 0, len(h.instances))
	for _, i := range h.instances {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Instance returns one running instance by name.
func (h *Hub) Instance(name string) (*Instance, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	i, ok := h.instances[name]
	return i, ok
}

// Record appends a guard decision, dropping the oldest beyond maxEvents.
func (h *Hub) Record(e GuardEvent) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.events) < maxEvents {
		h.events = append(h.events, e)
		return
	}
	h.events[h.next] = e
	h.next = (h.next + 1) % maxEvents
	h.full = true
}

// Events returns recorded decisions, newest first.
func (h *Hub) Events() []GuardEvent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]GuardEvent, 0, len(h.events))
	if h.full {
		out = append(out, h.events[h.next:]...)
		out = append(out, h.events[:h.next]...)
	} else {
		out = append(out, h.events...)
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out
}

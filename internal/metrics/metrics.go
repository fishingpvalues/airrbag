// Package metrics is a tiny Prometheus text-format exporter: a handful of
// labeled counters do not justify the client_golang dependency tree.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Registry holds counters and gauges keyed by name and label set.
type Registry struct {
	mu     sync.Mutex
	help   map[string]string
	kind   map[string]string
	values map[string]map[string]float64 // name -> rendered labels -> value
}

// New creates a Registry.
func New() *Registry {
	return &Registry{help: map[string]string{}, kind: map[string]string{}, values: map[string]map[string]float64{}}
}

// Describe registers help text and type for a metric name.
func (r *Registry) Describe(name, kind, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.help[name], r.kind[name] = help, kind
	if r.values[name] == nil {
		r.values[name] = map[string]float64{}
	}
}

func render(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(labels[i+1])
		fmt.Fprintf(&b, `%s="%s"`, labels[i], v)
	}
	b.WriteByte('}')
	return b.String()
}

// Add increments a counter. labels are key, value pairs.
func (r *Registry) Add(name string, delta float64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values[name] == nil {
		r.values[name] = map[string]float64{}
	}
	r.values[name][render(labels)] += delta
}

// Set sets a gauge.
func (r *Registry) Set(name string, v float64, labels ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values[name] == nil {
		r.values[name] = map[string]float64{}
	}
	r.values[name][render(labels)] = v
}

// Write renders the text exposition format.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.values))
	for n := range r.values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if h := r.help[n]; h != "" {
			fmt.Fprintf(w, "# HELP %s %s\n", n, h)
		}
		if k := r.kind[n]; k != "" {
			fmt.Fprintf(w, "# TYPE %s %s\n", n, k)
		}
		keys := make([]string, 0, len(r.values[n]))
		for k := range r.values[n] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s%s %g\n", n, k, r.values[n][k])
		}
	}
}

// Handler serves /metrics.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		r.Write(w)
	})
}

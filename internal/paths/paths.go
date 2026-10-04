// Package paths translates paths between the *Arr's view, a download client's
// view and Airrbag's own filesystem view. Each container mounts the same disks
// under different prefixes, so a path is only comparable after translation.
package paths

import (
	"path"
	"sort"
	"strings"

	"github.com/fishingpvalues/airrbag/internal/config"
)

// Source identifies whose view a path is in.
type Source struct {
	Kind string // "arr" or "client"
	Name string // instance or client name, optional
}

// Mapper applies prefix rules, longest prefix first.
type Mapper struct {
	rules []config.PathMapping
}

// New builds a Mapper from config rules.
func New(rules []config.PathMapping) *Mapper {
	r := make([]config.PathMapping, len(rules))
	copy(r, rules)
	for i := range r {
		r[i].From = clean(r[i].From)
		r[i].To = clean(r[i].To)
	}
	sort.SliceStable(r, func(i, j int) bool { return len(r[i].From) > len(r[j].From) })
	return &Mapper{rules: r}
}

func clean(p string) string {
	if p == "" {
		return p
	}
	c := path.Clean(p)
	return c
}

func (m *Mapper) applies(rule config.PathMapping, src Source) bool {
	switch rule.Source {
	case "", "any":
		return true
	case src.Kind:
		return true
	default:
		return rule.Source == src.Name
	}
}

// Local translates p from src's view to Airrbag's view. A path no rule covers
// is returned unchanged: the common case where Airrbag mounts the disks at
// the same paths as the *Arr.
func (m *Mapper) Local(p string, src Source) string {
	if p == "" {
		return p
	}
	p = clean(p)
	for _, r := range m.rules {
		if !m.applies(r, src) {
			continue
		}
		if p == r.From {
			return r.To
		}
		if strings.HasPrefix(p, r.From+"/") || r.From == "/" {
			rest := strings.TrimPrefix(p, r.From)
			return path.Join(r.To, rest)
		}
	}
	return p
}

// Within reports whether child is p itself or below it.
func Within(child, parent string) bool {
	if child == "" || parent == "" {
		return false
	}
	child, parent = path.Clean(child), path.Clean(parent)
	return child == parent || strings.HasPrefix(child, strings.TrimSuffix(parent, "/")+"/")
}

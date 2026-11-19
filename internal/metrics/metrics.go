// Package metrics counts traffic per route and per upstream.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
)

// maxUpstreams bounds the upstream table: plugins can push arbitrary targets.
const maxUpstreams = 4096

type counters struct {
	active, requests, errors, bytesIn, bytesOut atomic.Int64
}

// Registry holds the counters. A nil Registry records nothing.
type Registry struct {
	mu        sync.RWMutex
	routes    map[string]*counters
	upstreams map[string]*counters
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{routes: make(map[string]*counters), upstreams: make(map[string]*counters)}
}

func (r *Registry) entry(table map[string]*counters, key string, limit int) *counters {
	r.mu.RLock()
	c := table[key]
	r.mu.RUnlock()
	if c != nil {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c = table[key]; c == nil && (limit == 0 || len(table) < limit) {
		c = &counters{}
		table[key] = c
	}
	return c
}

// Retain drops idle routes that left the configuration.
func (r *Registry) Retain(routeIDs map[string]bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.routes {
		if !routeIDs[id] && c.active.Load() == 0 {
			delete(r.routes, id)
		}
	}
}

// Track is one request or connection in progress.
type Track struct {
	reg      *Registry
	route    *counters
	upstream *counters
}

// Begin starts tracking traffic on a route. It returns nil on a nil Registry;
// all Track methods accept a nil receiver.
func (r *Registry) Begin(routeID string) *Track {
	if r == nil {
		return nil
	}
	c := r.entry(r.routes, routeID, 0)
	c.active.Add(1)
	c.requests.Add(1)
	return &Track{reg: r, route: c}
}

// SetUpstream attributes the traffic to an upstream, at most once.
func (t *Track) SetUpstream(addr string) {
	if t == nil || addr == "" || t.upstream != nil {
		return
	}
	t.upstream = t.reg.entry(t.reg.upstreams, addr, maxUpstreams)
	if t.upstream != nil {
		t.upstream.active.Add(1)
		t.upstream.requests.Add(1)
	}
}

// End records the outcome.
func (t *Track) End(bytesIn, bytesOut int64, failed bool) {
	if t == nil {
		return
	}
	for _, c := range []*counters{t.route, t.upstream} {
		if c == nil {
			continue
		}
		c.active.Add(-1)
		c.bytesIn.Add(bytesIn)
		c.bytesOut.Add(bytesOut)
		if failed {
			c.errors.Add(1)
		}
	}
}

// Entry is the state of one route or upstream.
type Entry struct {
	ID       string `json:"id"`
	Active   int64  `json:"active"`
	Requests int64  `json:"requests"`
	Errors   int64  `json:"errors"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

// Snapshot is the JSON body of GET /api/metrics.
type Snapshot struct {
	Routes    []Entry `json:"routes"`
	Upstreams []Entry `json:"upstreams"`
}

// Snapshot returns the counters sorted by id.
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{Routes: []Entry{}, Upstreams: []Entry{}}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{Routes: entries(r.routes), Upstreams: entries(r.upstreams)}
}

func entries(table map[string]*counters) []Entry {
	result := make([]Entry, 0, len(table))
	for id, c := range table {
		result = append(result, Entry{
			ID:       id,
			Active:   c.active.Load(),
			Requests: c.requests.Load(),
			Errors:   c.errors.Load(),
			BytesIn:  c.bytesIn.Load(),
			BytesOut: c.bytesOut.Load(),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

package conntrack

import "sync"

// Key identifies the traffic of one session on one route.
type Key struct {
	RouteID string
	Session string
}

// Bytes holds byte counts in both directions.
type Bytes struct {
	Rx int64 // client → upstream
	Tx int64 // upstream → client
}

func (b Bytes) add(o Bytes) Bytes { return Bytes{Rx: b.Rx + o.Rx, Tx: b.Tx + o.Tx} }
func (b Bytes) sub(o Bytes) Bytes { return Bytes{Rx: b.Rx - o.Rx, Tx: b.Tx - o.Tx} }
func (b Bytes) zero() bool        { return b.Rx == 0 && b.Tx == 0 }

// Conn is a live connection or stream that carries a session key.
type Conn struct {
	key     Key
	counter *Counter // nil when no meter needs the connection's bytes
	drop    func()
}

func (c *Conn) bytes() Bytes { return Bytes{Rx: c.counter.Rx(), Tx: c.counter.Tx()} }

// Meter reports, at each Collect, the bytes sessions moved since its previous
// Collect. Every subscriber owns its meter, so subscribers with different
// intervals never take bytes from each other.
type Meter struct {
	reported map[*Conn]Bytes // bytes of live connections already collected
	pending  map[Key]Bytes   // uncollected bytes of closed connections
}

// Registry holds the live connections that carry a session key.
type Registry struct {
	mu        sync.Mutex
	bySession map[string]map[*Conn]struct{}
	meters    map[*Meter]struct{}
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		bySession: make(map[string]map[*Conn]struct{}),
		meters:    make(map[*Meter]struct{}),
	}
}

// Open registers a connection carrying session on routeID. counter may be nil
// when nobody meters the route. drop must close the connection and its
// upstream; it may be called more than once.
func (r *Registry) Open(routeID, session string, counter *Counter, drop func()) *Conn {
	c := &Conn{key: Key{RouteID: routeID, Session: session}, counter: counter, drop: drop}
	r.mu.Lock()
	conns := r.bySession[session]
	if conns == nil {
		conns = make(map[*Conn]struct{})
		r.bySession[session] = conns
	}
	conns[c] = struct{}{}
	r.mu.Unlock()
	return c
}

// Close unregisters a connection once it has stopped moving bytes. Bytes it
// moved after the last Collect of each meter are kept for that meter's next
// Collect.
func (r *Registry) Close(c *Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if conns := r.bySession[c.key.Session]; conns != nil {
		delete(conns, c)
		if len(conns) == 0 {
			delete(r.bySession, c.key.Session)
		}
	}
	if c.counter == nil {
		return
	}
	total := c.bytes()
	for m := range r.meters {
		delta := total.sub(m.reported[c])
		delete(m.reported, c)
		if !delta.zero() {
			m.pending[c.key] = m.pending[c.key].add(delta)
		}
	}
}

// Drop closes every live connection carrying one of the sessions and returns
// how many it closed. Unknown sessions are ignored.
func (r *Registry) Drop(sessions ...string) int {
	var victims []*Conn
	r.mu.Lock()
	for _, s := range sessions {
		for c := range r.bySession[s] {
			victims = append(victims, c)
		}
	}
	r.mu.Unlock()
	for _, c := range victims {
		c.drop()
	}
	return len(victims)
}

// NewMeter creates a meter. Its first Collect reports every byte live
// connections have moved so far.
func (r *Registry) NewMeter() *Meter {
	m := &Meter{reported: make(map[*Conn]Bytes), pending: make(map[Key]Bytes)}
	r.mu.Lock()
	r.meters[m] = struct{}{}
	r.mu.Unlock()
	return m
}

// RemoveMeter stops tracking closed connections for m.
func (r *Registry) RemoveMeter(m *Meter) {
	r.mu.Lock()
	delete(r.meters, m)
	r.mu.Unlock()
}

// Collect returns the bytes each session moved since m's previous Collect,
// including connections that closed in between. Sessions that moved nothing
// are left out.
func (r *Registry) Collect(m *Meter) map[Key]Bytes {
	r.mu.Lock()
	defer r.mu.Unlock()
	usage := m.pending
	m.pending = make(map[Key]Bytes)
	for _, conns := range r.bySession {
		for c := range conns {
			if c.counter == nil {
				continue
			}
			total := c.bytes()
			delta := total.sub(m.reported[c])
			if delta.zero() {
				continue
			}
			m.reported[c] = total
			usage[c.key] = usage[c.key].add(delta)
		}
	}
	return usage
}

// Restore returns usage that could not be delivered to m, so its next Collect
// reports it again.
func (r *Registry) Restore(m *Meter, usage map[Key]Bytes) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, b := range usage {
		m.pending[k] = m.pending[k].add(b)
	}
}

// Package balancer implements load balancing strategies for upstream selection.
package balancer

import (
	"math/rand"
	"sync"
	"sync/atomic"
)

// Balancer selects a target from a pool of upstreams.
type Balancer interface {
	// Next returns the next target address.
	// For connection-tracking strategies (e.g. LeastConn), this also
	// marks the target as having one more active connection.
	// Returns "" if the pool is empty or every target is ejected.
	Next() string

	// NextExcept is Next that also skips the excluded targets, for picking a
	// replacement after a failed dial.
	NextExcept(exclude []string) string

	// Done signals that a request/connection to the given target has finished.
	// This is a no-op for strategies that don't track active connections.
	Done(target string)

	// Eject takes target out of rotation for the balancer's cool-down after
	// a dial to it failed. A no-op when ejection is disabled.
	Eject(target string, cause error)

	// SwapTargets atomically replaces the target pool.
	// Active connections tracked by Done() are reset, and targets that left
	// the pool lose their ejection.
	SwapTargets(targets []string)

	// Targets returns a copy of the current target pool.
	Targets() []string
}

// strategy is a flat balancer that Grouped can drive as a sub-pool: it shares
// the route's ejection state, so swapping one sub-pool keeps the others' ejections.
type strategy interface {
	Balancer
	swap(targets []string)
	ejections() *Ejections
}

// RoundRobin distributes requests evenly across targets in order.
type RoundRobin struct {
	pool    atomic.Pointer[rrPool]
	counter atomic.Uint64
	ej      *Ejections
}

type rrPool struct {
	targets []string
}

// NewRoundRobin creates a round-robin balancer without ejection.
func NewRoundRobin(targets []string) *RoundRobin {
	return newRoundRobin(targets, nil)
}

func newRoundRobin(targets []string, ej *Ejections) *RoundRobin {
	rr := &RoundRobin{ej: ej}
	rr.pool.Store(&rrPool{targets: targets})
	return rr
}

func (rr *RoundRobin) Next() string { return rr.NextExcept(nil) }

func (rr *RoundRobin) NextExcept(exclude []string) string {
	p := rr.pool.Load()
	n := uint64(len(p.targets))
	if n == 0 {
		return ""
	}
	start := rr.counter.Add(1) - 1
	if !rr.ej.filtering(exclude) {
		return p.targets[start%n]
	}
	for i := uint64(0); i < n; i++ {
		if t := p.targets[(start+i)%n]; rr.ej.available(t, exclude) {
			return t
		}
	}
	return ""
}

func (rr *RoundRobin) Done(string) {}

func (rr *RoundRobin) Eject(target string, cause error) { rr.ej.eject(target, cause) }

// Take reports whether the target is in the pool. Round-robin keeps no
// per-target state, so nothing is reserved.
func (rr *RoundRobin) Take(target string) bool {
	return contains(rr.pool.Load().targets, target)
}

func (rr *RoundRobin) SwapTargets(targets []string) {
	rr.swap(targets)
	rr.ej.retain(targets)
}

func (rr *RoundRobin) swap(targets []string) {
	rr.pool.Store(&rrPool{targets: targets})
}

func (rr *RoundRobin) ejections() *Ejections { return rr.ej }

func (rr *RoundRobin) Targets() []string {
	p := rr.pool.Load()
	return append([]string(nil), p.targets...)
}

// Random selects a target at random.
type Random struct {
	pool atomic.Pointer[randPool]
	ej   *Ejections
}

type randPool struct {
	targets []string
}

// NewRandom creates a random balancer without ejection.
func NewRandom(targets []string) *Random {
	return newRandom(targets, nil)
}

func newRandom(targets []string, ej *Ejections) *Random {
	r := &Random{ej: ej}
	r.pool.Store(&randPool{targets: targets})
	return r
}

func (r *Random) Next() string { return r.NextExcept(nil) }

func (r *Random) NextExcept(exclude []string) string {
	return pickRandom(r.pool.Load().targets, r.ej, exclude)
}

// pickRandom picks a random available target: a random start, then the first
// available one after it, so ejected targets never starve the others.
func pickRandom(targets []string, ej *Ejections, exclude []string) string {
	n := len(targets)
	if n == 0 {
		return ""
	}
	start := rand.Intn(n)
	if !ej.filtering(exclude) {
		return targets[start]
	}
	for i := 0; i < n; i++ {
		if t := targets[(start+i)%n]; ej.available(t, exclude) {
			return t
		}
	}
	return ""
}

func (r *Random) Done(string) {}

func (r *Random) Eject(target string, cause error) { r.ej.eject(target, cause) }

// Take reports whether the target is in the pool. Random keeps no per-target
// state, so nothing is reserved.
func (r *Random) Take(target string) bool {
	return contains(r.pool.Load().targets, target)
}

func (r *Random) SwapTargets(targets []string) {
	r.swap(targets)
	r.ej.retain(targets)
}

func (r *Random) swap(targets []string) {
	r.pool.Store(&randPool{targets: targets})
}

func (r *Random) ejections() *Ejections { return r.ej }

func (r *Random) Targets() []string {
	p := r.pool.Load()
	return append([]string(nil), p.targets...)
}

// LeastConn routes to the target with the fewest active connections.
// When multiple targets share the minimum, the first one found is selected.
type LeastConn struct {
	pool atomic.Pointer[lcPool]
	mu   sync.Mutex // protects the find-min-and-increment in Next()
	ej   *Ejections
}

type lcPool struct {
	targets []string
	conns   []atomic.Int64
	index   map[string]int // target → slice index for O(1) lookup
}

// NewLeastConn creates a least-connections balancer without ejection.
func NewLeastConn(targets []string) *LeastConn {
	return newLeastConn(targets, nil)
}

func newLeastConn(targets []string, ej *Ejections) *LeastConn {
	lc := &LeastConn{ej: ej}
	lc.pool.Store(newLCPool(targets))
	return lc
}

func newLCPool(targets []string) *lcPool {
	idx := make(map[string]int, len(targets))
	for i, t := range targets {
		idx[t] = i
	}
	return &lcPool{
		targets: targets,
		conns:   make([]atomic.Int64, len(targets)),
		index:   idx,
	}
}

func (lc *LeastConn) Next() string { return lc.NextExcept(nil) }

// NextExcept returns the available target with the fewest active connections
// and atomically increments its counter.
func (lc *LeastConn) NextExcept(exclude []string) string {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	p := lc.pool.Load()
	filter := lc.ej.filtering(exclude)
	minIdx := -1
	var minVal int64
	for i, t := range p.targets {
		if filter && !lc.ej.available(t, exclude) {
			continue
		}
		if v := p.conns[i].Load(); minIdx < 0 || v < minVal {
			minVal = v
			minIdx = i
		}
	}
	if minIdx < 0 {
		return ""
	}

	p.conns[minIdx].Add(1)
	return p.targets[minIdx]
}

// Done decrements the active connection counter for the target.
func (lc *LeastConn) Done(target string) {
	p := lc.pool.Load()
	if i, ok := p.index[target]; ok {
		p.conns[i].Add(-1)
	}
}

func (lc *LeastConn) Eject(target string, cause error) { lc.ej.eject(target, cause) }

// Take reserves the named target, incrementing its connection counter exactly
// as Next() would. Reports false when the target is not in the pool, in which
// case nothing is reserved.
func (lc *LeastConn) Take(target string) bool {
	p := lc.pool.Load()
	i, ok := p.index[target]
	if !ok {
		return false
	}
	p.conns[i].Add(1)
	return true
}

// SwapTargets atomically replaces the target pool.
// Active connection counts are reset to zero.
func (lc *LeastConn) SwapTargets(targets []string) {
	lc.swap(targets)
	lc.ej.retain(targets)
}

func (lc *LeastConn) swap(targets []string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	lc.pool.Store(newLCPool(targets))
}

func (lc *LeastConn) ejections() *Ejections { return lc.ej }

func (lc *LeastConn) Targets() []string {
	p := lc.pool.Load()
	return append([]string(nil), p.targets...)
}

// Conns returns the current active connection count for a target.
// Intended for testing and diagnostics only.
func (lc *LeastConn) Conns(target string) int64 {
	p := lc.pool.Load()
	if i, ok := p.index[target]; ok {
		return p.conns[i].Load()
	}
	return 0
}

// KeyedBalancer extends Balancer with key-based target selection.
// Used by grouped balancers where the key (e.g., a domain wildcard capture)
// determines which sub-pool of targets to pick from.
type KeyedBalancer interface {
	Balancer
	NextKeyed(key string) string
	// NextKeyedExcept is NextKeyed that also skips the excluded targets.
	NextKeyedExcept(key string, exclude []string) string
	SwapGroupedTargets(groups map[string][]string)
}

// TargetTaker is implemented by balancers that can reserve one named target
// instead of choosing one. It backs plugin-pinned targets: the accounting
// mirrors Next(), so a later Done() stays balanced.
type TargetTaker interface {
	// Take reserves the target and reports whether it belongs to the pool.
	// When it reports false nothing was reserved and Done() must not be called.
	Take(target string) bool
}

// Grouped wraps a balancing strategy and provides per-key target pools.
// In flat mode (via SwapTargets), all requests use the inner balancer.
// In grouped mode (via SwapGroupedTargets), each key has its own sub-balancer.
// All pools share the inner balancer's ejection state.
//
// When fallback is enabled, NextKeyed picks a random target from all groups
// if the requested key has no matching group (or the group is empty).
type Grouped struct {
	strategy string
	fallback bool     // pick random from all groups when key not found
	inner    strategy // flat-mode fallback
	groups   atomic.Pointer[groupedMap]
	mu       sync.Mutex // protects SwapGroupedTargets
}

type groupedMap struct {
	m           map[string]strategy
	targetGroup map[string]string // target → group key for O(1) Done() routing
	allTargets  []string          // flat list of all targets for fallback selection
}

// NewGrouped creates a grouped balancer wrapping an inner flat balancer built
// by this package. The strategy name is used to create per-key sub-balancers.
func NewGrouped(kind string, inner Balancer) *Grouped {
	return &Grouped{
		strategy: kind,
		inner:    inner.(strategy),
	}
}

// SetFallback enables or disables random fallback across all groups.
// When enabled, NextKeyed picks a random target from all groups if
// the requested key has no matching group or the group pool is empty.
func (g *Grouped) SetFallback(enabled bool) {
	g.fallback = enabled
}

// Next delegates to the inner flat balancer. When the inner pool is empty
// and fallback is enabled, picks a random target from all grouped targets.
func (g *Grouped) Next() string { return g.NextExcept(nil) }

func (g *Grouped) NextExcept(exclude []string) string {
	if target := g.inner.NextExcept(exclude); target != "" {
		return target
	}
	if g.fallback {
		if gm := g.groups.Load(); gm != nil {
			return g.pickFallback(gm, exclude)
		}
	}
	return ""
}

// NextKeyed selects a target from the sub-pool matching the key.
// Falls back to the inner balancer if no groups are configured or key is empty.
// When fallback is enabled, picks a random target from all groups if the
// key has no matching group or every target of the matched group is unavailable.
func (g *Grouped) NextKeyed(key string) string { return g.NextKeyedExcept(key, nil) }

func (g *Grouped) NextKeyedExcept(key string, exclude []string) string {
	if gm := g.groups.Load(); gm != nil && key != "" {
		if bal, ok := gm.m[key]; ok {
			if target := bal.NextExcept(exclude); target != "" {
				return target
			}
		}
		if g.fallback {
			return g.pickFallback(gm, exclude)
		}
		return ""
	}
	return g.inner.NextExcept(exclude)
}

// pickFallback picks a random available target across all groups and
// reserves it in the sub-pool that owns it, so Done() stays balanced.
func (g *Grouped) pickFallback(gm *groupedMap, exclude []string) string {
	target := pickRandom(gm.allTargets, g.inner.ejections(), exclude)
	if target != "" {
		if tt, ok := gm.m[gm.targetGroup[target]].(TargetTaker); ok {
			tt.Take(target)
		}
	}
	return target
}

// Done decrements the active connection counter for the target.
// Checks grouped sub-balancers first, then falls back to the inner balancer.
func (g *Grouped) Done(target string) {
	if gm := g.groups.Load(); gm != nil {
		if key, ok := gm.targetGroup[target]; ok {
			gm.m[key].Done(target)
		}
		return
	}
	g.inner.Done(target)
}

func (g *Grouped) Eject(target string, cause error) { g.inner.Eject(target, cause) }

// Take reserves the named target in the sub-pool that owns it, so a pinned
// target is tracked by the same sub-balancer that would have selected it.
// Falls back to the inner balancer in flat mode.
func (g *Grouped) Take(target string) bool {
	if gm := g.groups.Load(); gm != nil {
		key, ok := gm.targetGroup[target]
		if !ok {
			return false
		}
		if tt, ok := gm.m[key].(TargetTaker); ok {
			return tt.Take(target)
		}
		return true
	}
	if tt, ok := g.inner.(TargetTaker); ok {
		return tt.Take(target)
	}
	return false
}

// SwapTargets replaces the flat target pool and clears any grouped state.
func (g *Grouped) SwapTargets(targets []string) {
	g.inner.SwapTargets(targets)
	g.groups.Store(nil)
}

func (g *Grouped) Targets() []string {
	return g.inner.Targets()
}

// SwapGroupedTargets atomically replaces the per-key target pools.
// Existing sub-balancers for unchanged keys are reused. Targets that left
// every group lose their ejection.
func (g *Grouped) SwapGroupedTargets(groups map[string][]string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ej := g.inner.ejections()
	old := g.groups.Load()
	newMap := make(map[string]strategy, len(groups))

	for key, targets := range groups {
		if old != nil {
			if existing, ok := old.m[key]; ok {
				existing.swap(targets)
				newMap[key] = existing
				continue
			}
		}
		newMap[key] = newStrategy(g.strategy, targets, ej)
	}

	tg := make(map[string]string, len(groups)*2)
	var all []string
	for key, targets := range groups {
		for _, t := range targets {
			tg[t] = key
		}
		all = append(all, targets...)
	}

	ej.retain(all)
	g.groups.Store(&groupedMap{m: newMap, targetGroup: tg, allTargets: all})
}

// NewByType creates a flat balancer of the given type that ejects failing
// targets with ej (nil disables ejection).
// Valid types: "random", "leastconn", "roundrobin" (default).
func NewByType(strategy string, targets []string, ej *Ejections) Balancer {
	return newStrategy(strategy, targets, ej)
}

func newStrategy(strategy string, targets []string, ej *Ejections) strategy {
	switch strategy {
	case "random":
		return newRandom(targets, ej)
	case "leastconn":
		return newLeastConn(targets, ej)
	default:
		return newRoundRobin(targets, ej)
	}
}

func contains(targets []string, target string) bool {
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}

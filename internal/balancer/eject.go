package balancer

import (
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Ejections tracks the targets of one route that are out of rotation after a
// failed dial. A nil *Ejections ejects nothing.
type Ejections struct {
	cooldown time.Duration
	active   atomic.Int32 // ejected targets; zero keeps selection on its fast path
	now      func() time.Time

	mu    sync.Mutex
	until map[string]time.Time
}

// NewEjections returns ejection state with the given cool-down, or nil when
// the cool-down is not positive, which disables ejection.
func NewEjections(cooldown time.Duration) *Ejections {
	if cooldown <= 0 {
		return nil
	}
	return &Ejections{cooldown: cooldown, now: time.Now, until: make(map[string]time.Time)}
}

// eject skips target until the cool-down passes. Repeated failures while the
// target is out extend the cool-down without logging again.
func (e *Ejections) eject(target string, cause error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	_, out := e.until[target]
	e.until[target] = e.now().Add(e.cooldown)
	if !out {
		e.active.Add(1)
	}
	e.mu.Unlock()
	if !out {
		slog.Warn("upstream target ejected",
			"target", target,
			"cooldown", e.cooldown,
			"err", cause,
		)
	}
}

// skip reports whether target is out of rotation, readmitting it once its
// cool-down has passed.
func (e *Ejections) skip(target string) bool {
	if e == nil || e.active.Load() == 0 {
		return false
	}
	e.mu.Lock()
	until, out := e.until[target]
	readmit := out && !e.now().Before(until)
	if readmit {
		delete(e.until, target)
		e.active.Add(-1)
	}
	e.mu.Unlock()
	if readmit {
		slog.Info("upstream target readmitted", "target", target)
	}
	return out && !readmit
}

// filtering reports whether selection must check targets one by one.
func (e *Ejections) filtering(exclude []string) bool {
	return len(exclude) > 0 || (e != nil && e.active.Load() > 0)
}

// available reports whether target may be selected.
func (e *Ejections) available(target string, exclude []string) bool {
	return !slices.Contains(exclude, target) && !e.skip(target)
}

// retain forgets ejected targets that are no longer in the pool.
func (e *Ejections) retain(targets []string) {
	if e == nil || e.active.Load() == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for t := range e.until {
		if !slices.Contains(targets, t) {
			delete(e.until, t)
			e.active.Add(-1)
		}
	}
}

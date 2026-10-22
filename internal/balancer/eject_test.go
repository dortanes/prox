package balancer

import (
	"errors"
	"testing"
	"time"
)

var errRefused = errors.New("connection refused")

// clock is a settable time source for cool-down tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func ejecting(cooldown time.Duration) (*Ejections, *clock) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	ej := NewEjections(cooldown)
	ej.now = c.now
	return ej, c
}

func strategies(targets []string, ej *Ejections) map[string]Balancer {
	return map[string]Balancer{
		"roundrobin": newRoundRobin(targets, ej),
		"random":     newRandom(targets, ej),
		"leastconn":  newLeastConn(targets, ej),
	}
}

func TestEject_SkipsTarget(t *testing.T) {
	for name, b := range strategies([]string{"a:1", "b:1", "c:1"}, NewEjections(time.Minute)) {
		t.Run(name, func(t *testing.T) {
			b.Eject("a:1", errRefused)
			for i := 0; i < 50; i++ {
				got := b.Next()
				if got == "a:1" || got == "" {
					t.Fatalf("pick %d = %q, want b:1 or c:1", i, got)
				}
				b.Done(got)
			}
		})
	}
}

func TestEject_ReadmitsAfterCooldown(t *testing.T) {
	for _, name := range []string{"roundrobin", "random", "leastconn"} {
		t.Run(name, func(t *testing.T) {
			ej, c := ejecting(10 * time.Second)
			b := strategies([]string{"a:1"}, ej)[name]
			b.Eject("a:1", errRefused)

			c.t = c.t.Add(9 * time.Second)
			if got := b.Next(); got != "" {
				t.Fatalf("during cool-down Next = %q, want empty", got)
			}
			c.t = c.t.Add(time.Second)
			if got := b.Next(); got != "a:1" {
				t.Fatalf("after cool-down Next = %q, want a:1", got)
			}
		})
	}
}

func TestEject_RepeatedFailureExtendsCooldown(t *testing.T) {
	ej, c := ejecting(10 * time.Second)
	b := newRoundRobin([]string{"a:1"}, ej)
	b.Eject("a:1", errRefused)
	c.t = c.t.Add(5 * time.Second)
	b.Eject("a:1", errRefused)
	c.t = c.t.Add(6 * time.Second)
	if got := b.Next(); got != "" {
		t.Fatalf("Next = %q, want empty until the extended cool-down passes", got)
	}
}

func TestEject_Disabled(t *testing.T) {
	b := NewByType("roundrobin", []string{"a:1"}, NewEjections(0))
	b.Eject("a:1", errRefused)
	if got := b.Next(); got != "a:1" {
		t.Fatalf("Next = %q, want a:1 with ejection disabled", got)
	}
}

func TestEject_SwapForgetsDepartedTargets(t *testing.T) {
	b := NewByType("roundrobin", []string{"a:1", "b:1"}, NewEjections(time.Minute))
	b.Eject("a:1", errRefused)
	b.Eject("b:1", errRefused)

	b.SwapTargets([]string{"b:1"})
	b.SwapTargets([]string{"a:1", "b:1"})
	for i := 0; i < 4; i++ {
		if got := b.Next(); got != "a:1" {
			t.Fatalf("pick %d = %q, want a:1 (re-added, b:1 still ejected)", i, got)
		}
	}
}

func TestNextExcept(t *testing.T) {
	for name, b := range strategies([]string{"a:1", "b:1"}, nil) {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				if got := b.NextExcept([]string{"a:1"}); got != "b:1" {
					t.Fatalf("NextExcept = %q, want b:1", got)
				}
			}
			if got := b.NextExcept([]string{"a:1", "b:1"}); got != "" {
				t.Fatalf("NextExcept(all) = %q, want empty", got)
			}
		})
	}
}

func TestLeastConn_EjectedMinimumIsSkipped(t *testing.T) {
	lc := newLeastConn([]string{"a:1", "b:1"}, NewEjections(time.Minute))
	lc.Take("b:1")
	lc.Eject("a:1", errRefused)
	if got := lc.Next(); got != "b:1" {
		t.Fatalf("Next = %q, want b:1 although a:1 has fewer connections", got)
	}
	if lc.Conns("a:1") != 0 || lc.Conns("b:1") != 2 {
		t.Fatalf("conns a=%d b=%d, want 0 and 2", lc.Conns("a:1"), lc.Conns("b:1"))
	}
}

func groupedWithEjection(kind string, fallback bool) *Grouped {
	g := NewGrouped(kind, NewByType(kind, nil, NewEjections(time.Minute)))
	g.SetFallback(fallback)
	g.SwapGroupedTargets(map[string][]string{
		"de": {"de-1:8080", "de-2:8080"},
		"us": {"us-1:8080"},
	})
	return g
}

func TestGrouped_EjectionStaysInGroup(t *testing.T) {
	g := groupedWithEjection("roundrobin", false)
	g.Eject("de-1:8080", errRefused)
	for i := 0; i < 10; i++ {
		if got := g.NextKeyed("de"); got != "de-2:8080" {
			t.Fatalf("NextKeyed(de) = %q, want de-2:8080", got)
		}
	}
}

func TestGrouped_AllEjectedGroupFallsBack(t *testing.T) {
	without := groupedWithEjection("roundrobin", false)
	without.Eject("de-1:8080", errRefused)
	without.Eject("de-2:8080", errRefused)
	if got := without.NextKeyed("de"); got != "" {
		t.Fatalf("NextKeyed(de) without fallback = %q, want empty", got)
	}

	with := groupedWithEjection("roundrobin", true)
	with.Eject("de-1:8080", errRefused)
	with.Eject("de-2:8080", errRefused)
	for i := 0; i < 10; i++ {
		if got := with.NextKeyed("de"); got != "us-1:8080" {
			t.Fatalf("NextKeyed(de) with fallback = %q, want us-1:8080", got)
		}
	}
}

func TestGrouped_SwapKeepsEjectionOfOtherGroups(t *testing.T) {
	g := groupedWithEjection("roundrobin", false)
	g.Eject("us-1:8080", errRefused)
	g.SwapGroupedTargets(map[string][]string{
		"de": {"de-3:8080"},
		"us": {"us-1:8080"},
	})
	if got := g.NextKeyed("us"); got != "" {
		t.Fatalf("NextKeyed(us) = %q, want empty while us-1:8080 is ejected", got)
	}
}

func TestGrouped_FallbackKeepsLeastConnBalanced(t *testing.T) {
	g := groupedWithEjection("leastconn", true)
	g.Eject("de-1:8080", errRefused)
	g.Eject("de-2:8080", errRefused)

	got := g.NextKeyed("de")
	g.Done(got)
	us := g.groups.Load().m["us"].(*LeastConn)
	if c := us.Conns("us-1:8080"); c != 0 {
		t.Fatalf("us-1:8080 conns after Done = %d, want 0", c)
	}
}

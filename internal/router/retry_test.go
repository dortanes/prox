package router

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/dortanes/prox/internal/balancer"
	"github.com/dortanes/prox/internal/config"
)

var errRefused = errors.New("connection refused")

// groupedRouter routes *.example.com through a grouped leastconn balancer
// keyed by the first domain label.
func groupedRouter(t *testing.T) *Router {
	t.Helper()
	rt := New([]*config.Route{{
		Match:    &config.Match{Domain: "*.example.com"},
		Balancer: &config.BalancerConfig{Type: config.BalancerLeastConn},
		Action:   config.ActionRef{Name: "proxy"},
	}})
	g := balancer.NewGrouped("leastconn", rt.RouteBalancer(0))
	g.SwapGroupedTargets(map[string][]string{
		"de": {"de-1:8080", "de-2:8080"},
		"us": {"us-1:8080"},
	})
	rt.SetRouteBalancer(0, g)
	return rt
}

func match(t *testing.T, rt *Router, host string) *MatchResult {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = host
	r, _ := rt.Match(req)
	m := GetMatchResult(r)
	if m == nil {
		t.Fatalf("no match for %s", host)
	}
	return m
}

func TestRetry_StaysInGroup(t *testing.T) {
	rt := groupedRouter(t)
	m := match(t, rt, "de.example.com")
	first := m.Target

	second := m.Retry(errRefused)
	if second == "" || second == first || second == "us-1:8080" {
		t.Fatalf("retry after %s = %q, want the other de target", first, second)
	}
	if third := m.Retry(errRefused); third != "" {
		t.Fatalf("third attempt = %q, want empty once the group is exhausted", third)
	}
}

func TestRetry_EjectsForLaterRequests(t *testing.T) {
	rt := groupedRouter(t)
	m := match(t, rt, "de.example.com")
	failed := m.Target
	m.Retry(errRefused)
	m.Done()

	for i := 0; i < 5; i++ {
		next := match(t, rt, "de.example.com")
		if next.Target == failed {
			t.Fatalf("request %d picked ejected target %s", i, failed)
		}
		next.Done()
	}
}

func TestRetry_KeepsLeastConnBalanced(t *testing.T) {
	rt := New([]*config.Route{{
		Balancer: &config.BalancerConfig{Type: config.BalancerLeastConn, Targets: []string{"a:1", "b:1"}},
		Action:   config.ActionRef{Name: "proxy"},
	}})
	lc := rt.RouteBalancer(0).(*balancer.LeastConn)

	m := match(t, rt, "any.test")
	failed := m.Target
	next := m.Retry(errRefused)
	if lc.Conns(failed) != 0 || lc.Conns(next) != 1 {
		t.Fatalf("in flight: %s=%d %s=%d, want 0 and 1", failed, lc.Conns(failed), next, lc.Conns(next))
	}
	m.Done()
	if lc.Conns(failed) != 0 || lc.Conns(next) != 0 {
		t.Fatalf("after Done: %s=%d %s=%d, want 0 and 0", failed, lc.Conns(failed), next, lc.Conns(next))
	}
}

func TestRetry_PinnedTargetIsNotReplaced(t *testing.T) {
	rt := groupedRouter(t)
	m := match(t, rt, "de.example.com")
	m.SelectTarget("de-1:8080")

	if got := m.Retry(errRefused); got != "" {
		t.Fatalf("retry of a pinned target = %q, want empty", got)
	}
	next := match(t, rt, "de.example.com")
	defer next.Done()
	if next.Target != "de-2:8080" {
		t.Fatalf("next pick = %q, want de-2:8080 while de-1:8080 is ejected", next.Target)
	}
}

func TestRetry_FlatBalancer(t *testing.T) {
	rt := New([]*config.Route{{
		Balancer: &config.BalancerConfig{Type: config.BalancerRoundRobin, Targets: []string{"a:1", "b:1"}},
		Action:   config.ActionRef{Name: "proxy"},
	}})
	m := match(t, rt, "any.test")
	first := m.Target
	if got := m.Retry(errRefused); got == "" || got == first {
		t.Fatalf("retry after %s = %q, want the other target", first, got)
	}
	if got := m.Retry(errRefused); got != "" {
		t.Fatalf("retry with every target tried = %q, want empty", got)
	}
}

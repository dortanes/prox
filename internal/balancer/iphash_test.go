package balancer

import (
	"errors"
	"testing"
	"time"
)

func TestIPHash_KeepsClientOnTarget(t *testing.T) {
	targets := []string{"de-1:8080", "de-2:8080", "us-1:8080"}
	b := NewByType("iphash", targets, nil)

	first := Pick(b, "", "192.0.2.1", nil)
	for range 20 {
		if got := Pick(b, "", "192.0.2.1", nil); got != first {
			t.Fatalf("client moved from %s to %s", first, got)
		}
	}

	// Removing another target must not move the client.
	var rest []string
	for _, target := range targets {
		if target == first {
			rest = append(rest, target)
		}
	}
	for _, target := range targets {
		if target != first && len(rest) < 2 {
			rest = append(rest, target)
		}
	}
	b.SwapTargets(rest)
	if got := Pick(b, "", "192.0.2.1", nil); got != first {
		t.Fatalf("client moved to %s after an unrelated removal", got)
	}
}

func TestIPHash_SkipsEjectedTarget(t *testing.T) {
	b := NewByType("iphash", []string{"de-1:8080", "de-2:8080"}, NewEjections(time.Minute))
	first := Pick(b, "", "192.0.2.1", nil)
	b.Eject(first, errors.New("refused"))
	if got := Pick(b, "", "192.0.2.1", nil); got == first || got == "" {
		t.Fatalf("got %q after ejecting %q", got, first)
	}
}

func TestIPHash_Grouped(t *testing.T) {
	g := NewGrouped("iphash", NewByType("iphash", nil, nil))
	g.SwapGroupedTargets(map[string][]string{"de": {"de-1:8080", "de-2:8080"}, "us": {"us-1:8080"}})

	first := Pick(g, "de", "192.0.2.1", nil)
	if first != "de-1:8080" && first != "de-2:8080" {
		t.Fatalf("picked %q outside the group", first)
	}
	if got := Pick(g, "de", "192.0.2.1", nil); got != first {
		t.Fatalf("client moved from %s to %s", first, got)
	}
	if got := Pick(g, "us", "192.0.2.1", nil); got != "us-1:8080" {
		t.Fatalf("group us picked %q", got)
	}
}

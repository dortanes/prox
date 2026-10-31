package server

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/config"
)

type fakeDomainManager struct {
	mu      sync.Mutex
	domains []string
	applied chan []string
}

func newFakeDomainManager(domains ...string) *fakeDomainManager {
	return &fakeDomainManager{domains: domains, applied: make(chan []string, 8)}
}

func (f *fakeDomainManager) ManagedDomains() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.domains)
}

func (f *fakeDomainManager) ManageDomains(_ context.Context, domains []string) error {
	f.mu.Lock()
	f.domains = domains
	f.mu.Unlock()
	f.applied <- domains
	return nil
}

func TestRediscoverZones_AppliesChangedZones(t *testing.T) {
	known := []string{"example.com", "*.example.com"}
	added := []string{"example.com", "*.example.com", "example.org", "*.example.org"}

	m := newFakeDomainManager(known...)
	var mu sync.Mutex
	calls := 0
	discover := func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls <= 3 {
			return known, nil
		}
		return added, nil
	}

	g := &Group{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		g.rediscoverZones(ctx, "web", m, time.Millisecond, discover)
		close(done)
	}()

	select {
	case got := <-m.applied:
		mu.Lock()
		n := calls
		mu.Unlock()
		if n < 4 {
			t.Errorf("applied after %d discoveries, want the unchanged ones skipped", n)
		}
		if !slicesEqual(got, added) {
			t.Errorf("applied %v, want %v", got, added)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new zone was not applied")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("rediscovery did not stop after cancel")
	}
}

func TestRediscoverZones_DropsResultAfterCancel(t *testing.T) {
	m := newFakeDomainManager("example.com", "*.example.com")
	started := make(chan struct{})
	release := make(chan struct{})
	discover := func(context.Context) ([]string, error) {
		close(started)
		<-release
		return []string{"example.org", "*.example.org"}, nil
	}

	g := &Group{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		g.rediscoverZones(ctx, "web", m, time.Millisecond, discover)
		close(done)
	}()

	<-started
	cancel()
	close(release)
	<-done

	select {
	case got := <-m.applied:
		t.Errorf("cancelled rediscovery applied %v", got)
	default:
	}
}

func TestExtractDomains(t *testing.T) {
	routes := []*config.Route{
		{Match: &config.Match{Domain: "example.com"}},
		{Match: &config.Match{Domain: "api.example.com"}},
		{Match: &config.Match{Domain: "*.example.com"}},     // wildcard OK
		{Match: &config.Match{Domain: "cdn-*.example.com"}}, // partial wildcard → skip
		{Match: &config.Match{Domain: "*.example.**"}},      // glob → skip
		{Match: nil},                                  // nil match → skip
		{Match: &config.Match{Path: "/api/*"}},        // no domain → skip
		{Match: &config.Match{Domain: "example.com"}}, // duplicate → skip
	}

	domains := extractDomains(routes)

	expected := map[string]bool{
		"example.com":     true,
		"api.example.com": true,
		"*.example.com":   true,
	}

	if len(domains) != len(expected) {
		t.Fatalf("expected %d domains, got %d: %v", len(expected), len(domains), domains)
	}

	for _, d := range domains {
		if !expected[d] {
			t.Errorf("unexpected domain: %q", d)
		}
	}
}

func TestExtractDomains_Empty(t *testing.T) {
	domains := extractDomains(nil)
	if len(domains) != 0 {
		t.Errorf("expected 0 domains, got %d", len(domains))
	}
}

func TestSlicesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", []string{}, []string{}, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"b", "a"}, []string{"a", "b"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different content", []string{"a", "b"}, []string{"a", "c"}, false},
		{"a nil b not", nil, []string{"a"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slicesEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("slicesEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

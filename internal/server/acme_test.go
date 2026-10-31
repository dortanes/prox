package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	proxacme "github.com/dortanes/prox/internal/acme"
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

func TestRediscoverZones_BackoffStopsAfterSuccess(t *testing.T) {
	setRediscoverRetry(t, time.Millisecond)
	zones := []string{"example.com", "*.example.com"}

	m := newFakeDomainManager()
	var calls atomic.Int32
	discover := func(context.Context) ([]string, error) {
		if calls.Add(1) <= 3 {
			return nil, errors.New("lookup failed")
		}
		return zones, nil
	}

	g := &Group{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		g.rediscoverZones(ctx, "web", m, time.Hour, discover)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case got := <-m.applied:
		if !slicesEqual(got, zones) {
			t.Fatalf("applied %v, want %v", got, zones)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("zones were not applied after failed discoveries")
	}

	n := calls.Load()
	time.Sleep(100 * time.Millisecond)
	if extra := calls.Load() - n; extra != 0 {
		t.Errorf("discovered %d more times after success, want the normal interval", extra)
	}
}

func TestStartACME_RecoversFromFailedDiscovery(t *testing.T) {
	setRediscoverRetry(t, time.Millisecond)
	zones := []string{"example.com", "*.example.com"}

	var calls atomic.Int32
	setDiscoverDomains(t, func(context.Context, *config.ACMEDNSConfig) ([]string, error) {
		// The first call is the startup discovery.
		if calls.Add(1) <= 2 {
			return nil, errors.New("lookup failed")
		}
		return zones, nil
	})

	g, ms := newDiscoveryGroup(t)
	g.startACME(context.Background(), g.cfg)

	deadline := time.Now().Add(5 * time.Second)
	for !slicesEqual(ms.acme.ManagedDomains(), zones) {
		if time.Now().After(deadline) {
			t.Fatalf("managed %v after %d discoveries, want %v", ms.acme.ManagedDomains(), calls.Load(), zones)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSyncACMEDomains_StopsRetries(t *testing.T) {
	setRediscoverRetry(t, time.Millisecond)

	var live atomic.Int32
	retried := make(chan struct{}, 1)
	setDiscoverDomains(t, func(ctx context.Context, _ *config.ACMEDNSConfig) ([]string, error) {
		if ctx.Err() == nil && live.Add(1) >= 3 {
			select {
			case retried <- struct{}{}:
			default:
			}
		}
		return nil, errors.New("lookup failed")
	})

	g, ms := newDiscoveryGroup(t)
	g.startACME(context.Background(), g.cfg)

	select {
	case <-retried:
	case <-time.After(2 * time.Second):
		t.Fatal("failed discovery was not retried")
	}

	svc := *g.cfg.Services["web"]
	acme := *svc.ACME
	acme.Domains = []string{"example.net"}
	svc.ACME = &acme
	g.syncACMEDomains(ms, &svc)

	n := live.Load()
	time.Sleep(50 * time.Millisecond)
	if extra := live.Load() - n; extra != 0 {
		t.Errorf("discovery retried %d times after reload", extra)
	}
}

// newDiscoveryGroup builds a group with one service that discovers its zones
// and has no route domains to fall back to.
func newDiscoveryGroup(t *testing.T) (*Group, *managedServer) {
	t.Helper()

	ca := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(ca.Close)

	// Not t.TempDir: cancelled obtains still release their storage locks after Close returns.
	storage, err := os.MkdirTemp("", "prox-acme-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storage) })

	svc := &config.Service{
		Listen: ":443",
		TLS:    true,
		ACME: &config.ACMEConfig{
			Email:     "admin@example.com",
			CA:        ca.URL,
			Challenge: "dns",
			Storage:   storage,
			DNS: &config.ACMEDNSConfig{
				Provider:         "cloudflare",
				Token:            "test",
				Discover:         true,
				DiscoverInterval: &config.Duration{Duration: time.Hour},
			},
		},
		Routes: []*config.Route{{Match: &config.Match{Domain: "*.**"}}},
	}

	m, err := proxacme.New(svc.ACME, "")
	if err != nil {
		t.Fatalf("acme.New: %v", err)
	}
	ms := &managedServer{name: "web", acme: m}
	g := &Group{
		servers: []*managedServer{ms},
		cfg:     &config.Config{Services: map[string]*config.Service{"web": svc}},
	}
	t.Cleanup(func() {
		g.acmeMu.Lock()
		if ms.stopRediscovery != nil {
			ms.stopRediscovery()
		}
		g.acmeMu.Unlock()
		m.Close()
	})
	return g, ms
}

func setRediscoverRetry(t *testing.T, d time.Duration) {
	t.Helper()
	prev := rediscoverRetry
	rediscoverRetry = d
	t.Cleanup(func() { rediscoverRetry = prev })
}

func setDiscoverDomains(t *testing.T, fn func(context.Context, *config.ACMEDNSConfig) ([]string, error)) {
	t.Helper()
	prev := discoverDomains
	discoverDomains = fn
	t.Cleanup(func() { discoverDomains = prev })
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

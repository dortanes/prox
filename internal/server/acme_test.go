package server

import (
	"testing"

	"github.com/labostack/prox/internal/config"
)

func TestExtractDomains(t *testing.T) {
	routes := []*config.Route{
		{Match: &config.Match{Domain: "example.com"}},
		{Match: &config.Match{Domain: "api.example.com"}},
		{Match: &config.Match{Domain: "*.example.com"}},    // wildcard OK
		{Match: &config.Match{Domain: "cdn-*.example.com"}}, // partial wildcard → skip
		{Match: &config.Match{Domain: "*.example.**"}},      // glob → skip
		{Match: nil},                                        // nil match → skip
		{Match: &config.Match{Path: "/api/*"}},              // no domain → skip
		{Match: &config.Match{Domain: "example.com"}},       // duplicate → skip
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

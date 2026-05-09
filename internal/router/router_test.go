package router

import (
	"net/http"
	"testing"

	"github.com/dortanes/prox/internal/config"
)

func TestRouter_ExactMatch(t *testing.T) {
	rt := New([]*config.Route{
		{
			Match:  &config.Match{Path: "/styles.css"},
			Action: config.ActionRef{Name: "serve_css"},
		},
	})

	r, _ := http.NewRequest("GET", "/styles.css", nil)
	if got := rt.Match(r); got != "serve_css" {
		t.Errorf("expected serve_css, got %q", got)
	}
}

func TestRouter_WildcardMatch(t *testing.T) {
	rt := New([]*config.Route{
		{
			Match:  &config.Match{Path: "/api/*"},
			Action: config.ActionRef{Name: "proxy_backend"},
		},
	})

	tests := []struct {
		path string
		want string
	}{
		{"/api/users", "proxy_backend"},
		{"/api/users/123", "proxy_backend"},
		{"/api/", "proxy_backend"},
		{"/other", ""},
	}

	for _, tc := range tests {
		r, _ := http.NewRequest("GET", tc.path, nil)
		if got := rt.Match(r); got != tc.want {
			t.Errorf("path %q: expected %q, got %q", tc.path, tc.want, got)
		}
	}
}

func TestRouter_MethodFilter(t *testing.T) {
	rt := New([]*config.Route{
		{
			Match:  &config.Match{Path: "/data", Methods: []string{"GET", "HEAD"}},
			Action: config.ActionRef{Name: "get_data"},
		},
		{
			Match:  &config.Match{Path: "/data", Methods: []string{"POST"}},
			Action: config.ActionRef{Name: "post_data"},
		},
	})

	tests := []struct {
		method string
		want   string
	}{
		{"GET", "get_data"},
		{"HEAD", "get_data"},
		{"POST", "post_data"},
		{"DELETE", ""},
	}

	for _, tc := range tests {
		r, _ := http.NewRequest(tc.method, "/data", nil)
		if got := rt.Match(r); got != tc.want {
			t.Errorf("method %s: expected %q, got %q", tc.method, tc.want, got)
		}
	}
}

func TestRouter_FirstMatchWins(t *testing.T) {
	rt := New([]*config.Route{
		{
			Match:  &config.Match{Path: "/api/special"},
			Action: config.ActionRef{Name: "special"},
		},
		{
			Match:  &config.Match{Path: "/api/*"},
			Action: config.ActionRef{Name: "general"},
		},
	})

	r, _ := http.NewRequest("GET", "/api/special", nil)
	if got := rt.Match(r); got != "special" {
		t.Errorf("expected first-match 'special', got %q", got)
	}
}

func TestRouter_NoMatch(t *testing.T) {
	rt := New([]*config.Route{
		{
			Match:  &config.Match{Path: "/known"},
			Action: config.ActionRef{Name: "handler"},
		},
	})

	r, _ := http.NewRequest("GET", "/unknown", nil)
	if got := rt.Match(r); got != "" {
		t.Errorf("expected empty string for no match, got %q", got)
	}
}

func TestRouter_AllMethodsWhenEmpty(t *testing.T) {
	rt := New([]*config.Route{
		{
			Match:  &config.Match{Path: "/open"},
			Action: config.ActionRef{Name: "open"},
		},
	})

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}
	for _, m := range methods {
		r, _ := http.NewRequest(m, "/open", nil)
		if got := rt.Match(r); got != "open" {
			t.Errorf("method %s: expected 'open', got %q", m, got)
		}
	}
}
